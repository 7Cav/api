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
func TestCacheControlWriter_CommitDecision(t *testing.T) {
	wrap := func() (*cacheControlWriter, *httptest.ResponseRecorder) {
		rr := httptest.NewRecorder()
		return &cacheControlWriter{ResponseWriter: rr, value: "max-age=600"}, rr
	}

	t.Run("explicit WriteHeader 200 stamps", func(t *testing.T) {
		w, rr := wrap()
		w.WriteHeader(http.StatusOK)
		assert.Equal(t, "max-age=600", rr.Header().Get("Cache-Control"))
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
		assert.Equal(t, "max-age=600", rr.Header().Get("Cache-Control"))
	})

	// The tunnel blind spot (same shape commitWriter closes in sentry.go): a
	// ResponseController flush before the first write commits the implied 200
	// on the wire, so the header must be stamped by then — without FlushError
	// the flush reaches the recorder via Unwrap and the later Write stamps a
	// dead map.
	t.Run("FlushError before first write stamps", func(t *testing.T) {
		w, rr := wrap()
		require.NoError(t, http.NewResponseController(w).Flush())
		assert.Equal(t, "max-age=600", rr.Header().Get("Cache-Control"),
			"a flush commits the implied 200 — the freshness signal must already be on it")
	})
}

// cacheControl values are registration-time constants; a negative max-age is
// always a programming error, so it fails at registration, not on the wire.
func TestCacheControl_NegativeMaxAgePanics(t *testing.T) {
	assert.Panics(t, func() {
		cacheControl(-5, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	})
}
