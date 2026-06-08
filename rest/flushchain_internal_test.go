package rest

// Composed flush-chain integrity (#174): #167's "flush succeeds from a
// handler" held only by composition of per-layer pins (sentry, metrics,
// cachecontrol, gzip each pinned individually) — nothing pinned the COMPOSED
// property, so a wrapper inserted into New tomorrow without
// FlushError/Flusher/Unwrap would break handler flushes in production with
// every existing test green. This test assembles the stack through chain()
// (rest.go) — the SAME function New composes its middleware with, so the
// assembly under test is production's by construction, not a hand-kept
// mirror that could drift (#174 review) — with routes() swapped for a probe
// mux whose route registers through handle() exactly like a production route
// (routeLabel + requireScope + cacheControl). Over a real server it pins the
// composed behavior: the mid-body flush succeeds, and the flushed prefix is
// decodable on the wire while the handler still holds the tail. Internal
// (package rest) because chain and the wrappers it mounts are unexported.

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlushChain_NewShapedStackStreamsFlushedPrefix(t *testing.T) {
	// Sentry ENABLED: without a client sentryMiddleware never mounts
	// commitWriter, and the composed property would silently exclude the
	// outermost wrapper. The premise that an enabled client mounts the
	// wrapper UNCONDITIONALLY is itself pinned —
	// TestSentryMiddleware_MountsCommitWriterWhenEnabled (sentry_test.go).
	enableSentry(t)

	const part1 = "first chunk, flushed mid-stream"
	const part2 = "; tail held by the handler until the prefix decoded"

	flushErr := make(chan error, 1) // handler runs on the server goroutine
	release := make(chan struct{})  // client → handler: prefix decoded, finish
	releaseOnce := sync.OnceFunc(func() { close(release) })
	writeErr := make(chan error, 2)
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.WriteString(w, part1)
		writeErr <- err
		flushErr <- http.NewResponseController(w).Flush()
		<-release
		_, err = io.WriteString(w, part2)
		writeErr <- err
	})

	// The production assembly itself: chain() is what New composes the
	// middleware with (rest.go), here with routes() swapped for the probe
	// mux. handle() gives the probe the full per-route wrap set (routeLabel —
	// the metrics sweep's never-routed contract holds for this traffic too —
	// plus requireScope and cacheControl), so the flush traverses every
	// writer wrapper a production route's would: commitWriter → statusWriter
	// → gzipResponseWriter → cacheControlWriter.
	mux := http.NewServeMux()
	handle(mux, "GET /", "read", maxAgeRosterFamily, probe)
	h := chain(&sentryFakeDatastore{}, mux)

	srv := httptest.NewServer(h)
	defer srv.Close()
	// LIFO: unblock the handler before srv.Close waits on it, so a failure
	// before the deliberate release fails the test instead of hanging it.
	defer releaseOnce()

	// Watchdog: a chain that buffers the flush instead of streaming it would
	// leave the client blocked forever; the deadline turns that into a crisp
	// failure (same pattern as the per-layer streaming pin in gzip_test.go).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer cav7_sentry_read")
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"),
		"the request negotiated gzip — the full wrapper chain must be in play")
	assert.Equal(t, "max-age=600", res.Header.Get("Cache-Control"),
		"the first write committed the implied 200 — cacheControlWriter stamped it, proof the innermost wrapper was in the chain")

	require.NoError(t, <-writeErr, "pre-flush write must succeed")
	require.NoError(t, <-flushErr,
		"ResponseController.Flush must succeed through the composed New-shaped chain")

	// Decode the flushed prefix while the handler still blocks on release —
	// possible only if every layer pushed its part of the flush to the wire.
	zr, prefix := decodeStackPrefixWithin(t, res.Body, len(part1), 5*time.Second)
	assert.Equal(t, part1, prefix,
		"flushed prefix must decompress to exactly the pre-flush writes")

	releaseOnce()
	tail, err := io.ReadAll(zr) // EOF verifies the gzip CRC/size trailer
	require.NoError(t, err, "tail must decode through an intact trailer")
	require.NoError(t, <-writeErr, "post-flush write must succeed")
	assert.Equal(t, part1+part2, prefix+string(tail),
		"full body must decompress byte-identically to the handler output")
}

// decodeStackPrefixWithin opens a gzip reader over body and decodes exactly n
// bytes, failing the test if that takes longer than timeout — the deadlock
// shape a buffered (non-streamed) flush produces. Returns the open reader so
// the caller can decode the rest of the stream. (The package-external twin
// lives in gzip_test.go; this internal test cannot reach it.)
func decodeStackPrefixWithin(t *testing.T, body io.Reader, n int, timeout time.Duration) (*gzip.Reader, string) {
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
		t.Fatal("flushed prefix never became decodable — some layer buffered the flush instead of streaming it to the wire")
		return nil, ""
	}
}
