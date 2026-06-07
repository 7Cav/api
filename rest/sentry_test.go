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
	"io"
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
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	findAllRanks     func() ([]*proto.RankExpanded, error)
	findProfilesById func(...uint64) ([]*proto.Profile, error)
	validateApiKey   func(string) (*datastores.ApiKeyResult, error)
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

func (f *sentryFakeDatastore) FindProfilesById(ids ...uint64) ([]*proto.Profile, error) {
	return f.findProfilesById(ids...)
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

// A malformed DSN must fail SAFE — telemetry must never take the API down
// (the init-failure branch in SetupSentry): Init errors, capture reports
// disabled, no client binds, and nothing panics. The API then runs exactly as
// in the no-DSN case.
func TestSetupSentry_InvalidDSNFailsSafeWithoutClient(t *testing.T) {
	viper.Set("SENTRY_DSN", "not-a-dsn")
	t.Cleanup(func() { viper.Set("SENTRY_DSN", "") })

	var enabled bool
	require.NotPanics(t, func() { enabled = SetupSentry(testRelease) },
		"a bad DSN must never panic the boot path")
	assert.False(t, enabled, "a failed init must report capture disabled")
	assert.Nil(t, sentry.CurrentHub().Client(), "a failed init must bind no client")
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
// double-report a panic it already captured. The metrics half of the
// recovery-outside-metrics ruling is pinned alongside: exactly one counter
// increment, status="500", under the route — metering already done when the
// re-panic reaches this layer.
func TestSentry_PanicCompletesAs500AndReportsTaggedEvent(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t) // panic + 5xx logging stays server-side, not in test output
	h := New(&sentryFakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		panic("ranks exploded")
	}}, &sentryStubCache{})

	panicCounter := requestsTotal.WithLabelValues("GET /api/v1/milpacs/ranks", "GET", "500", "7")
	before := testutil.ToFloat64(panicCounter)

	var rr *httptest.ResponseRecorder
	require.NotPanics(t, func() { rr = doRanks(h) },
		"the sentry layer must recover the metrics layer's re-panic")

	assert.Equal(t, before+1, testutil.ToFloat64(panicCounter),
		`exactly one status="500" increment under the route — recovery outside metrics must not skip or double metering`)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":13,"message":"Internal Server Error","details":[]}`, rr.Body.String(),
		"the panic 500 must keep the contract error shape")
	assert.Empty(t, rr.Header().Get("Cache-Control"),
		"the contract 500 after a panic carries no freshness signal — the exact leak cacheControlWriter's commit-time design exists to prevent")

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
	// routeLabel: the committed 200 meters under the route, keeping the
	// registry's never-routed contract (route="" ⇒ auth tiers or pre-routing
	// panic, swept post-run by TestMain, #173) — an unlabeled probe would
	// mint route="" 200.
	mux.Handle("GET /boom", routeLabel(sentryLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial body before the panic"))
		panic("exploded after the 200")
	}))))
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

// Two simultaneously in-flight failing requests must keep ISOLATED tags:
// each event carries its own request's key_id and route, never the other's.
// This is what the per-request hub.Clone() in sentryMiddleware buys — a
// future "simplification" to the global hub would make concurrent requests
// race on one shared scope. Channel-gated (a WaitGroup both handlers block
// on) so both requests are inside the chain at the same time; CI runs plain
// `go test`, so the -race run of this test is the only realistic guard.
func TestSentry_ConcurrentRequestsKeepIsolatedTags(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)

	// inside is swapped per round; the swap is sequenced by wg.Wait, so the
	// fakes' reads never race the assignment.
	var inside *sync.WaitGroup
	gate := func() {
		inside.Done()
		inside.Wait() // releases only once BOTH requests are inside the chain
	}
	h := New(&sentryFakeDatastore{
		validateApiKey: func(raw string) (*datastores.ApiKeyResult, error) {
			switch raw {
			case "cav7_key_a":
				return &datastores.ApiKeyResult{KeyId: 11, UserId: 3, Scopes: map[string]struct{}{"read": {}}}, nil
			case "cav7_key_b":
				return &datastores.ApiKeyResult{KeyId: 22, UserId: 4, Scopes: map[string]struct{}{"read": {}}}, nil
			}
			return nil, nil
		},
		findAllRanks: func() ([]*proto.RankExpanded, error) {
			gate()
			return nil, errOutageSentry
		},
		findProfilesById: func(...uint64) ([]*proto.Profile, error) {
			gate()
			return nil, errOutageSentry
		},
	}, &sentryStubCache{})

	send := func(path, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// One interleaved pair is a weak witness: with a shared global hub the
	// two reports can still serialize into the right tags by scheduling
	// luck (red-validation caught the CurrentHub()-no-Clone mutation only
	// at -count=20). Repeating the gated pair raises a single plain run to
	// ~80% red under that mutation (measured: 4/5 at 300 rounds — the two
	// reports usually serialize even with both requests gated into the
	// chain, so per-round contamination odds are low); under -race the
	// mutation is a straight data race on the shared scope and fails
	// deterministically. CI runs plain `go test ./...` — the branch gate's
	// -race pass is the reliable guard until CI grows one.
	const rounds = 300
	for i := 0; i < rounds; i++ {
		inside = &sync.WaitGroup{}
		inside.Add(2)
		var wg sync.WaitGroup
		wg.Add(2)
		var rrA, rrB *httptest.ResponseRecorder
		go func() { defer wg.Done(); rrA = send("/api/v1/milpacs/ranks", "cav7_key_a") }()
		go func() { defer wg.Done(); rrB = send("/api/v1/milpacs/profile/id/5", "cav7_key_b") }()
		wg.Wait()

		require.Equal(t, http.StatusInternalServerError, rrA.Code)
		require.Equal(t, http.StatusInternalServerError, rrB.Code)
	}

	events := tr.Events()
	require.Len(t, events, 2*rounds, "every failing request = one event")
	want := map[string]string{
		"11": "GET /api/v1/milpacs/ranks",
		"22": "GET /api/v1/milpacs/profile/id/{user_id}",
	}
	seen := map[string]int{}
	for _, ev := range events {
		require.Equal(t, want[ev.Tags["key_id"]], ev.Tags["route"],
			"each event must carry its OWN request's key_id and route — per-request hub.Clone() isolation")
		seen[ev.Tags["key_id"]]++
	}
	assert.Equal(t, map[string]int{"11": rounds, "22": rounds}, seen,
		"event count per key must match the rounds — no dropped or doubled reports")
}

// http.ErrAbortHandler is the stdlib's sentinel for a DELIBERATE abort —
// net/http suppresses its stack trace, and stdlib handlers panic with it on
// purpose (e.g. httputil.ReverseProxy on client disconnects). The
// recovery layer must re-raise it unreported: not an error, no event, no 500
// rewrite — aborting means the connection dies, exactly as the handler asked.
func TestSentry_ErrAbortHandlerRepanicsUnreported(t *testing.T) {
	tr := enableSentry(t)

	mux := http.NewServeMux()
	// routeLabel: keeps the probe inside the registry's never-routed
	// contract (#173) — unlabeled, its relabeled 500 would meter as
	// route="", conflating this routed abort with a pre-routing panic.
	mux.Handle("GET /abort", routeLabel(sentryLabel(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))))
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

// commitWriter sits in the write-delegation chain when sentry is enabled, so
// it must keep http.ResponseController working — Flush via its own FlushError
// (which marks the response committed), Hijacker/deadline control via Unwrap
// — otherwise that control silently vanishes for every inner layer (same
// obligation the metrics statusWriter carries).
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
		"ResponseController.Flush must reach the underlying writer through commitWriter")
}

// Flush-then-panic: FlushError marks the response committed, so a panic after
// an explicit flush re-raises (connection abort) instead of writing a
// contract 500 over the response already flushed to the wire — the blind spot
// Unwrap-only tunnelling had (Flush used to bypass the committed flag
// entirely).
//
// The successful-flush case here is the boundary of #164's rollback: only a
// flush the delegate REFUSED (http.ErrNotSupported — nothing sent) may clear
// the latch; a flush that reached the recorder really committed.
func TestSentry_PanicAfterFlushRepanicsInsteadOfRewriting(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)

	h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, http.NewResponseController(w).Flush(),
			"the recorder is a Flusher — FlushError must delegate to it")
		panic("exploded after the flush")
	}))

	rr := httptest.NewRecorder()
	panicked := func() (p any) {
		defer func() { p = recover() }()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/flush-boom", nil))
		return nil
	}()
	require.Equal(t, "exploded after the flush", panicked,
		"a flushed (committed) response must re-raise — no honest 500 is possible any more")
	assert.True(t, rr.Flushed, "the flush must have reached the base writer")
	assert.Zero(t, rr.Body.Len(), "no contract 500 body behind the flushed response")
	require.Len(t, tr.Events(), 1, "the panic is still captured even when the response cannot be rewritten")
}

// A first flush the delegate fails with http.ErrNotSupported sent NOTHING of
// the final response — no layer below could flush, so the wire the final
// response would land on is untouched (#164). The committed latch FlushError
// itself set must roll back (mirror of cacheControlWriter's #163 R1 rollback:
// latch-was-ours + errors.Is), or a later handler panic takes the committed
// re-panic path and aborts the connection instead of writing the contract
// 500 over a wire the final response genuinely never touched.
//
// The reachable trigger is a BASE writer below sentryMiddleware with no flush
// support: test harnesses today (noFlushWriter here), plausibly a cutover-era
// wrapper like http.TimeoutHandler later — production net/http's base writer
// always supports flush. The production gzip chain cannot trigger this
// rollback in either era: before #167's flush support, ResponseController's
// walk dead-ended at the gzip layer, above commitWriter, so a gzipped flush
// never set the latch; with #167's gzipResponseWriter.FlushError, the gzip
// header and sync block go through commitWriter.Write BEFORE the delegated
// flush can fail, so the latch is genuinely Write's — never the flush's to
// roll back. And a gzipped panic re-panics regardless — GzipMiddleware's
// deferred Close commits stream bytes through commitWriter during unwind
// (TestSentry_GzippedPanicReportsAndRepanics).
func TestSentry_PanicAfterFailedFirstFlushWritesContract500(t *testing.T) {
	tr := enableSentry(t)
	captureErrorLog(t)

	h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := http.NewResponseController(w).Flush()
		require.ErrorIs(t, err, http.ErrNotSupported,
			"an unflushable base writer refuses the flush — nothing was sent")
		panic("exploded after the failed flush")
	}))

	rr := httptest.NewRecorder()
	require.NotPanics(t, func() {
		// noFlushWriter (cachecontrol_internal_test.go): the shape
		// gzipResponseWriter had before #167 — no FlushError, no Flusher,
		// no Unwrap.
		h.ServeHTTP(&noFlushWriter{rr: rr}, httptest.NewRequest(http.MethodGet, "/flush-fail-boom", nil))
	}, "nothing reached the wire — recovery must write the contract 500, not re-panic into a connection abort")

	res := rr.Result()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode,
		"the contract 500 must land on the untouched wire")
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))
	assert.JSONEq(t, `{"code":13,"message":"Internal Server Error","details":[]}`, rr.Body.String(),
		"the panic 500 must keep the contract error shape")
	require.Len(t, tr.Events(), 1, "one panic = one event — the recovery's own 500 write must not double-report")
}

// The latch-was-ours guard on the #164 rollback: a failed flush AFTER a prior
// Write or a prior latching WriteHeader (a final status, or the 101
// carve-out — see the informational predicate) must NOT reset the latch —
// the commit already happened (bytes or the final status line genuinely out,
// or the stdlib's own 101 latch set), so the only
// honest panic semantics left are the
// committed path's re-panic and connection abort. A rollback here would write
// a contract 500 behind a response already started — the exact corruption
// commitWriter exists to prevent.
func TestSentry_FailedFlushAfterCommitKeepsRepanicSemantics(t *testing.T) {
	cases := []struct {
		name   string
		commit func(w http.ResponseWriter)
		// wantStatus/wantBody pin the wire exactly as the prior commit left it.
		wantStatus int
		wantBody   string
	}{
		{
			name:       "prior Write",
			commit:     func(w http.ResponseWriter) { _, _ = w.Write([]byte("partial body before the flush")) },
			wantStatus: http.StatusOK, // net/http implied 200
			wantBody:   "partial body before the flush",
		},
		{
			name:       "prior WriteHeader",
			commit:     func(w http.ResponseWriter) { w.WriteHeader(http.StatusAccepted) },
			wantStatus: http.StatusAccepted,
			wantBody:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := enableSentry(t)
			captureErrorLog(t)

			h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.commit(w)
				err := http.NewResponseController(w).Flush()
				require.ErrorIs(t, err, http.ErrNotSupported,
					"an unflushable base writer refuses the flush")
				panic("exploded after the failed flush")
			}))

			rr := httptest.NewRecorder()
			panicked := func() (p any) {
				defer func() { p = recover() }()
				h.ServeHTTP(&noFlushWriter{rr: rr}, httptest.NewRequest(http.MethodGet, "/committed-flush-fail", nil))
				return nil
			}()
			require.Equal(t, "exploded after the failed flush", panicked,
				"a committed response must re-raise — the failed flush must not roll back a latch it did not set")

			res := rr.Result()
			assert.Equal(t, tc.wantStatus, res.StatusCode, "the committed status stays untouched")
			assert.Equal(t, tc.wantBody, rr.Body.String(), "no contract 500 body behind the committed response")
			require.Len(t, tr.Events(), 1, "the panic is still captured even when the response cannot be rewritten")
		})
	}
}

// flushErrorWriter is a base writer whose flush genuinely FAILS rather than
// being refused: FlushError returns the injected error, never
// http.ErrNotSupported. The real-server analogue is a conn write error — the
// implied 200 commits to the wire BEFORE the error returns to the handler.
type flushErrorWriter struct {
	rr  *httptest.ResponseRecorder
	err error
}

func (w *flushErrorWriter) Header() http.Header         { return w.rr.Header() }
func (w *flushErrorWriter) Write(b []byte) (int, error) { return w.rr.Write(b) }
func (w *flushErrorWriter) WriteHeader(code int)        { w.rr.WriteHeader(code) }
func (w *flushErrorWriter) FlushError() error           { return w.err }

// The errors.Is discriminator on the #164 rollback: ONLY the delegate's
// refusal (http.ErrNotSupported — nothing sent) may clear the latch. A first
// flush failing with a genuine I/O error is the opposite world: the delegate
// really flushed, so on a real server the implied 200 commits to the wire
// before the conn-write error returns, and the latch must KEEP — the later
// panic takes the committed path (re-panic, wire untouched by recovery, one
// event). Broadening the discriminator to any non-nil error would roll the
// latch back and let the recovery write a contract 500 behind a sent 200 —
// the exact corruption commitWriter exists to prevent. Mirror image of
// TestSentry_PanicAfterFailedFirstFlushWritesContract500, opposite outcome.
func TestSentry_PanicAfterGenuineFlushErrorKeepsRepanicSemantics(t *testing.T) {
	errConnReset := errors.New("conn reset")
	cases := []struct {
		name     string
		flushErr error // what the base writer's FlushError returns
		want     error // the sentinel the handler's premise guard must see through the chain
	}{
		{name: "plain error", flushErr: errConnReset, want: errConnReset},
		{name: "wrapped sentinel", flushErr: fmt.Errorf("flush tcp conn: %w", io.ErrClosedPipe), want: io.ErrClosedPipe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := enableSentry(t)
			captureErrorLog(t)

			h := sentryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				err := http.NewResponseController(w).Flush()
				require.ErrorIs(t, err, tc.want,
					"the base writer's genuine flush error must surface to the handler")
				require.NotErrorIs(t, err, http.ErrNotSupported,
					"premise: a real flush failure, not the delegate's refusal")
				panic("exploded after the genuine flush error")
			}))

			rr := httptest.NewRecorder()
			panicked := func() (p any) {
				defer func() { p = recover() }()
				h.ServeHTTP(&flushErrorWriter{rr: rr, err: tc.flushErr},
					httptest.NewRequest(http.MethodGet, "/genuine-flush-error-boom", nil))
				return nil
			}()
			require.Equal(t, "exploded after the genuine flush error", panicked,
				"a genuinely failed flush really committed — the latch must keep and the panic re-raise")

			res := rr.Result()
			assert.Empty(t, res.Header.Get("Content-Type"), "no contract 500 headers behind the kept latch")
			assert.Zero(t, rr.Body.Len(), "recovery must not write a contract 500 behind a flush that reached the wire")
			require.Len(t, tr.Events(), 1, "the panic is still captured even when the response cannot be rewritten")
		})
	}
}
