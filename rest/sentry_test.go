package rest

// Behavioral tests for the new stack's first-class Sentry wiring (#132):
// env-gating, panic recovery at the front of the chain, choke-point 5xx
// reports, tag discipline (key id and route pattern, never bearer material),
// and release tagging. Internal package tests (like auth_test.go) so they can
// swap the sentryTransport seam and drive the production SetupSentry path —
// no real network is ever touched.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
func (*sentryStubCache) PrefixName(uint32) string                      { return "" }
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
func (t *transportMock) Flush(time.Duration) bool             { return true }
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
