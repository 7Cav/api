package rest

// The auth middleware tests moved here with the middleware itself (from
// servers/gateway): same two-tier 401 behavior, now pinned at its single
// source. The golden corpus pins the same behavior end-to-end on both
// stacks.

import (
	"bytes"
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

// A datastore failure during key validation is a server fault, not a
// credential rejection: it must NOT masquerade as the 401 tier (telling a
// legitimate client its key went bad mid-outage), and it must be Error-logged
// with request context — the goldens structurally cannot witness this branch,
// so this test is the only thing keeping the outage visible. The bearer token
// must never reach the log.
func TestAuthMiddleware_DatastoreError_Is503AndLogged(t *testing.T) {
	buf := captureErrorLog(t)
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, io.ErrUnexpectedEOF
	}}
	rr, nextCalled, _ := callMiddleware(t, ds, "Bearer cav7_secrettoken")

	assert.False(t, nextCalled)
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":14,"message":"service unavailable","details":[]}`, rr.Body.String(),
		"Unavailable JSON via the choke point, leaking nothing about the key")

	logged := buf.String()
	assert.Contains(t, logged, "unexpected EOF")
	assert.Contains(t, logged, "GET")
	assert.Contains(t, logged, "/api/v1/whatever")
	assert.NotContains(t, logged, "cav7_secrettoken", "the bearer token must NEVER be logged")
}

// captureWarnLog redirects the package Warn logger into a buffer for one test
// and restores the previous writer afterwards — the 401 tiers Warn-log the
// rejected attempt (the only place a caller address is logged).
func captureWarnLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := Warn.Writer()
	Warn.SetOutput(&buf)
	t.Cleanup(func() { Warn.SetOutput(prev) })
	return &buf
}

// The two 401 warn lines must report the resolved client IP and the socket
// peer — `from <client-ip> (peer <RemoteAddr>)` — each keeping its existing
// distinct prefix (#190, ADR 0005). With a trusted peer the client IP is
// resolved from the forwarding headers.

func TestAuthMiddleware_BadScheme401_LogsClientAndPeer(t *testing.T) {
	withTrustedProxiesEnv(t, "10.0.0.0/8")
	require.NoError(t, InitTrustedProxies())
	buf := captureWarnLog(t)

	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		t.Fatal("ValidateApiKey must not be called for a scheme error")
		return nil, nil
	}}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.4")
	rr := httptest.NewRecorder()
	AuthMiddleware(ds, next).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	logged := buf.String()
	assert.Contains(t, logged, "bad bearer scheme", "keeps its distinct prefix")
	assert.Contains(t, logged, "from 203.0.113.9 (peer 10.0.0.5:443)")
}

func TestAuthMiddleware_UnknownKey401_LogsClientAndPeer(t *testing.T) {
	withTrustedProxiesEnv(t, "10.0.0.0/8")
	require.NoError(t, InitTrustedProxies())
	buf := captureWarnLog(t)

	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, nil // zero rows → unknown key
	}}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("Authorization", "Bearer cav7_unknownkey")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.4")
	rr := httptest.NewRecorder()
	AuthMiddleware(ds, next).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	logged := buf.String()
	// The unknown-key tier keeps its generic prefix (no "bad bearer scheme").
	assert.NotContains(t, logged, "bad bearer scheme")
	assert.Contains(t, logged, "from 203.0.113.9 (peer 10.0.0.5:443)")
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
