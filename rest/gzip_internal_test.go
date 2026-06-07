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
	"net/http"
	"net/http/httptest"
	"testing"

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
// bare Unwrap would institutionalize) leaves the snapshot empty; one that
// reorders leaves the sync block out of the snapshot. Both fail the decode.
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
// instead of swallowing it and pretending the bytes reached the wire.
func TestGzipResponseWriter_FlushReportsUnsupportedChain(t *testing.T) {
	flushErr := make(chan error, 1)
	h := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr <- http.NewResponseController(w).Flush()
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(&noFlushUnderlying{header: make(http.Header)}, req)

	require.ErrorIs(t, <-flushErr, http.ErrNotSupported,
		"an unflushable chain below gzip must surface, not vanish")
}
