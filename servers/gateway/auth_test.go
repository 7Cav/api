package gateway

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
// authMiddleware. Any other call panics (nil method) — a loud failure if a
// test accidentally reaches further into the datastore.
type fakeAuthDatastore struct {
	datastores.Datastore
	validateApiKey func(string) (*datastores.ApiKeyResult, error)
}

func (f *fakeAuthDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	return f.validateApiKey(rawKey)
}

// callMiddleware runs authMiddleware in front of a handler that records whether
// it was reached, and returns the recorded response plus the next-called flag.
func callMiddleware(t *testing.T, ds datastores.Datastore, authHeader string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})
	h := authMiddleware(ds, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr, nextCalled
}

func TestAuthMiddleware_NoAuthHeader_NamesBearerScheme(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		t.Fatal("ValidateApiKey must not be called when no bearer token is present")
		return nil, nil
	}}
	rr, nextCalled := callMiddleware(t, ds, "")

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
	rr, nextCalled := callMiddleware(t, ds, "cav7_rawkeywithoutprefix")

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
	rr, nextCalled := callMiddleware(t, ds, "Bearer cav7_badkey")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, nextCalled)
	body := strings.TrimSpace(rr.Body.String())
	// Generic — must NOT name the Bearer scheme (that branch is for scheme errors)
	// and must not leak anything about the key.
	assert.Equal(t, "Unauthorized", body)
	assert.NotContains(t, body, "Bearer")
}

func TestAuthMiddleware_ValidKey_CallsNext(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return &datastores.ApiKeyResult{KeyId: 1, UserId: 2}, nil
	}}
	rr, nextCalled := callMiddleware(t, ds, "Bearer cav7_goodkey")

	require.True(t, nextCalled)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAuthMiddleware_ValidateError_GenericUnauthorized(t *testing.T) {
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, io.ErrUnexpectedEOF
	}}
	rr, nextCalled := callMiddleware(t, ds, "Bearer cav7_anykey")

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, nextCalled)
	assert.Equal(t, "Unauthorized", strings.TrimSpace(rr.Body.String()))
}
