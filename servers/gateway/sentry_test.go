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
type captureTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *captureTransport) Configure(sentry.ClientOptions)        {}
func (t *captureTransport) Flush(time.Duration) bool              { return true }
func (t *captureTransport) FlushWithContext(context.Context) bool { return true }
func (t *captureTransport) Close()                                {}

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

// bindCaptureClient binds a capture-only Sentry client to the global hub for
// the duration of the test, restoring the unbound (disabled) state afterwards.
func bindCaptureClient(t *testing.T) *captureTransport {
	t.Helper()
	transport := &captureTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport})
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
		req := httptest.NewRequest(http.MethodGet, "/api/v1/roster", nil)
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
