package rest

// Direct pins on gzipResponseWriter's FlushError ordering (#167), observed
// against a recording fake writer: the e2e streaming test (gzip_test.go) can
// only turn a wrong flush order into a watchdog timeout, while the snapshot
// taken here at the moment the underlying Flush fires fails crisply. The
// snapshot is wire-faithful — bytes the underlying writer had received when
// the flush reached it, not the buffer's state after the handler finished.

import (
	"bytes"
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flushSnapshotWriter records what the gzip layer pushed down to it, and
// snapshots that buffer the moment Flush is called — the bytes that would be
// on the wire after the flush.
type flushSnapshotWriter struct {
	header   http.Header
	buf      bytes.Buffer
	flushed  int
	snapshot []byte // buf contents at the first Flush
}

func (w *flushSnapshotWriter) Header() http.Header         { return w.header }
func (w *flushSnapshotWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *flushSnapshotWriter) WriteHeader(int)             {}
func (w *flushSnapshotWriter) Flush() {
	if w.flushed == 0 {
		w.snapshot = append([]byte(nil), w.buf.Bytes()...)
	}
	w.flushed++
}

// FlushError must flush the gzip.Writer's buffered output into the underlying
// writer BEFORE flushing the underlying chain — in that order the bytes on
// the wire at flush time are a valid gzip stream prefix decoding to exactly
// the pre-flush writes. A mutant that skips the gzip flush (the corruption a
// bare Unwrap would institutionalize) leaves the snapshot holding only the
// 10-byte gzip header from the handler's first Write — the decode fails at
// io.ReadFull, not gzip.NewReader; one that reorders leaves the sync block
// out of the snapshot. Both fail the decode.
func TestGzipResponseWriter_FlushOrdersGzipBeforeUnderlying(t *testing.T) {
	const part1 = "written before the flush"

	fake := &flushSnapshotWriter{header: make(http.Header)}
	flushErr := make(chan error, 1)
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.WriteString(w, part1)
		require.NoError(t, err)
		flushErr <- http.NewResponseController(w).Flush()
		_, err = io.WriteString(w, "after the flush, never flushed explicitly")
		require.NoError(t, err)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(fake, req)

	require.NoError(t, <-flushErr)
	require.Equal(t, 1, fake.flushed, "exactly one delegated flush must reach the underlying writer")

	zr, err := gzip.NewReader(bytes.NewReader(fake.snapshot))
	require.NoError(t, err, "snapshot at flush time must open as a gzip stream")
	prefix := make([]byte, len(part1))
	_, err = io.ReadFull(zr, prefix)
	require.NoError(t, err, "snapshot must contain the complete sync block for the pre-flush writes")
	assert.Equal(t, part1, string(prefix))
}

// A deliberate connection takeover must not poison the close log (#175):
// after a hijack (reachable through the wrapper's Unwrap), the middleware's
// deferred gz.Close() inevitably fails with http.ErrHijacked — its trailer
// writes land on a connection the handler now owns — but nothing was
// truncated: the client got exactly the bytes the hijacker wrote. Logging
// "response likely truncated" there would cry wolf on every hijack, and that
// log line is the ONLY server-side signal of the real corruption class (the
// stale-Content-Length truncations are silent everywhere else), so it has to
// stay trustworthy. Internal (package rest) for captureErrorLog; a real
// server because only a real connection can be hijacked.
func TestGzipMiddleware_HijackKeepsCloseLogQuiet(t *testing.T) {
	logged := captureErrorLog(t)

	hijackErr := make(chan error, 1) // handler runs on the server goroutine
	inner := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, bufrw, err := http.NewResponseController(w).Hijack()
		hijackErr <- err
		if err != nil {
			return
		}
		// The takeover speaks raw HTTP itself — the gzip layer is out of
		// the loop from here.
		_, _ = bufrw.WriteString("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
		_ = bufrw.Flush()
		_ = conn.Close()
	}))
	// On a hijacked connection neither the client response (the hijacker
	// wrote it directly) nor srv.Close (httptest forgets hijacked conns)
	// orders the middleware's deferred gz.Close before the assertions — only
	// the middleware's own return does. closed is that barrier.
	closed := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r) // the deferred gz.Close runs before this returns
		close(closed)
	})

	srv := httptest.NewUnstartedServer(h)
	// gz.Close's doomed trailer writes make the stdlib log "response.Write on
	// hijacked connection" — expected here, keep it out of test output.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	require.NoError(t, <-hijackErr, "the hijack must reach the connection through the gzip wrapper")
	require.Equal(t, http.StatusNoContent, res.StatusCode, "the client must see the hijacker's raw response")
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware never returned after the hijacked request")
	}
	assert.NotContains(t, logged.String(), "gzip close failed",
		"a deliberate hijack is not a truncation — the close log must stay trustworthy")
}

// noFlushUnderlying is a writer the controller cannot flush — no FlushError,
// no Flusher, no Unwrap.
type noFlushUnderlying struct {
	header http.Header
	buf    bytes.Buffer
}

func (w *noFlushUnderlying) Header() http.Header         { return w.header }
func (w *noFlushUnderlying) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *noFlushUnderlying) WriteHeader(int)             {}

// When nothing below the gzip layer can flush, the handler must still hear
// about it loudly: FlushError reports the chain's http.ErrNotSupported
// instead of swallowing it and pretending the bytes reached the wire. The
// failure is NOT a no-op, though — the gzip header and sync block reach the
// underlying writer before the delegated flush can fail, and this pins that
// half-state (it is the fact cacheControlWriter's rollback comment scopes
// itself around). The underlying buffer is snapshotted INSIDE the handler,
// the moment the flush returns: once ServeHTTP returns, the middleware's
// deferred gz.Close() writes the gzip header and trailer into the buffer
// regardless of what FlushError did, so any post-return assertion on the
// buffer is vacuous — a probe-first FlushError that pushes nothing before
// failing would pass it.
func TestGzipResponseWriter_FlushReportsUnsupportedChain(t *testing.T) {
	out := &noFlushUnderlying{header: make(http.Header)}
	flushErr := make(chan error, 1)
	atFlush := make(chan []byte, 1) // out.buf contents the moment FlushError returns
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := http.NewResponseController(w).Flush()
		atFlush <- append([]byte(nil), out.buf.Bytes()...)
		flushErr <- err
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(out, req)

	require.ErrorIs(t, <-flushErr, http.ErrNotSupported,
		"an unflushable chain below gzip must surface, not vanish")
	downstream := <-atFlush
	require.GreaterOrEqual(t, len(downstream), 15,
		"the failed flush is not a no-op: the 10-byte gzip header and 5-byte sync block must already be downstream when FlushError returns")
	assert.Equal(t, []byte{0x1f, 0x8b}, downstream[:2],
		"bytes downstream at flush-failure time must start with the gzip magic")
}
