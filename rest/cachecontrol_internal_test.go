package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Direct pins on cacheControlWriter's commit decision: the battery rides the
// implied-200 path only (writeJSON never calls WriteHeader(200)), so the
// explicit-WriteHeader branch needs its own witness — without one, neutering
// the header-set inside the code==200 branch leaves the whole suite green.
//
// Stamp-PRESENCE assertions read rr.Result().Header — the recorder snapshots
// at WriteHeader (a flush implies one), so a stamp set after delegating
// would be wire-invisible and must fail here. Stamp-ABSENCE assertions read
// the live rr.Header() map, the stronger check there: not even a
// post-snapshot late stamp is tolerated.
func TestCacheControlWriter_CommitDecision(t *testing.T) {
	wrap := func() (*cacheControlWriter, *httptest.ResponseRecorder) {
		rr := httptest.NewRecorder()
		return &cacheControlWriter{ResponseWriter: rr, value: "max-age=600"}, rr
	}

	t.Run("explicit WriteHeader 200 stamps", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusOK)
		assert.Equal(t, "max-age=600", rr.Result().Header.Get("Cache-Control"))
	})

	t.Run("WriteHeader 204 does not stamp", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusNoContent)
		assert.Empty(t, rr.Header().Get("Cache-Control"))
	})

	t.Run("WriteHeader 500 does not stamp", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusInternalServerError)
		assert.Empty(t, rr.Header().Get("Cache-Control"))
	})

	// A non-latching 1xx WriteHeader (the predicate carves out 101) latches
	// nothing (#165) — and in particular
	// must not stamp: a stamp on the 1xx would sit in the live map and leak
	// onto whatever final status follows, here a 500 (the leak class the type
	// doc forbids). The stamp-on-the-real-200 direction needs real 1xx wire
	// machinery the recorder lacks — informational_internal_test.go's shared
	// table covers it on a real server.
	t.Run("WriteHeader 103 leaves the decision to the final status", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusInternalServerError)
		assert.Empty(t, rr.Header().Get("Cache-Control"),
			"a 500 after a forwarded 103 must not carry the freshness signal")
	})

	t.Run("second WriteHeader cannot change the first decision", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusInternalServerError)
		w.WriteHeader(http.StatusOK) // superfluous — the 500 already committed
		assert.Empty(t, rr.Header().Get("Cache-Control"))
	})

	t.Run("implied 200 via Write stamps", func(t *testing.T) {
		w, rr := wrap()
		_, err := w.Write([]byte("{}"))
		require.NoError(t, err)
		assert.Equal(t, "max-age=600", rr.Result().Header.Get("Cache-Control"))
	})

	// The tunnel blind spot (same shape commitWriter closes in sentry.go): a
	// ResponseController flush before the first write commits the implied 200
	// on the wire, so the header must be stamped by then — without FlushError
	// the flush reaches the recorder via Unwrap and the later Write stamps a
	// dead map.
	t.Run("FlushError before first write stamps", func(t *testing.T) {
		w, rr := wrap()
		require.NoError(t, http.NewResponseController(w).Flush())
		assert.Equal(t, "max-age=600", rr.Result().Header.Get("Cache-Control"),
			"a flush commits the implied 200 — the freshness signal must already be on it")
	})

	// committed==true guard on FlushError: a flush after an explicit non-200
	// must not stamp — the 500 already decided. Asserted on the LIVE map (not
	// the Result snapshot): a guard mutant stamps after the recorder's
	// WriteHeader snapshot, so only the live map can see it.
	t.Run("FlushError after WriteHeader 500 does not stamp", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusInternalServerError)
		require.NoError(t, http.NewResponseController(w).Flush())
		assert.Empty(t, rr.Header().Get("Cache-Control"))
	})

	// Regression pin (#163 R1): on an unflushable chain — a plain
	// ResponseWriter with no FlushError/Flusher/Unwrap, the shape
	// gzipResponseWriter had before #167 — the delegated flush ALWAYS returns
	// http.ErrNotSupported: nothing reached the wire. The stamp and
	// committed=true must roll back, or a handler reacting to the failed
	// flush by writing an error commits a non-200 carrying max-age (the leak
	// class the wrapper's own doc forbids).
	t.Run("failed flush rolls back stamp before error response", func(t *testing.T) {
		rr := httptest.NewRecorder()
		w := &cacheControlWriter{ResponseWriter: &noFlushWriter{rr: rr}, value: "max-age=600"}

		err := http.NewResponseController(w).Flush()
		require.ErrorIs(t, err, http.ErrNotSupported,
			"an unflushable writer supports no flush — nothing was sent")

		w.WriteHeader(http.StatusNotFound)
		assert.Empty(t, rr.Result().Header.Get("Cache-Control"),
			"a 404 after a failed flush must not carry the freshness signal")
	})
}

// noFlushWriter hides the recorder's Flusher — the shape gzipResponseWriter
// had before #167 (no FlushError, no Flusher, no Unwrap), where a delegated
// flush always fails with http.ErrNotSupported.
type noFlushWriter struct {
	rr *httptest.ResponseRecorder
}

func (w *noFlushWriter) Header() http.Header         { return w.rr.Header() }
func (w *noFlushWriter) Write(b []byte) (int, error) { return w.rr.Write(b) }
func (w *noFlushWriter) WriteHeader(code int)        { w.rr.WriteHeader(code) }

// cacheControl values are registration-time constants; a negative max-age is
// always a programming error, so it fails at registration, not on the wire.
func TestCacheControl_NegativeMaxAgePanics(t *testing.T) {
	assert.Panics(t, func() {
		cacheControl(-5, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	})
}
