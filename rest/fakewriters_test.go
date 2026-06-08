package rest

// The delegate fake family (#174): every error-path pin on the chain's
// writer wrappers injects its downstream failure through one of these fakes,
// each modelling ONE base-writer shape the real wire can take. They live
// together so the next pin (e.g. #178's rollback witnesses) reuses a shape
// instead of minting a near-duplicate:
//
//   - noFlushWriter: the controller's REFUSAL shape — no FlushError, no
//     Flusher, no Unwrap, so a delegated flush returns http.ErrNotSupported
//     and nothing reaches the wire (the rollback discriminator's "nothing
//     sent" world, #164/#163 R1).
//   - flushErrorWriter: the GENUINE-failure shape — FlushError returns the
//     injected error (a conn write error's analogue), so the commit really
//     happened before the error surfaced (the "keep the latch/stamp" world,
//     #164/#172).
//   - failingWriter: the broken-pipe shape — every body Write fails with the
//     injected error, the failure that makes gzip's OWN writer (gz.Flush,
//     gz.Close) fail; its FlushError succeeds and counts, so a pin can
//     observe whether a wrapper's flush path stopped before delegating.
//   - flushSnapshotWriter: the wire-faithful observer — records what the
//     wrapper pushed down and snapshots the buffer the moment Flush arrives
//     (the bytes that would be on the wire after the flush).
//   - noFlushUnderlying: noFlushWriter with its own buffer instead of a
//     recorder, for pins that must inspect the bytes downstream of a refusal.
//
// All package-internal: the wrappers under test are unexported.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
)

// noFlushWriter hides the recorder's Flusher — the shape gzipResponseWriter
// had before #167 (no FlushError, no Flusher, no Unwrap), where a delegated
// flush always fails with http.ErrNotSupported.
type noFlushWriter struct {
	rr *httptest.ResponseRecorder
}

func (w *noFlushWriter) Header() http.Header         { return w.rr.Header() }
func (w *noFlushWriter) Write(b []byte) (int, error) { return w.rr.Write(b) }
func (w *noFlushWriter) WriteHeader(code int)        { w.rr.WriteHeader(code) }

// flushErrorWriter is a base writer whose flush genuinely FAILS rather than
// being refused: FlushError returns the injected error, never
// http.ErrNotSupported. The real-server analogue is a conn write error — the
// implied 200 commits to the wire BEFORE the error returns to the handler.
type flushErrorWriter struct {
	rr  *httptest.ResponseRecorder
	err error
}

func (w *flushErrorWriter) Header() http.Header         { return w.rr.Header() }
func (w *flushErrorWriter) Write(b []byte) (int, error) { return w.rr.Write(b) }
func (w *flushErrorWriter) WriteHeader(code int)        { w.rr.WriteHeader(code) }
func (w *flushErrorWriter) FlushError() error           { return w.err }

// failingWriter rejects every body write with a fixed genuine error — the
// downstream-failure shape (connection torn down, write timeout) that makes
// the gzip layer's own writes fail: gz.Flush and gz.Close surface it, and it
// sticks in the gzip.Writer. Its FlushError SUCCEEDS and counts calls, so a
// pin can prove a wrapper's flush path returned early — when the failure
// came from the wrapper's own downstream write, a delegated flush must never
// run (flushes stays 0).
type failingWriter struct {
	header  http.Header
	err     error
	flushes int // delegated flushes that reached this base
}

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) Write([]byte) (int, error) { return 0, w.err }
func (w *failingWriter) WriteHeader(int)           {}
func (w *failingWriter) FlushError() error         { w.flushes++; return nil }

// flushSnapshotWriter records what the wrapper under test pushed down to it,
// and snapshots that buffer the moment Flush is called — the bytes that would
// be on the wire after the flush.
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

// noFlushUnderlying is a writer the controller cannot flush — no FlushError,
// no Flusher, no Unwrap — with its own buffer, for pins that must inspect
// what landed downstream of the refusal.
type noFlushUnderlying struct {
	header http.Header
	buf    bytes.Buffer
}

func (w *noFlushUnderlying) Header() http.Header         { return w.header }
func (w *noFlushUnderlying) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *noFlushUnderlying) WriteHeader(int)             {}
