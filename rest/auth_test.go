package rest

// The auth middleware tests moved here with the middleware itself (from
// servers/gateway): same two-tier 401 behavior, now pinned at its single
// source. The golden corpus pins the same behavior end-to-end on both
// stacks.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAuthDatastore embeds the Datastore interface so it satisfies the type
// without implementing every method; only ValidateApiKey is exercised by
// AuthMiddleware. Any other call panics (nil method) — a loud failure if a
// test accidentally reaches further into the datastore.
type fakeAuthDatastore struct {
	datastores.Datastore
	validateApiKey func(string) (*datastores.ApiKeyResult, error)
}

func (f *fakeAuthDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	return f.validateApiKey(rawKey)
}

// callMiddleware runs AuthMiddleware in front of a handler that records
// whether it was reached, and returns the recorded response plus the
// next-called flag and the key the handler saw on its context.
func callMiddleware(t *testing.T, ds datastores.Datastore, authHeader string) (*httptest.ResponseRecorder, bool, *datastores.ApiKeyResult) {
	t.Helper()
	nextCalled := false
	var seenKey *datastores.ApiKeyResult
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		seenKey = KeyFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	h := AuthMiddleware(ds, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr, nextCalled, seenKey
}

func TestAuthMiddleware_NoAuthHeader_NamesBearerScheme(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		t.Fatal("ValidateApiKey must not be called when no bearer token is present")
		return nil, nil
	}}
	rr, nextCalled, _ := callMiddleware(t, ds, "")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, nextCalled)
	body := rr.Body.String()
	assert.Contains(t, body, "Bearer")
	assert.Contains(t, body, "Authorization")
}

func TestAuthMiddleware_RawKeyNoBearerPrefix_NamesBearerScheme(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		t.Fatal("ValidateApiKey must not be called when the Bearer scheme is absent")
		return nil, nil
	}}
	rr, nextCalled, _ := callMiddleware(t, ds, "cav7_rawkeywithoutprefix")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, nextCalled)
	body := rr.Body.String()
	assert.Contains(t, body, "Bearer")
	assert.Contains(t, body, "Authorization")
}

func TestAuthMiddleware_BadKey_GenericUnauthorizedNoLeak(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
		assert.Equal(t, "cav7_badkey", token)
		return nil, nil // zero rows → nil result, no error
	}}
	rr, nextCalled, _ := callMiddleware(t, ds, "Bearer cav7_badkey")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, nextCalled)
	body := strings.TrimSpace(rr.Body.String())
	// Generic — must NOT name the Bearer scheme (that branch is for scheme errors)
	// and must not leak anything about the key.
	assert.Equal(t, "Unauthorized", body)
	assert.NotContains(t, body, "Bearer")
}

func TestAuthMiddleware_ValidKey_CallsNextWithKeyOnContext(t *testing.T) {
	key := &datastores.ApiKeyResult{KeyId: 1, UserId: 2}
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return key, nil
	}}
	rr, nextCalled, seenKey := callMiddleware(t, ds, "Bearer cav7_goodkey")

	require.True(t, nextCalled)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Same(t, key, seenKey,
		"the validated key must reach the handler via the request context")
}

func TestAuthMiddleware_ValidateError_GenericUnauthorized(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, io.ErrUnexpectedEOF
	}}
	rr, nextCalled, _ := callMiddleware(t, ds, "Bearer cav7_anykey")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, nextCalled)
	assert.Equal(t, "Unauthorized", strings.TrimSpace(rr.Body.String()))
}

// requireScope is the per-route authorization gate (ADR 0004: scope checks
// are per-handler; one scope never implies another).
func TestRequireScope(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := requireScope("read", next)

	t.Run("key with the scope passes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		req = req.WithContext(ContextWithKey(req.Context(),
			&datastores.ApiKeyResult{Scopes: map[string]struct{}{"read": {}}}))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("key with a different scope is denied", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
		req = req.WithContext(ContextWithKey(req.Context(),
			&datastores.ApiKeyResult{Scopes: map[string]struct{}{"read:tickets": {}}}))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
		assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
	})

	t.Run("no key on context is denied, not a panic", func(t *testing.T) {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}
