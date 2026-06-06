package rest

// Behavioral tests for the new stack's first-class Sentry wiring (#132):
// env-gating, panic recovery at the front of the chain, choke-point 5xx
// reports, tag discipline (key id and route pattern, never bearer material),
// and release tagging. Internal package tests (like auth_test.go) so they can
// swap the sentryTransport seam and drive the production SetupSentry path —
// no real network is ever touched.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sentryFakeDatastore embeds the Datastore interface so it satisfies the type
// without implementing every method (same pattern as fakeAuthDatastore); any
// unstubbed call panics loudly. One key: cav7_sentry_read → key id 7, scope
// "read" — the id (never the token) is what events must carry.
type sentryFakeDatastore struct {
	datastores.Datastore
	findAllRanks   func() ([]*proto.RankExpanded, error)
	validateApiKey func(string) (*datastores.ApiKeyResult, error)
}

func (f *sentryFakeDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	if f.validateApiKey != nil {
		return f.validateApiKey(rawKey)
	}
	if rawKey == "cav7_sentry_read" {
		return &datastores.ApiKeyResult{KeyId: 7, UserId: 3, Scopes: map[string]struct{}{"read": {}}}, nil
	}
	return nil, nil
}

func (f *sentryFakeDatastore) FindAllRanks() ([]*proto.RankExpanded, error) {
	return f.findAllRanks()
}

// sentryStubCache is the no-op TicketReferenceCache the sentry tests mount —
// New refuses nil, and no sentry test reaches the tickets routes.
type sentryStubCache struct{}

func (*sentryStubCache) StatusName(uint32) string                       { return "" }
func (*sentryStubCache) PriorityName(uint32) string                     { return "" }
func (*sentryStubCache) PrefixName(uint32) string                       { return "" }
func (*sentryStubCache) Category(uint32) *referencecache.CategoryRecord { return nil }
func (*sentryStubCache) CategoryAncestors(uint32) []uint32              { return nil }
func (*sentryStubCache) CategoryTree() []*referencecache.CategoryRecord { return nil }
func (*sentryStubCache) ExpandSubtree(ids []uint32) []uint32            { return ids }

// doRanks drives one authenticated GET /api/v1/milpacs/ranks through the
// public chain h.
func doRanks(h http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_sentry_read")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// transportMock captures every event the client would have sent to Sentry.
// Events arrive post-BeforeSend, so the production scrub is observable.
type transportMock struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *transportMock) Configure(sentry.ClientOptions) {}
func (t *transportMock) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}
func (t *transportMock) Flush(time.Duration) bool              { return true }
func (t *transportMock) FlushWithContext(context.Context) bool { return true }
func (t *transportMock) Close()                                {}
func (t *transportMock) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

// testRelease is the build-time version stand-in tests pass to SetupSentry.
const testRelease = "v-test-132"

// errOutageSentry is the injected datastore failure driving the handler-500
// tier (its text leaks into the response body — frozen handler behavior).
var errOutageSentry = errors.New("simulated datastore outage")

// enableSentry drives the PRODUCTION init path (SetupSentry) with the
// transport seam swapped for a capture mock, and restores the disabled state
// afterwards so the rest of the package's tests keep running with no client.
func enableSentry(t *testing.T) *transportMock {
	t.Helper()
	tr := &transportMock{}
	sentryTransport = tr
	viper.Set("SENTRY_DSN", "https://public@sentry.example.invalid/1")
	t.Cleanup(func() {
		sentry.CurrentHub().BindClient(nil)
		sentryTransport = nil
		viper.Set("SENTRY_DSN", "")
	})
	require.True(t, SetupSentry(testRelease), "SetupSentry must enable capture when SENTRY_DSN is set")
	return tr
}

// No DSN → complete no-op: nothing is initialised, exactly as in Phase 0 —
// no client, so every capture point in the package stays a pass-through.
func TestSetupSentry_NoDSNInitialisesNothing(t *testing.T) {
	viper.Set("SENTRY_DSN", "")
	assert.False(t, SetupSentry(testRelease), "no DSN must report capture disabled")
	assert.Nil(t, sentry.CurrentHub().Client(), "no DSN must bind no client")
}

// With a DSN the client is initialised and release-tagged from the build-time
// version the caller passes — every event carries it.
func TestSetupSentry_DSNEnablesClientWithRelease(t *testing.T) {
	tr := enableSentry(t)
	require.NotNil(t, sentry.CurrentHub().Client(), "a DSN must bind a client")

	sentry.CaptureMessage("wiring probe")
	events := tr.Events()
	require.Len(t, events, 1)
	assert.Equal(t, testRelease, events[0].Release, "events must carry the build-time release")
}

// The headline acceptance (#132): a panicking handler produces ONE event —
// tagged with the release, the validated key id, and the matched route
// pattern — and the request still completes as a 500 in the contract error
// shape instead of net/http killing the connection. One event, not two: the
// recovery's own 500 write passes the choke point, which must not
// double-report a panic it already captured.
func TestSentry_PanicCompletesAs500AndReportsTaggedEvent(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t) // panic + 5xx logging stays server-side, not in test output
	h := New(&sentryFakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		panic("ranks exploded")
	}}, &sentryStubCache{})

	var rr *httptest.ResponseRecorder
	require.NotPanics(t, func() { rr = doRanks(h) },
		"the sentry layer must recover the metrics layer's re-panic")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":13,"message":"Internal Server Error","details":[]}`, rr.Body.String(),
		"the panic 500 must keep the contract error shape")

	events := tr.Events()
	require.Len(t, events, 1, "one panic = one event; the choke-point 5xx report must not double-report")
	ev := events[0]
	assert.Equal(t, testRelease, ev.Release, "release tagging from the build-time version")
	assert.Equal(t, "GET /api/v1/milpacs/ranks", ev.Tags["route"], "route tag is the matched mux pattern")
	assert.Equal(t, "7", ev.Tags["key_id"], "key_id tag is the validated key's id")
	assert.Equal(t, sentry.LevelFatal, ev.Level)
	assert.Equal(t, "ranks exploded", ev.Message, "string panics arrive as message events (Phase 0 convention)")
}

// A handler 500 written through the writeError choke point produces one
// message event — tagged route/key_id/http_status, release-stamped, grouped
// by the Phase 0 method+status fingerprint so Sentry issues survive the
// cutover.
func TestSentry_Handler500ThroughChokePointReportsEvent(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)
	h := New(&sentryFakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, errOutageSentry
	}}, &sentryStubCache{})

	rr := doRanks(h)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	events := tr.Events()
	require.Len(t, events, 1, "one 5xx response = one event from the choke point")
	ev := events[0]
	assert.Equal(t, "HTTP 500 GET /api/v1/milpacs/ranks", ev.Message, "Phase 0 message format")
	assert.Equal(t, sentry.LevelError, ev.Level)
	assert.Equal(t, testRelease, ev.Release)
	assert.Equal(t, "GET /api/v1/milpacs/ranks", ev.Tags["route"], "route tag is the matched mux pattern")
	assert.Equal(t, "7", ev.Tags["key_id"])
	assert.Equal(t, "500", ev.Tags["http_status"])
	assert.Equal(t, []string{"http-5xx", "GET", "500"}, ev.Fingerprint,
		"grouping by method+status (Phase 0 idiom): parameterized routes must not fan one failure into N issues")
}

// 4xx responses are expected behavior, not errors worth an event: drive every
// client-error tier through the choke point (and the bypassing 401 tier) and
// assert silence.
func TestSentry_4xxProducesNoEvents(t *testing.T) {
	tr := enableSentry(t)
	h := New(&sentryFakeDatastore{}, &sentryStubCache{})

	for _, tc := range []struct {
		name, method, path, bearer string
		wantCode                   int
	}{
		{"401 missing credentials", http.MethodGet, "/api/v1/milpacs/ranks", "", http.StatusUnauthorized},
		{"401 unknown key", http.MethodGet, "/api/v1/milpacs/ranks", "cav7_unknown", http.StatusUnauthorized},
		{"403 wrong scope", http.MethodGet, "/api/v1/tickets", "cav7_sentry_read", http.StatusForbidden},
		{"404 unknown path", http.MethodGet, "/api/v1/does/not/exist", "cav7_sentry_read", http.StatusNotFound},
		{"400 binding error", http.MethodGet, "/api/v1/milpacs/profile/id/notanumber", "cav7_sentry_read", http.StatusBadRequest},
		{"405 wrong method", http.MethodPost, "/api/v1/milpacs/ranks", "cav7_sentry_read", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		require.Equal(t, tc.wantCode, rr.Code, tc.name)
	}

	assert.Empty(t, tr.Events(), "client errors must not produce Sentry events")
}

// The auth-tier 503 (ValidateApiKey failing mid-outage) writes through the
// same choke point BEFORE routing and before any key validates: the event is
// emitted with the route and key_id tags simply omitted — same semantics as
// the empty metrics labels on that tier.
func TestSentry_AuthOutage503ReportsWithoutRouteOrKeyTags(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)
	h := New(&sentryFakeDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, errOutageSentry
	}}, &sentryStubCache{})

	rr := doRanks(h)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)

	events := tr.Events()
	require.Len(t, events, 1, "the pre-routing 503 tier must still report")
	ev := events[0]
	assert.Equal(t, "HTTP 503 GET /api/v1/milpacs/ranks", ev.Message)
	assert.Equal(t, "503", ev.Tags["http_status"])
	assert.NotContains(t, ev.Tags, "route", "no route matched — the tag must be omitted, not empty")
	assert.NotContains(t, ev.Tags, "key_id", "no key validated — the tag must be omitted, not empty")
}

// Bearer material must NEVER appear in any event payload. Drive every
// reporting tier that handles the key (panic, handler 500, auth-outage 503)
// with its real token — plus a query-string token, the other place a caller
// might put one — then serialize every captured event in full and sweep for
// the token. The token is assembled at runtime: AttachStacktrace embeds ±5
// SOURCE lines around in-stack frames (ContextifyFrames), so a token literal
// in this test's body would trip the sweep as source-context noise — source
// can only ever leak source, never a runtime token value, which is exactly
// what this test must stay sensitive to.
func TestSentry_BearerMaterialAbsentFromEventPayloads(t *testing.T) {
	token := fmt.Sprintf("cav%d_%s", 7, "sweeptoken")
	acceptAny := func(string) (*datastores.ApiKeyResult, error) {
		return &datastores.ApiKeyResult{KeyId: 7, UserId: 3, Scopes: map[string]struct{}{"read": {}}}, nil
	}

	tr := enableSentry(t)
	captureErrorLog(t)

	panicStack := New(&sentryFakeDatastore{validateApiKey: acceptAny, findAllRanks: func() ([]*proto.RankExpanded, error) {
		panic("boom")
	}}, &sentryStubCache{})
	errorStack := New(&sentryFakeDatastore{validateApiKey: acceptAny, findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, errOutageSentry
	}}, &sentryStubCache{})
	outageStack := New(&sentryFakeDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		return nil, errOutageSentry
	}}, &sentryStubCache{})

	for _, h := range []http.Handler{panicStack, errorStack, outageStack} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks?key="+token, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cookie", "session="+token)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	events := tr.Events()
	require.Len(t, events, 3, "every tier must have reported")
	sawKeyID := false
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), token,
			"bearer material leaked into an event payload: %s", raw)
		if ev.Tags["key_id"] == "7" {
			sawKeyID = true
		}
	}
	assert.True(t, sawKeyID, "the validated key id (not the token) is how events are attributed")
}

// The production BeforeSend scrub (wired by SetupSentry) strips request auth
// material from any event that does carry request data — belt-and-braces for
// capture points that attach the request to the scope.
func TestSentry_BeforeSendScrubsRequestAuthMaterial(t *testing.T) {
	tr := enableSentry(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks?key=cav7_secret", nil)
	req.Header.Set("Authorization", "Bearer cav7_secret")
	req.Header.Set("authorization", "Bearer cav7_secret") // case-insensitive strip
	req.Header.Set("Proxy-Authorization", "Basic cav7_secret")
	req.Header.Set("Cookie", "session=cav7_secret")
	req.Header.Set("User-Agent", "sweep-test")

	hub := sentry.CurrentHub().Clone()
	hub.Scope().SetRequest(req)
	hub.CaptureMessage("request-bearing event")

	events := tr.Events()
	require.Len(t, events, 1)
	ev := events[0]
	require.NotNil(t, ev.Request, "request data itself survives — only auth material is stripped")
	assert.Empty(t, ev.Request.Cookies)
	assert.Empty(t, ev.Request.QueryString)
	for name := range ev.Request.Headers {
		assert.NotContains(t, []string{"authorization", "cookie", "proxy-authorization"},
			strings.ToLower(name), "auth header %q must be scrubbed", name)
	}
	assert.Equal(t, "sweep-test", ev.Request.Headers["User-Agent"], "non-auth headers survive")
}

// No DSN → complete no-op (Phase 0 parity): with no client bound the sentry
// layer is a pass-through — panics propagate exactly as they do today
// (net/http recovers them per-connection; here, to the test) with nothing
// written, and 5xx responses flow through the choke point unchanged with no
// capture machinery in the path.
func TestSentry_NoDSNIsCompletePassThrough(t *testing.T) {
	require.Nil(t, sentry.CurrentHub().Client(), "precondition: no client bound")
	captureErrorLog(t)

	h := New(&sentryFakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		panic("ranks exploded")
	}}, &sentryStubCache{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_sentry_read")
	rr := httptest.NewRecorder()
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(rr, req)
		return nil
	}()
	require.Equal(t, "ranks exploded", panicked,
		"without a DSN the panic must propagate unchanged — no recovery, no rewriting of crash semantics")
	assert.Zero(t, rr.Body.Len(), "no recovery layer means nothing is written for the panicked request")

	h500 := New(&sentryFakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, errOutageSentry
	}}, &sentryStubCache{})
	rr = doRanks(h500)
	assert.Equal(t, http.StatusInternalServerError, rr.Code, "the 5xx path itself is untouched")
}

// A panic AFTER the response committed cannot become a contract 500 — the
// status is on the wire. The event is still captured, then the panic
// re-raises so net/http aborts the connection and the client sees the
// truncation instead of trusting a half response. (Composition mirrors New;
// mini-chain because no real handler writes before panicking.)
func TestSentry_PanicAfterCommittedResponseReportsAndRepanics(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)

	mux := http.NewServeMux()
	mux.Handle("GET /boom", sentryLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial body before the panic"))
		panic("exploded after the 200")
	})))
	h := sentryMiddleware(metricsMiddleware(mux))

	rr := httptest.NewRecorder()
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/boom", nil))
		return nil
	}()
	require.Equal(t, "exploded after the 200", panicked,
		"a committed response must re-raise — net/http's connection abort is the only honest signal left")
	assert.Equal(t, http.StatusOK, rr.Code, "the committed status stays untouched")
	assert.Equal(t, "partial body before the panic", rr.Body.String(), "no 500 body appended behind a committed 200")

	events := tr.Events()
	require.Len(t, events, 1, "the event is captured even when the response cannot be rewritten")
	assert.Equal(t, "GET /boom", events[0].Tags["route"])
}

// The gzip layer commits bytes during panic unwind (its deferred Close emits
// the stream header even when nothing was written), so a gzipped panic takes
// the committed path: event captured, panic re-raised — same connection-abort
// semantics the old stack had for every panic.
func TestSentry_GzippedPanicReportsAndRepanics(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)
	h := New(&sentryFakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		panic("ranks exploded")
	}}, &sentryStubCache{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_sentry_read")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(rr, req)
		return nil
	}()
	require.Equal(t, "ranks exploded", panicked)

	events := tr.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "GET /api/v1/milpacs/ranks", events[0].Tags["route"])
	assert.Equal(t, "7", events[0].Tags["key_id"])
}

// http.ErrAbortHandler is the stdlib's sentinel for a DELIBERATE abort —
// net/http suppresses its stack trace, and httputil.ReverseProxy (the docs
// proxy the cutover slice mounts) panics with it on client disconnects. The
// recovery layer must re-raise it unreported: not an error, no event, no 500
// rewrite — aborting means the connection dies, exactly as the handler asked.
func TestSentry_ErrAbortHandlerRepanicsUnreported(t *testing.T) {
	tr := enableSentry(t)

	mux := http.NewServeMux()
	mux.Handle("GET /abort", sentryLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})))
	h := sentryMiddleware(metricsMiddleware(mux))

	rr := httptest.NewRecorder()
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/abort", nil))
		return nil
	}()
	require.Equal(t, http.ErrAbortHandler, panicked,
		"the abort sentinel must propagate for net/http to honour")
	assert.Zero(t, rr.Body.Len(), "an abort must not be rewritten into a contract 500")
	assert.Empty(t, tr.Events(), "a deliberate abort is not an error — no event")
}

// commitWriter is the outermost wrapper when sentry is enabled, so it must
// expose Unwrap for http.ResponseController — otherwise Flusher/Hijacker/
// deadline control silently vanish for every inner layer (same obligation the
// metrics statusWriter pins one layer in).
func TestSentry_ResponseControllerTunnelsThroughCommitWriter(t *testing.T) {
	enableSentry(t)

	flushErr := make(chan error, 1) // handler runs on the server goroutine
	h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flushErr <- http.NewResponseController(w).Flush()
	}))

	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := srv.Client().Get(srv.URL + "/")
	require.NoError(t, err)
	res.Body.Close()
	require.NoError(t, <-flushErr,
		"ResponseController.Flush must reach the underlying writer via commitWriter.Unwrap")
}
