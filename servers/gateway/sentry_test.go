package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureTransport is an in-memory sentry.Transport recording every event the
// client would have sent over the wire — the observable seam for these tests.
// Flush here always drains (per the SDK contract Flush reports queue drain,
// not delivery); flushed records whether anything ever flushed synchronously,
// pinning the HTTP side of the deliberate sync/async flush asymmetry (gRPC
// panics flush, HTTP panics must not — the process survives and the async
// transport sends).
type captureTransport struct {
	mu      sync.Mutex
	events  []*sentry.Event
	flushed bool
}

func (t *captureTransport) Configure(sentry.ClientOptions) {}

func (t *captureTransport) Flush(time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushed = true
	return true
}

func (t *captureTransport) FlushWithContext(context.Context) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushed = true
	return true
}

func (t *captureTransport) Close() {}

func (t *captureTransport) Flushed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.flushed
}

func (t *captureTransport) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

func (t *captureTransport) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

// testRelease stands in for the build-time version injection so tests can
// prove events carry the release tag.
const testRelease = "test-release-1.2.3"

// bindCaptureClient binds a capture-only Sentry client to the global hub for
// the duration of the test, restoring the unbound (disabled) state afterwards.
// AttachStacktrace mirrors production (setupSentry) so string-panic events
// carry frames here exactly as they do live.
func bindCaptureClient(t *testing.T) *captureTransport {
	t.Helper()
	transport := &captureTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport, Release: testRelease, AttachStacktrace: true})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return transport
}

func TestSentryMiddleware_500Response_CapturedWithRouteAndStatus(t *testing.T) {
	transport := bindCaptureClient(t)
	h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))

	assert.Equal(t, http.StatusInternalServerError, rr.Code, "response must pass through untouched")

	events := transport.Events()
	require.Len(t, events, 1, "a 5xx response must produce exactly one event")
	event := events[0]
	assert.Equal(t, "/api/v1/roster", event.Tags["route"])
	assert.Equal(t, "500", event.Tags["http_status"])
	assert.Equal(t, "http", event.Tags["transport"])
	assert.Equal(t, sentry.LevelError, event.Level)
	assert.Equal(t, []string{"http-5xx", "GET", "500"}, event.Fingerprint,
		"grouping must be method+status, not URL — parameterized paths must not fan one failure into N issues")
}

// TestSentryMiddleware_WriteThenWriteHeader_Records200 pins the implicit-200
// latch: once a handler writes the body, net/http has committed status 200,
// and a buggy late WriteHeader(500) must not record a false 500.
func TestSentryMiddleware_WriteThenWriteHeader_Records200(t *testing.T) {
	transport := bindCaptureClient(t)
	h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`)) // commits the implicit 200
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))

	assert.Equal(t, http.StatusOK, rr.Code, "the committed implicit 200 is what the client saw")
	assert.Empty(t, transport.Events(), "a status the client never received must not produce an event")
}

func TestSentryMiddleware_NonServerErrorResponses_NoEvents(t *testing.T) {
	transport := bindCaptureClient(t)

	cases := []struct {
		name   string
		handle http.HandlerFunc
		want   int
	}{
		{"implicit 200", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{}`)) // no explicit WriteHeader
		}, http.StatusOK},
		{"explicit 200", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}, http.StatusOK},
		{"404", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no such profile", http.StatusNotFound)
		}, http.StatusNotFound},
		{"401", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			sentryMiddleware(tc.handle).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))
			assert.Equal(t, tc.want, rr.Code)
		})
	}

	assert.Empty(t, transport.Events(), "non-5xx responses must not generate Sentry noise")
}

func TestSentryMiddleware_HandlerPanic_CapturedThenRepanics(t *testing.T) {
	transport := bindCaptureClient(t)
	h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))

	assert.PanicsWithValue(t, "kaboom", func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tickets", nil))
	}, "panic must propagate so net/http's per-connection recovery behaves as today")

	events := transport.Events()
	require.Len(t, events, 1, "a handler panic must produce exactly one event")
	event := events[0]
	assert.Equal(t, "/api/v1/tickets", event.Tags["route"])
	assert.Equal(t, "http", event.Tags["transport"])
	assert.Equal(t, sentry.LevelFatal, event.Level)
	assert.Equal(t, testRelease, event.Release, "panic events must carry the release")
	assert.False(t, transport.Flushed(),
		"HTTP panics must NOT flush synchronously — the process survives and the async transport delivers")
	require.NotEmpty(t, event.Threads, "a string panic must carry a stack (AttachStacktrace shapes it as a thread)")
	require.NotNil(t, event.Threads[0].Stacktrace)
	assert.NotEmpty(t, event.Threads[0].Stacktrace.Frames, "a string panic without frames is undebuggable")
}

func TestSentryMiddleware_NoClient_PassThrough(t *testing.T) {
	// No client bound — the no-DSN state. Responses and panics must behave
	// exactly as they do today.
	require.Nil(t, sentry.CurrentHub().Client(), "test requires the disabled state")

	rr := httptest.NewRecorder()
	sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)

	assert.PanicsWithValue(t, "kaboom", func() {
		sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("kaboom")
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil))
	}, "panics must still propagate when Sentry is disabled")
}

func TestSentryMiddleware_BearerTokenNeverInPayload(t *testing.T) {
	transport := bindCaptureClient(t)
	const secret = "cav7_topsecrettokenvalue"

	makeReq := func() *http.Request {
		// Secret in BOTH vectors — header and query string — so the
		// whole-payload sweep below also pins the query-borne path
		// (?api_key=...), not just the Authorization header.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/roster?api_key="+secret, nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		return req
	}

	sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	})).ServeHTTP(httptest.NewRecorder(), makeReq())
	assert.PanicsWithValue(t, "kaboom", func() {
		sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("kaboom")
		})).ServeHTTP(httptest.NewRecorder(), makeReq())
	})

	events := transport.Events()
	require.Len(t, events, 2)
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		assert.NotContains(t, string(payload), secret, "bearer material must never appear in any event payload")
	}
}

// TestBuildAPIHandler_500BehindValidAuth_OneEventWithKeyID drives the actual
// production chain constructor — auth(sentry(compression)) — end to end: a
// 500 from the inner handler behind valid auth must produce exactly one
// event carrying the key_id auth attached.
func TestBuildAPIHandler_500BehindValidAuth_OneEventWithKeyID(t *testing.T) {
	transport := bindCaptureClient(t)
	ds := &fakeAuthDatastore{validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
		assert.Equal(t, "cav7_goodkey", token)
		return &datastores.ApiKeyResult{KeyId: 42}, nil
	}}
	h := buildAPIHandler(ds, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tickets", nil)
	req.Header.Set("Authorization", "Bearer cav7_goodkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	events := transport.Events()
	require.Len(t, events, 1, "one 500 through the full chain must mean exactly one event")
	assert.Equal(t, "42", events[0].Tags["key_id"], "the chain order is the contract: auth must run before sentry")
	assert.Equal(t, "500", events[0].Tags["http_status"])
}

// TestBuildAPIHandler_BadAuth_401NoEvents pins the other side of the chain
// order: rejected requests never reach Sentry (or the inner handler).
func TestBuildAPIHandler_BadAuth_401NoEvents(t *testing.T) {
	transport := bindCaptureClient(t)
	ds := &fakeAuthDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, nil // zero rows — invalid key
	}}
	innerCalled := false
	h := buildAPIHandler(ds, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tickets", nil)
	req.Header.Set("Authorization", "Bearer cav7_badkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.False(t, innerCalled, "auth must reject before anything inner runs")
	assert.Empty(t, transport.Events(), "auth rejections are expected behavior, not Sentry events")
}

// TestSentryMiddleware_BehindAuth_EventCarriesKeyID exercises the real chain
// shape — authMiddleware outside, sentryMiddleware inside — and proves the
// validated API key id reaches the event while the raw key never does.
func TestSentryMiddleware_BehindAuth_EventCarriesKeyID(t *testing.T) {
	transport := bindCaptureClient(t)
	const secret = "cav7_topsecrettokenvalue"

	ds := &fakeAuthDatastore{validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
		assert.Equal(t, secret, token)
		return &datastores.ApiKeyResult{KeyId: 42, UserId: 7}, nil
	}}
	h := authMiddleware(ds, sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	events := transport.Events()
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, "42", event.Tags["key_id"], "events carry the key id, not the key")
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), secret, "bearer material must never appear in any event payload")
}
