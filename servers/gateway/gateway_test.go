package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildAPIHandler_ResponseCacheRemoved pins the Phase 2 de-cache (#123):
// the response-cache middleware is OUT of the /api chain. Observable contract,
// driven through the production chain constructor on a cacheable (non-tickets)
// GET route with a nil *cache.RedisCache:
//
//   - every request reaches the inner handler — nothing is served from a
//     cache, and the nil RedisCache is never touched (the old chain would
//     panic dereferencing it on this route);
//   - the X-Cache header is gone (the PRD's enumerated break for this slice);
//   - the response body passes through unbuffered and intact.
func TestBuildAPIHandler_ResponseCacheRemoved(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return &datastores.ApiKeyResult{KeyId: 42}, nil
	}}

	innerCalls := 0
	h := buildAPIHandler(ds, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
