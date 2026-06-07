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
	"errors"
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

// A deliberate connection takeover with NOTHING through the gzip layer first
// must not poison the close log (#175): after a hijack (reachable through the
// wrapper's Unwrap), the middleware's deferred gz.Close() inevitably fails
// with http.ErrHijacked — its header/trailer writes land on a connection the
// handler now owns — and because no gzip stream ever started, nothing was
// truncated: the client got exactly the bytes the hijacker wrote. Logging
// "response likely truncated" there would cry wolf on every clean hijack, and
// that log line is the ONLY server-side signal of the real corruption class
// (the stale-Content-Length truncations are silent everywhere else), so it
// has to stay trustworthy. That premise holds ONLY for the no-output case:
// when handler output preceded the hijack the client DID lose bytes — the
// two loud siblings below pin that side. Internal (package rest) for
// captureErrorLog; a real server because only a real connection can be
// hijacked.
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
	// gz.Close's doomed header/trailer writes make the stdlib log
	// "response.Write on hijacked connection" — expected here, keep it out of
	// test output.
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
		"a no-output hijack is not a truncation — the close log must stay trustworthy")
	assert.NotContains(t, logged.String(), "gzip stream abandoned",
		"no gzip stream ever started — the abandoned-stream message is for hijacks AFTER output")
}

// The carve-out's flip side (#175): a hijack AFTER handler output went
// through the gzip layer is NOT a clean takeover — the handler's bytes sit in
// the flate buffer (only the 10-byte gzip header went downstream), so the
// client got Content-Encoding: gzip with a stream that never decodes to the
// payload. gz.Close() still fails with ErrHijacked, but suppressing the log
// here would silence REAL corruption; the middleware must log the distinct
// abandoned-stream message instead. Same barrier pattern as the quiet test
// above — only the middleware's own return orders the deferred gz.Close.
func TestGzipMiddleware_HijackAfterWriteLogsAbandonedStream(t *testing.T) {
	logged := captureErrorLog(t)

	const payload = "written through the gzip wrapper and owed to the client"
	writeErr := make(chan error, 1) // handler runs on the server goroutine
	hijackErr := make(chan error, 1)
	inner := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.WriteString(w, payload) // gzip header goes downstream; payload buffers in flate
		writeErr <- err
		conn, _, err := http.NewResponseController(w).Hijack()
		hijackErr <- err
		if err != nil {
			return
		}
		// The takeover dies without writing — the crash/bug shape. The
		// handler's payload is gone with the flate buffer.
		_ = conn.Close()
	}))
	closed := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r) // the deferred gz.Close runs before this returns
		close(closed)
	})

	srv := httptest.NewUnstartedServer(h)
	// gz.Close's doomed flate-flush writes make the stdlib log "response.Write
	// on hijacked connection" — expected here, keep it out of test output.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	raw, readErr := io.ReadAll(res.Body)
	require.NoError(t, res.Body.Close())

	require.NoError(t, <-writeErr, "the write through the wrapper reports success — corruption is invisible to the handler")
	require.NoError(t, <-hijackErr, "the hijack must reach the connection through the gzip wrapper")
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware never returned after the hijacked request")
	}

	// The client-visible truth: Content-Encoding: gzip was committed, but the
	// payload never arrives decodable — the read or the decode dies first.
	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	if readErr == nil {
		zr, zerr := gzip.NewReader(bytes.NewReader(raw))
		if zerr == nil {
			decoded, derr := io.ReadAll(zr)
			require.True(t, derr != nil || string(decoded) != payload,
				"scenario invalid: the payload survived the hijack intact — nothing was abandoned")
		}
	}

	assert.Contains(t, logged.String(), "gzip stream abandoned by hijack after output",
		"output went through the gzip layer before the hijack — the truncation must be named, not carved out")
}

// The flush sibling of the test above (#175): a flush is the OTHER way a gzip
// stream starts — FlushError pushes the gzip header and a sync block to the
// wire before any Write — so a hijack after it abandons a stream the client
// already began decoding: valid gzip prefix, then unexpected EOF mid-stream.
// The deferred gz.Close()'s ErrHijacked must log the abandoned-stream message
// here exactly as it does for the buffered-write shape; a latch that only
// Write sets would stay quiet.
func TestGzipMiddleware_HijackAfterFlushLogsAbandonedStream(t *testing.T) {
	logged := captureErrorLog(t)

	flushErr := make(chan error, 1) // handler runs on the server goroutine
	hijackErr := make(chan error, 1)
	inner := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		// Flush before any write: the gzip header + empty sync block reach the
		// wire — the stream is started and a trailer is now owed.
		flushErr <- rc.Flush()
		conn, _, err := rc.Hijack()
		hijackErr <- err
		if err != nil {
			return
		}
		_ = conn.Close() // takeover dies without terminating the stream
	}))
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
	raw, _ := io.ReadAll(res.Body) // the conn dies mid-body — the read error is incidental
	require.NoError(t, res.Body.Close())

	require.NoError(t, <-flushErr, "the pre-hijack flush reports success — the stream is started on the wire")
	require.NoError(t, <-hijackErr, "the hijack must reach the connection through the gzip wrapper")
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware never returned after the hijacked request")
	}

	// The client-visible truth: the flushed bytes open as a valid gzip stream
	// (header + sync block reached the wire before the hijack), but the stream
	// was never terminated — the decode dies short of a trailer.
	require.Equal(t, "gzip", res.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err, "the flushed prefix must open as a gzip stream — it reached the wire before the hijack")
	_, err = io.ReadAll(zr)
	require.Error(t, err, "scenario invalid: the stream decoded through an intact trailer — nothing was abandoned")

	assert.Contains(t, logged.String(), "gzip stream abandoned by hijack after output",
		"a flush starts the gzip stream just as a write does — the hijack abandons it, and the log must say so")
}

// A committed bodyless status must not detonate the close log (#175, folded
// in by maintainer ruling 2026-06-07; pre-existing on develop, detonates at
// #134): a handler that commits WriteHeader(204) behind gzip writes nothing
// through the gzip layer, so the deferred gz.Close()'s empty-stream
// header+trailer writes would hit net/http's bodyAllowedForStatus==false and
// fail with ErrBodyNotAllowed — making the close log cry "response likely
// truncated" on every such response when nothing was ever owed: no gzip
// stream started, exactly the no-output hijack reasoning. The middleware must
// skip the doomed close writes entirely, and the eagerly-set
// Content-Encoding: gzip must come off the 204 — there is no representation
// at all, so advertising an encoding is a lie (stdlib precedent: net/http's
// writeNotModified deletes Content-Encoding for the same reason). Real server
// + barrier (same pattern as the hijack tests) because the wire shape — no
// Content-Encoding, no Content-Length, empty body — is the behavior.
func TestGzipMiddleware_NoContentKeepsCloseLogQuiet(t *testing.T) {
	logged := captureErrorLog(t)

	inner := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The length-aware shape: a stale Content-Length set before the
		// handler decides there is nothing to send.
		w.Header().Set("Content-Length", "57")
		w.WriteHeader(http.StatusNoContent)
	}))
	// The deferred gz.Close (or its skip) runs before inner.ServeHTTP returns;
	// closed orders it before the log assertion.
	closed := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		close(closed)
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware never returned")
	}

	require.Equal(t, http.StatusNoContent, res.StatusCode)
	assert.Empty(t, res.Header.Get("Content-Encoding"),
		"a 204 carries no representation — advertising gzip would be a lie")
	assert.Empty(t, res.Header.Get("Content-Length"),
		"the stale uncompressed Content-Length must not survive onto the 204")
	assert.Empty(t, body, "a 204 has no body — not even an empty gzip stream")
	assert.NotContains(t, logged.String(), "gzip close failed",
		"nothing went through the gzip layer and no body is allowed — there is nothing to truncate, so the close log must stay quiet")
}

// The 304 sibling of the 204 test above — THE shape #134 detonates: ServeContent
// mints a 304 per conditional-GET cache hit, and on develop each one made the
// deferred gz.Close fail with ErrBodyNotAllowed and the close log cry
// "response likely truncated" falsely. The bodyless reasoning is identical
// (no gzip stream started, none owed), with one extra wire stake: a 304
// revalidates a representation the CLIENT already holds, so the eagerly-set
// Content-Encoding: gzip would misdescribe that stored representation —
// net/http's own writeNotModified deletes Content-Encoding for the same
// reason (the validators, ETag here, are what a 304 is for).
func TestGzipMiddleware_NotModifiedKeepsCloseLogQuiet(t *testing.T) {
	logged := captureErrorLog(t)

	inner := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The conditional-GET hit shape: validator + the length the full
		// response would have carried, then a bare 304.
		w.Header().Set("ETag", `"roster-v7"`)
		w.Header().Set("Content-Length", "57")
		w.WriteHeader(http.StatusNotModified)
	}))
	closed := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		close(closed)
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware never returned")
	}

	require.Equal(t, http.StatusNotModified, res.StatusCode)
	assert.Equal(t, `"roster-v7"`, res.Header.Get("ETag"),
		"the validator is what the 304 exists to carry")
	assert.Empty(t, res.Header.Get("Content-Encoding"),
		"a 304 describes the representation the client already holds — gzip was never applied to it")
	assert.Empty(t, res.Header.Get("Content-Length"),
		"the stale uncompressed Content-Length must not survive onto the 304")
	assert.Empty(t, body, "a 304 has no body — not even an empty gzip stream")
	assert.NotContains(t, logged.String(), "gzip close failed",
		"every conditional-GET hit would cry wolf otherwise (#134) — the close log must stay trustworthy")
}

// HEAD with a length-aware bodyless handler (#175 folded-in scope; the
// http.ServeContent HEAD shape at #134: set the headers, skip the body): the
// wrapper rightly strips the handler's UNCOMPRESSED Content-Length — the GET
// twin serves a gzip body, so that length is a lie — but without the skip the
// deferred gz.Close()'s empty-stream writes (23 bytes) were counted by
// net/http's HEAD bookkeeping and minted into Content-Length: 23, a length no
// GET would ever produce. The deliberate choice pinned here: skip the close
// writes, so net/http sets NO Content-Length at all (its HEAD finalization
// only computes one when the handler wrote bytes) — an honest HEAD makes no
// length claim it cannot back. Content-Encoding: gzip STAYS: HEAD must mirror
// the GET twin's headers, and the GET twin (whether it writes a body through
// gz or falls to the middleware's empty-stream commit) serves gzip.
func TestGzipMiddleware_HeadBodylessMakesNoLengthClaim(t *testing.T) {
	logged := captureErrorLog(t)

	inner := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Length-aware handler on a HEAD: declares the (uncompressed) length
		// it would serve a GET, writes nothing.
		w.Header().Set("Content-Length", "57")
	}))
	closed := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r)
		close(closed)
	})

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodHead, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("middleware never returned")
	}

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "gzip", res.Header.Get("Content-Encoding"),
		"HEAD mirrors the GET twin, and the GET twin serves gzip")
	assert.Empty(t, res.Header.Get("Content-Length"),
		"neither the stale uncompressed length nor the minted 23-byte empty-stream length — an honest HEAD makes no length claim here")
	assert.NotContains(t, logged.String(), "gzip close failed",
		"nothing went through the gzip layer — there is nothing to truncate")
}

// failingWriter rejects every body write with a fixed genuine error — the
// downstream-failure shape (connection torn down, write timeout) that makes
// the deferred gz.Close fail for real.
type failingWriter struct {
	header http.Header
	err    error
}

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) Write([]byte) (int, error) { return 0, w.err }
func (w *failingWriter) WriteHeader(int)           {}

// The loud side of the carve-out (#175): a GENUINE close failure — anything
// but the hijack/bodyless shapes — must still log "gzip close failed".
// Nothing else pins this: a carve-out widened to swallow every close error
// would pass the rest of the suite (every other test either expects silence
// or never fails the close), so this is the test that keeps the close log
// alive at all. Deterministic fake: the delegate rejects the gzip layer's
// downstream writes, so gz.Close surfaces the genuine error to the deferred
// log. Same-goroutine ServeHTTP — no server, no races.
func TestGzipMiddleware_GenuineCloseFailureLogs(t *testing.T) {
	logged := captureErrorLog(t)

	errDownstream := errors.New("downstream write torn away")
	out := &failingWriter{header: make(http.Header), err: errDownstream}
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The write fails downstream (gzip's lazy header hits the broken
		// delegate) and the error sticks in the gzip.Writer — gz.Close then
		// returns it. The handler seeing the error changes nothing about the
		// middleware's duty to log: most handlers ignore write errors.
		_, _ = io.WriteString(w, "payload the client will never get")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(out, req)

	assert.Contains(t, logged.String(), "gzip close failed",
		"a genuine close failure must stay loud — the carve-outs are for hijack/bodyless shapes only")
	assert.Contains(t, logged.String(), errDownstream.Error(),
		"the log must carry the underlying error for diagnosis")
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
