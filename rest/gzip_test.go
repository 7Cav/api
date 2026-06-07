package rest_test

// Behavioral tests for the gzip layer's ResponseController support (#167):
// a handler behind GzipMiddleware must be able to flush mid-body without
// corrupting the compressed stream. Driven over real server connections
// (httptest.NewServer) — the recorder implements Flusher directly and would
// mask a dead tunnel — with Accept-Encoding set explicitly so the transport
// neither injects the header nor transparently decompresses: the tests read
// the raw gzip bytes exactly as a streaming consumer would.

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ResponseController.Flush from a handler on a gzip-negotiated request must
// succeed — before #167 gzipResponseWriter implemented neither FlushError nor
// Unwrap, so the controller dead-ended with http.ErrNotSupported and no
// streaming handler could ever flush behind gzip.
func TestGzip_ResponseControllerFlushSucceeds(t *testing.T) {
	flushErr := make(chan error, 1) // handler runs on the server goroutine
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr <- http.NewResponseController(w).Flush()
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"),
		"the request negotiated gzip — the wrapper under test must be in play")
	require.NoError(t, <-flushErr,
		"ResponseController.Flush must succeed through the gzip wrapper")
}

// Non-gzip-negotiated requests are unaffected (#167 acceptance): without
// Accept-Encoding: gzip the middleware passes the writer straight through, so
// a mid-body flush succeeds and the flushed bytes arrive uncompressed.
func TestGzip_NonNegotiatedRequestFlushesPlain(t *testing.T) {
	const body = "plain, never compressed"

	flushErr := make(chan error, 1) // handler runs on the server goroutine
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.WriteString(w, body)
		assert.NoError(t, err)
		flushErr <- http.NewResponseController(w).Flush()
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "identity") // explicit: suppress the transport's auto-gzip
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.NoError(t, <-flushErr, "flush must keep working off the gzip path")
	require.Empty(t, res.Header.Get("Content-Encoding"),
		"no negotiation, no compression")
	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(got), "body must arrive as written, uncompressed")
}

// The other ResponseController verbs (deadline control here, as the witness)
// must tunnel through the gzip wrapper via Unwrap — they don't touch the
// compressed stream, so passing them straight down is safe. Flush is the one
// verb that must NOT take that route; the explicit FlushError above wins the
// controller's method search, so Unwrap never opens the corruption path.
func TestGzip_ResponseControllerDeadlinesTunnelThrough(t *testing.T) {
	deadlineErr := make(chan error, 1) // handler runs on the server goroutine
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadlineErr <- http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.NoError(t, <-deadlineErr,
		"deadline control must reach the connection through the gzip wrapper")
}

// The streaming contract (#167): bytes received after a mid-body flush must
// be incrementally decodable as a gzip stream — the flushed prefix
// decompresses to exactly what the handler wrote before flushing, while the
// handler is still alive and holding the rest. The handler blocks after the
// flush until the client has decoded the prefix, so a flush that left the
// sync block buffered anywhere (gzip.Writer or the chain below) deadlocks the
// decode — the watchdog turns that into a crisp failure. Then the handler
// finishes and the full body must still decompress byte-identically to the
// uncompressed handler output, through an intact trailer.
func TestGzip_MidBodyFlushStreamsDecodablePrefix(t *testing.T) {
	const part1 = "first chunk, flushed mid-body"
	const part2 = "; second chunk, after the flush"

	flushErr := make(chan error, 1) // handler runs on the server goroutine
	release := make(chan struct{})  // client → handler: prefix decoded, finish
	releaseOnce := sync.OnceFunc(func() { close(release) })
	handlerErr := make(chan error, 2)
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.WriteString(w, part1)
		handlerErr <- err
		flushErr <- http.NewResponseController(w).Flush()
		<-release
		_, err = io.WriteString(w, part2)
		handlerErr <- err
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()
	// LIFO: unblock the handler before srv.Close waits on it, so a failure
	// before the deliberate release fails the test instead of hanging it.
	defer releaseOnce()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))

	require.NoError(t, <-handlerErr, "pre-flush write must succeed")
	require.NoError(t, <-flushErr, "mid-body flush must succeed")

	// Decode the flushed prefix while the handler still blocks on release —
	// possible only if the flush pushed a complete sync block to the wire.
	zr, prefix := decodePrefixWithin(t, res.Body, len(part1), 5*time.Second)
	assert.Equal(t, part1, prefix,
		"flushed prefix must decompress to exactly the pre-flush writes")

	releaseOnce()
	rest_, err := io.ReadAll(zr) // EOF verifies the gzip CRC/size trailer
	require.NoError(t, err, "tail must decode through an intact trailer")
	require.NoError(t, <-handlerErr, "post-flush write must succeed")
	assert.Equal(t, part1+part2, prefix+string(rest_),
		"full body must decompress byte-identically to the handler output")
}

// decodePrefixWithin opens a gzip reader over body and decodes exactly n
// bytes, failing the test if that takes longer than timeout — the deadlock
// shape a buffered (non-streamed) flush produces. Returns the open reader so
// the caller can decode the rest of the stream.
func decodePrefixWithin(t *testing.T, body io.Reader, n int, timeout time.Duration) (*gzip.Reader, string) {
	t.Helper()
	type result struct {
		zr  *gzip.Reader
		buf []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		zr, err := gzip.NewReader(body)
		if err != nil {
			done <- result{err: err}
			return
		}
		buf := make([]byte, n)
		_, err = io.ReadFull(zr, buf)
		done <- result{zr: zr, buf: buf, err: err}
	}()
	select {
	case res := <-done:
		require.NoError(t, res.err, "flushed prefix must be decodable as a gzip stream")
		return res.zr, string(res.buf)
	case <-time.After(timeout):
		t.Fatal("flushed prefix never became decodable — the flush left compressed bytes buffered instead of streaming a sync block to the wire")
		return nil, ""
	}
}
