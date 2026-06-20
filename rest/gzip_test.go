package rest_test

// Behavioral tests for the gzip layer's ResponseController support (#167)
// and the stale-Content-Length strips at every header-committing event
// (#175): a handler behind GzipMiddleware must be able to flush mid-body
// without corrupting the compressed stream, and a handler-set uncompressed
// Content-Length must never commit. Driven over real server connections
// (httptest.NewServer) because a recorder cannot carry these tests:
// incremental mid-body delivery is unobservable on a recorder (one buffer,
// no wire timing), the deadline test needs a real connection to set a
// deadline on, only net/http enforces a declared Content-Length (a recorder
// neither truncates nor errors, so the stale-CL corruption is unobservable
// on it), and the dangerous mutant — Unwrap without FlushError — reports
// flush success on recorder and real connection alike, so no error
// assertion can catch it: the catch must come from decoding what was
// actually delivered at flush time. These tests do that on real wire bytes;
// gzip_internal_test.go pins the same property crisply against a snapshot
// fake. Accept-Encoding is set explicitly so the transport neither injects
// the header nor transparently decompresses: the tests read the raw gzip
// bytes exactly as a streaming consumer would.

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// A flush before the first write is a header-committing event, so FlushError
// must strip a stale uncompressed Content-Length exactly as Write does
// (rest/gzip.go). If the stale length commits alongside Content-Encoding:
// gzip, the wire corrupts in one of the two shapes the WriteHeader variant's
// doc lays out (overrun cut vs short close) and the client hits unexpected
// EOF mid-stream — while the flush AND the handler's writes all returned nil,
// so the corruption is invisible to the handler. Regression pin vs pre-#167,
// where the same handler got a loud ErrNotSupported with zero bytes moved.
func TestGzip_FlushBeforeFirstWriteStripsStaleContentLength(t *testing.T) {
	const payload = `{"roster":"live","unit":"7th Cavalry","status":"active"}`

	flushErr := make(chan error, 1) // handler runs on the server goroutine
	writeErr := make(chan error, 1)
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A length-aware handler sets the UNCOMPRESSED length, then flushes
		// before its first write.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		flushErr <- http.NewResponseController(w).Flush()
		_, err := io.WriteString(w, payload)
		writeErr <- err
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	require.NoError(t, <-flushErr, "the flush reports success either way — corruption would be silent")
	require.NoError(t, <-writeErr, "and so does the handler's write")

	zr, err := gzip.NewReader(res.Body)
	require.NoError(t, err, "body must open as a gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err,
		"compressed stream must arrive whole, not truncated at the stale uncompressed Content-Length")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.Equal(t, payload, string(decoded),
		"body must decompress byte-identically to the handler output")
}

// The first Write is the most common header-committing event (#175): a
// handler that never calls WriteHeader commits the implicit 200 at its first
// Write, so the wrapper's Write must strip the stale uncompressed
// Content-Length before delegating — committed, the stale length corrupts the
// wire in one of the two shapes the WriteHeader variant's doc lays out, both
// invisible to the handler (every Write returns nil).
// Same shape as the WriteHeader variant below minus the explicit WriteHeader;
// this is the wire-level pin on this path (the old servers/gateway and its
// recorder-based gzip round-trip test were removed at the #134 cutover, so this
// is now the sole pin).
func TestGzip_FirstWriteStripsStaleContentLength(t *testing.T) {
	const payload = `{"roster":"live","unit":"7th Cavalry","status":"active"}`

	writeErr := make(chan error, 1) // handler runs on the server goroutine
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A length-aware handler sets the UNCOMPRESSED length, then commits
		// it with its first Write — no explicit WriteHeader.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, err := io.WriteString(w, payload)
		writeErr <- err
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	require.NoError(t, <-writeErr, "the handler's write reports success either way — corruption would be silent")

	zr, err := gzip.NewReader(res.Body)
	require.NoError(t, err, "body must open as a gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err,
		"compressed stream must arrive whole, not truncated at the stale uncompressed Content-Length")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.Equal(t, payload, string(decoded),
		"body must decompress byte-identically to the handler output")
}

// An explicit WriteHeader is the other header-committing event (#175):
// net/http latches Content-Length at the final WriteHeader, so the wrapper
// must strip a stale uncompressed Content-Length there exactly as Write and
// FlushError do. Without the strip the stale length commits alongside
// Content-Encoding: gzip and the wire corrupts in one of two shapes, both
// ending in client unexpected EOF mid-stream: compression that EXPANDS the
// payload (the small body here) overruns the declared length — net/http cuts
// the stream mid-write, and for a body this size, buffered whole in flate,
// only the deferred-Close log ever hears of it (a LARGE incompressible body
// forces block emission mid-handler and hands the overrun back as
// ErrContentLength from the handler's own Write) — while compression that
// SHRINKS the payload (the compressible #134 file-serving shape) lands the
// complete stream UNDER the declared length and net/http closes the
// connection short of it, gz.Close succeeding: fully silent server-side.
// WriteHeader returns nothing, and in both of these flate-buffered shapes
// the handler's writes all return nil — so neither a quiet close log nor a
// large compressible probe makes the strip unnecessary. Latent until #134 mounts
// file-serving handlers (http.FileServer/ServeContent set Content-Length)
// behind this middleware.
func TestGzip_WriteHeaderStripsStaleContentLength(t *testing.T) {
	const payload = `{"roster":"live","unit":"7th Cavalry","status":"active"}`

	writeErr := make(chan error, 1) // handler runs on the server goroutine
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A length-aware handler (the http.ServeContent shape) sets the
		// UNCOMPRESSED length, then commits it with an explicit WriteHeader.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, err := io.WriteString(w, payload)
		writeErr <- err
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	require.NoError(t, <-writeErr, "the handler's write reports success either way — corruption would be silent")

	zr, err := gzip.NewReader(res.Body)
	require.NoError(t, err, "body must open as a gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err,
		"compressed stream must arrive whole, not truncated at the stale uncompressed Content-Length")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.Equal(t, payload, string(decoded),
		"body must decompress byte-identically to the handler output")
}

// The fourth header-committing event (#175): a handler that sets the stale
// uncompressed Content-Length and returns WITHOUT writing never passes
// through Write, WriteHeader, or FlushError — the middleware's deferred
// gz.Close() then commits the response with its own direct downstream writes
// (gzip header + empty-stream trailer, ~23 bytes). With the stale length
// latched at that commit, net/http closes the connection short of the
// declared length and the client hits unexpected EOF on a response the
// handler believes it never started. The middleware must strip the stale
// length before its own commit too: the client must receive a complete,
// decodable empty gzip stream.
func TestGzip_BodylessHandlerStripsStaleContentLength(t *testing.T) {
	h := rest.GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sets the length it intended to serve, then bails without a byte —
		// the not-modified/error-early shape of a length-aware handler.
		w.Header().Set("Content-Length", "57")
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))

	zr, err := gzip.NewReader(res.Body)
	require.NoError(t, err, "body must open as a gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err,
		"empty gzip stream must arrive whole, not cut short of the stale declared Content-Length")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.Empty(t, string(decoded), "the handler wrote nothing — the stream must decode to nothing")
}

// The other ResponseController verbs (deadline control here, as the witness)
// must tunnel through the gzip wrapper via Unwrap — they don't touch the
// compressed stream, so passing them straight down is safe. Flush is the one
// verb that must NOT take that route; gzipResponseWriter's explicit
// FlushError (rest/gzip.go) wins the controller's method search, so Unwrap
// never opens the corruption path.
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

	// The watchdog only arms once Do returns, and the deferred release —
	// registered above, before Do — can only fire once Do returns. A mutant
	// whose flush delivers nothing before the headers would leave Do blocked
	// forever; the request deadline turns that hang into a crisp failure,
	// which in turn lets the deferred release unblock the handler.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
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
	tail, err := io.ReadAll(zr) // EOF verifies the gzip CRC/size trailer
	require.NoError(t, err, "tail must decode through an intact trailer")
	require.NoError(t, <-handlerErr, "post-flush write must succeed")
	assert.Equal(t, part1+part2, prefix+string(tail),
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
