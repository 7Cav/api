package gateway

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAuthDatastore embeds the Datastore interface so it satisfies the type
// without implementing every method; only ValidateApiKey is exercised by the
// auth layer (rest.AuthMiddleware — moved there at #125, tested there). Any
// other call panics (nil method) — a loud failure if a test accidentally
// reaches further into the datastore.
type fakeAuthDatastore struct {
	datastores.Datastore
	validateApiKey func(string) (*datastores.ApiKeyResult, error)
}

func (f *fakeAuthDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	return f.validateApiKey(rawKey)
}

// TestBuildAPIHandler_ResponseCacheRemoved pins the Phase 2 de-cache
// (#123/#124): the response cache is gone — middleware out of the /api chain
// at #123, the cache package and Redis deleted outright at #124. Observable
// contract, driven through the production chain constructor on a formerly
// cacheable (non-tickets) GET route:
//
//   - every request reaches the inner handler — nothing is served from a
//     cache;
//   - the X-Cache header is gone (the PRD's enumerated break for this slice);
//   - the response body passes through unbuffered and intact.
func TestBuildAPIHandler_ResponseCacheRemoved(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return &datastores.ApiKeyResult{KeyId: 42}, nil
	}}

	innerCalls := 0
	h := buildAPIHandler(ds, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"roster":"live"}`))
	}))

	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/roster/cbt", nil)
		req.Header.Set("Authorization", "Bearer cav7_goodkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		assert.Empty(t, rr.Header().Values("X-Cache"),
			"X-Cache must be gone — the enumerated Phase 2 break (#123)")
		assert.Equal(t, `{"roster":"live"}`, rr.Body.String(),
			"response must pass through the chain untouched")
		assert.Equal(t, i, innerCalls,
			"every request must reach the inner handler — no response cache in the chain")
	}
}

// TestBuildAPIHandler_GzipRoundTrip pins the compression layer, live on every
// /api route since the Phase 2 de-cache (#123) — before that, the cache
// middleware stripped Accept-Encoding and gzipped responses itself, so on
// cacheable routes this path never ran in production (only the tickets
// bypass reached it). The assertion is a real round-trip — the body
// must decompress back to the original payload through an intact gzip trailer
// — because the failure mode this guards (gzipResponseWriter.Write dropping
// the stale uncompressed Content-Length too late or not at all) corrupts the
// body while the status stays 200; a status-only check would pass against the
// exact bug.
func TestBuildAPIHandler_GzipRoundTrip(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return &datastores.ApiKeyResult{KeyId: 42}, nil
	}}

	const payload = `{"roster":"live","unit":"7th Cavalry","status":"active"}`
	h := buildAPIHandler(ds, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A length-aware inner handler sets the UNCOMPRESSED length; the
		// compression layer must strip it or clients truncate the gzip body.
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write([]byte(payload))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/roster/cbt", nil)
	req.Header.Set("Authorization", "Bearer cav7_goodkey")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	// Assert on Result().Header — the snapshot taken when the response was
	// committed, i.e. what the client actually received — not the recorder's
	// live header map, which would also reflect a Del that happened too late
	// to make it onto the wire.
	assert.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))
	assert.Empty(t, rr.Result().Header.Get("Content-Length"),
		"stale uncompressed Content-Length must be stripped")

	zr, err := gzip.NewReader(rr.Body)
	require.NoError(t, err, "body must be a valid gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err, "gzip body must decompress cleanly")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.Equal(t, payload, string(decoded),
		"gunzipped body must round-trip to the original payload")
}
