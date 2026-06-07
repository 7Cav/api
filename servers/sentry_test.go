package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// stubTransport is an in-memory sentry.Transport with a controllable Flush
// outcome. flushDrains=false simulates a drain timeout (queue still busy when
// the window closes — the hang-class failure mode), NOT a delivery failure:
// per the SDK's Flush contract, fast send failures dequeue and "flush"
// successfully.
type stubTransport struct {
	mu          sync.Mutex
	events      []*sentry.Event
	flushDrains bool
}

func (t *stubTransport) Configure(sentry.ClientOptions)        {}
func (t *stubTransport) Flush(time.Duration) bool              { return t.flushDrains }
func (t *stubTransport) FlushWithContext(context.Context) bool { return t.flushDrains }
func (t *stubTransport) Close()                                {}

func (t *stubTransport) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

func (t *stubTransport) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

// bindStubClient binds a stub-transport Sentry client to the global hub for
// the duration of the test, restoring the unbound (disabled) state afterwards.
func bindStubClient(t *testing.T, flushDrains bool) *stubTransport {
	t.Helper()
	transport := &stubTransport{flushDrains: flushDrains}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return transport
}

func TestSetupSentry_NoDSN_Disabled(t *testing.T) {
	viper.Set("SENTRY_DSN", "")
	defer viper.Set("SENTRY_DSN", "")

	var infoBuf bytes.Buffer
	Info.SetOutput(&infoBuf)
	defer Info.SetOutput(os.Stdout)

	enabled := setupSentry()

	assert.False(t, enabled, "setupSentry must be a no-op when SENTRY_DSN is unset")
	assert.Nil(t, sentry.CurrentHub().Client(), "no client may be bound without a DSN")
	assert.Contains(t, infoBuf.String(), "Sentry disabled — SENTRY_DSN not set",
		"the disabled state must be visible at boot, not silent")
}

func TestSetupSentry_MalformedDSN_DisabledWithoutPanic(t *testing.T) {
	viper.Set("SENTRY_DSN", "not-a-dsn")
	defer viper.Set("SENTRY_DSN", "")

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	enabled := setupSentry()

	assert.False(t, enabled, "a malformed DSN must disable capture, never take the API down")
	assert.Nil(t, sentry.CurrentHub().Client(), "no client may be bound after a failed init")
	assert.Contains(t, warnBuf.String(), "sentry init failed — continuing without error capture",
		"a config so broken it disables capture must say so in the logs, not just return false")
}

func TestSetupSentry_WithDSN_InitialisesErrorsOnlyClientWithRelease(t *testing.T) {
	viper.Set("SENTRY_DSN", "https://public@example.ingest.sentry.io/1")
	viper.Set("SENTRY_DEBUG", true)
	defer func() {
		viper.Set("SENTRY_DSN", "")
		viper.Set("SENTRY_DEBUG", false)
		sentry.CurrentHub().BindClient(nil)
	}()

	// Shrink the startup-probe flush and dial-check windows: the example DSN
	// is not routable, so neither boot check may stall the suite for seconds.
	restoreProbe := sentryStartupProbeTimeout
	sentryStartupProbeTimeout = 50 * time.Millisecond
	defer func() { sentryStartupProbeTimeout = restoreProbe }()
	restoreDial := sentryDialCheckTimeout
	sentryDialCheckTimeout = 50 * time.Millisecond
	defer func() { sentryDialCheckTimeout = restoreDial }()

	enabled := setupSentry()

	assert.True(t, enabled)
	client := sentry.CurrentHub().Client()
	require.NotNil(t, client, "a client must be bound when SENTRY_DSN is set")
	opts := client.Options()
	assert.Equal(t, version, opts.Release, "release must reuse the build-time version injection")
	assert.False(t, opts.EnableTracing, "errors only — no tracing")
	assert.Equal(t, 1.0, opts.SampleRate, "explicit zero must normalise to the SDK default of 1.0 — every error event is sent")
	assert.True(t, opts.AttachStacktrace, "string panics must carry stack traces")
	assert.True(t, opts.Debug, "SENTRY_DEBUG=true must enable SDK diagnostics without a rebuild")
	assert.NotNil(t, opts.DebugWriter, "SDK diagnostics must land in our logs, not the default stderr")

	// Don't trust NotNil — drive the WIRED hook with credential-bearing
	// request material and prove it scrubs. Any function would satisfy
	// NotNil; only the real scrubber survives this.
	require.NotNil(t, opts.BeforeSend, "scrubber must be wired so bearer material can never leave the process")
	fixture := sentry.NewEvent()
	fixture.Request = &sentry.Request{
		Headers: map[string]string{
			"Authorization": "Bearer cav7_wiredsecret",
			"Cookie":        "session=wired123",
		},
		Cookies:     "session=wired123",
		QueryString: "api_key=cav7_wiredsecret",
	}
	scrubbed := opts.BeforeSend(fixture, nil)
	require.NotNil(t, scrubbed, "the wired hook must sanitise, never drop, the event")
	payload, err := json.Marshal(scrubbed)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "cav7_wiredsecret",
		"the wired hook must strip bearer material from headers and query strings")
	assert.NotContains(t, string(payload), "session=wired123",
		"the wired hook must strip cookie material from headers and the cookie jar")
}

func TestSentryStartupProbe_DrainTimeout_WarnsLoudly(t *testing.T) {
	bindStubClient(t, false)

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	ok := sentryStartupProbe()

	assert.False(t, ok)
	assert.Contains(t, warnBuf.String(), "startup probe did not flush",
		"a hang-class transport stall is the one failure mode the drain probe can see — it must be loud")
}

func TestSentryStartupProbe_FlushDrains_NoWarning(t *testing.T) {
	transport := bindStubClient(t, true)

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	ok := sentryStartupProbe()

	assert.True(t, ok)
	assert.Empty(t, warnBuf.String(), "a drained probe must not warn")
	events := transport.Events()
	require.Len(t, events, 1, "the probe must push exactly one canary event through the transport")
	event := events[0]
	assert.Equal(t, "sentry startup probe", event.Message)
	assert.Equal(t, []string{"sentry-startup-probe"}, event.Fingerprint,
		"a fixed fingerprint folds every container restart into one Sentry issue — no resolve→reopen churn")
	assert.Equal(t, "true", event.Tags["probe"], "probe events must be filterable")
	assert.Equal(t, sentry.LevelInfo, event.Level, "the canary is informational, never an alertable error")
}

func TestSentryStartupProbe_ClientSideDrop_WarnsAndFails(t *testing.T) {
	// A BeforeSend veto makes CaptureMessage return a nil event ID — the
	// client dropped the canary before the transport ever saw it, so there is
	// nothing to drain and the flush alone would report a false success.
	client, err := sentry.NewClient(sentry.ClientOptions{
		Transport:  &stubTransport{flushDrains: true},
		BeforeSend: func(*sentry.Event, *sentry.EventHint) *sentry.Event { return nil },
	})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	ok := sentryStartupProbe()

	assert.False(t, ok, "a client-side drop means no canary exercised the pipeline — probe failed")
	assert.Contains(t, warnBuf.String(), "dropped client-side",
		"a canary that never reached the transport must be visible, not a silent flush success")
}

// TestSetupSentry_ProbeStall_WarnsAndWithholdsEnabledLine pins that
// setupSentry actually INVOKES the startup probe against the client it just
// configured: a stub transport injected through the test seam reports
// Flush=not-drained, so the stalled-transport premise holds by construction —
// no socket dial manufactures the stall, and machine load cannot invert the
// outcome (#179: a refused dial is a fast send outcome that drains the queue,
// so the old local-server stall lost the timing race under load). Binding a
// stub onto the hub is not enough here — setupSentry binds its own client, so
// the seam must reach the client it constructs. Deleting the
// sentryStartupProbe() call from setupSentry turns this red (no stall
// warning, the enabled line prints, no canary reaches the transport).
func TestSetupSentry_ProbeStall_WarnsAndWithholdsEnabledLine(t *testing.T) {
	transport := &stubTransport{flushDrains: false}
	sentryTransportOverride = transport
	t.Cleanup(func() { sentryTransportOverride = nil })

	// 127.0.0.1:1 refuses instantly — the suite's deterministic stand-in for
	// the dial-check pre-check, which is frozen and irrelevant to the stall
	// premise. Shrink its window anyway so a pathological environment cannot
	// stall the suite. The probe window itself no longer needs shrinking: the
	// stub's Flush reports not-drained immediately, regardless of load.
	viper.Set("SENTRY_DSN", "http://public@127.0.0.1:1/1")
	defer func() {
		viper.Set("SENTRY_DSN", "")
		sentry.CurrentHub().BindClient(nil)
	}()
	restoreDial := sentryDialCheckTimeout
	sentryDialCheckTimeout = 500 * time.Millisecond
	defer func() { sentryDialCheckTimeout = restoreDial }()

	var warnBuf, infoBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)
	Info.SetOutput(&infoBuf)
	defer Info.SetOutput(os.Stdout)

	enabled := setupSentry()

	assert.True(t, enabled, "a stalled probe means degraded, never disabled — capture stays on")
	assert.Contains(t, warnBuf.String(), "startup probe did not flush",
		"a transport that cannot drain within the window must be loud at boot")
	assert.NotContains(t, infoBuf.String(), "Sentry error capture enabled",
		"the success line must be withheld when the probe could not drain")
	events := transport.Events()
	require.Len(t, events, 1,
		"the canary must reach the transport of the client setupSentry just configured — the probe ran against THAT client, not some pre-bound stub")
	assert.Equal(t, "sentry startup probe", events[0].Message)
}

// TestSentryDebugEnabled_StrictParse pins the operator-trap fix: viper's
// GetBool silently maps "yes"/"on"/typos to false, which mid-incident reads
// as "debug on but nothing logged". Unparseable values must warn.
func TestSentryDebugEnabled_StrictParse(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		want     bool
		wantWarn bool
	}{
		{"unset — silently off", "", false, false},
		{"true", "true", true, false},
		{"1", "1", true, false},
		{"TRUE", "TRUE", true, false},
		{"false", "false", false, false},
		{"yes — operator trap, warn", "yes", false, true},
		{"on — operator trap, warn", "on", false, true},
		{"typo — warn", "ture", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Set("SENTRY_DEBUG", tc.raw)
			defer viper.Set("SENTRY_DEBUG", "")

			var warnBuf bytes.Buffer
			Warn.SetOutput(&warnBuf)
			defer Warn.SetOutput(os.Stdout)

			assert.Equal(t, tc.want, sentryDebugEnabled())
			if tc.wantWarn {
				assert.Contains(t, warnBuf.String(), "SENTRY_DEBUG",
					"an unparseable value must be called out by name, not silently treated as off")
				assert.Contains(t, warnBuf.String(), tc.raw, "the warning must echo the rejected value")
			} else {
				assert.Empty(t, warnBuf.String(), "parseable or unset values must not warn")
			}
		})
	}
}

func TestSentryDialCheck_UnreachableHost_WarnsWithHost(t *testing.T) {
	// 127.0.0.1:1 refuses instantly — the deterministic stand-in for the
	// wrong-DSN-host misconfig class the startup probe cannot see (fast send
	// failures still drain the queue). Shrink the dial window anyway so a
	// pathological environment cannot stall the suite.
	restore := sentryDialCheckTimeout
	sentryDialCheckTimeout = 500 * time.Millisecond
	defer func() { sentryDialCheckTimeout = restore }()

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	sentryDialCheck("https://public@127.0.0.1:1/1")

	assert.Contains(t, warnBuf.String(), "127.0.0.1:1",
		"the warning must name the unreachable host so the misconfig is actionable")
	assert.Contains(t, warnBuf.String(), "SENTRY_DEBUG",
		"the warning must point at the diagnostic that shows per-event send failures")
}

func TestSentryDialCheck_ReachableHost_Silent(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = lis.Close() }()

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	sentryDialCheck("https://public@" + lis.Addr().String() + "/1")

	assert.Empty(t, warnBuf.String(), "a reachable ingest host must not warn")
}

// chainFakeDatastore embeds the Datastore interface so it satisfies the type
// while implementing only ValidateApiKey — the single method the auth
// interceptor touches.
type chainFakeDatastore struct {
	datastores.Datastore
	validateApiKey func(string) (*datastores.ApiKeyResult, error)
}

func (f *chainFakeDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	return f.validateApiKey(rawKey)
}

// TestAPIUnaryInterceptors_AuthOuterSentryInner_KeyIDReachesEvent pins the
// gRPC interceptor chain ORDER as a tested contract — the mirror of the
// buildAPIHandler tests on the HTTP side. Auth must run outermost (attaching
// the validated key to ctx) and sentry inner (reading it for the key_id tag);
// flipping the pair loses the tag.
func TestAPIUnaryInterceptors_AuthOuterSentryInner_KeyIDReachesEvent(t *testing.T) {
	transport := bindStubClient(t, true)
	ds := &chainFakeDatastore{validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
		assert.Equal(t, "cav7_goodkey", token)
		return &datastores.ApiKeyResult{KeyId: 42}, nil
	}}

	interceptors := apiUnaryInterceptors(ds)
	require.Len(t, interceptors, 2, "the chain is exactly auth + sentry")
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.MilpacService/GetProfile"}
	// Mimic grpc.ChainUnaryInterceptor semantics: index 0 is outermost.
	chained := func(ctx context.Context, req any, handler grpc.UnaryHandler) (any, error) {
		return interceptors[0](ctx, req, info, func(ctx context.Context, req any) (any, error) {
			return interceptors[1](ctx, req, info, handler)
		})
	}

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer cav7_goodkey"))
	handlerErr := status.Error(codes.Internal, "boom")
	_, err := chained(ctx, nil, func(ctx context.Context, req any) (any, error) {
		return nil, handlerErr
	})

	assert.Equal(t, handlerErr, err, "the handler error must pass through the whole chain unchanged")
	events := transport.Events()
	require.Len(t, events, 1, "one Internal error through the chain must mean exactly one event")
	assert.Equal(t, "42", events[0].Tags["key_id"],
		"auth must run outermost and attach the key before sentry reads it — an order flip silently loses key_id")
}

func TestScrubEvent_StripsBearerAndCookieMaterial(t *testing.T) {
	event := sentry.NewEvent()
	event.Request = &sentry.Request{
		Method: "GET",
		URL:    "/api/v1/roster",
		Headers: map[string]string{
			"Authorization": "Bearer cav7_supersecrettoken",
			"cookie":        "session=abc123",
			"User-Agent":    "test-agent",
		},
		Cookies:     "session=abc123",
		QueryString: "api_key=cav7_supersecrettoken&page=2",
	}

	scrubbed := scrubEvent(event, nil)

	require.NotNil(t, scrubbed, "scrubbing must sanitise, never drop, the event")
	assert.Empty(t, scrubbed.Request.QueryString, "query strings can carry credentials — cleared wholesale")
	payload, err := json.Marshal(scrubbed)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "cav7_supersecrettoken", "bearer material must never appear in any event payload")
	assert.NotContains(t, string(payload), "session=abc123", "cookie material must never appear in any event payload")
	assert.Contains(t, string(payload), "test-agent", "non-sensitive headers stay for debugging value")
}

func TestScrubEvent_NoRequest_PassesThrough(t *testing.T) {
	event := sentry.NewEvent()
	event.Message = "plain event"

	scrubbed := scrubEvent(event, nil)

	require.NotNil(t, scrubbed)
	assert.Equal(t, "plain event", scrubbed.Message)
}

// TestFlushSentryOnShutdown_NoClient_WarnsAndSkips pins the guard's
// visibility: it only fires on programmer error (the call site must gate on
// setupSentry()), and silent misuse would mean shutdown flushes everyone
// assumes exist were never installed.
func TestFlushSentryOnShutdown_NoClient_WarnsAndSkips(t *testing.T) {
	require.Nil(t, sentry.CurrentHub().Client(), "test requires the disabled state")

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	flushSentryOnShutdown()

	assert.Contains(t, warnBuf.String(), "without an initialised sentry client",
		"misuse must be visible in logs, not a silent no-op")
}

func TestWatchShutdown_FlushesBeforeExit(t *testing.T) {
	cases := []struct {
		name     string
		flushOK  bool
		wantWarn bool
	}{
		{"flush succeeds — silent", true, false},
		{"flush times out — dropped events are visible", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warnBuf bytes.Buffer
			Warn.SetOutput(&warnBuf)
			defer Warn.SetOutput(os.Stdout)

			signals := make(chan os.Signal, 1)
			done := make(chan struct{})
			var order []string

			go watchShutdown(signals,
				func() bool {
					order = append(order, "flush")
					return tc.flushOK
				},
				func(code int) {
					assert.Equal(t, 0, code, "graceful shutdown must exit 0")
					order = append(order, "exit")
					close(done)
				})

			signals <- syscall.SIGTERM

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("watchShutdown never exited after the signal")
			}
			assert.Equal(t, []string{"flush", "exit"}, order, "buffered events must be flushed before the process exits")
			if tc.wantWarn {
				assert.Contains(t, warnBuf.String(), "shutdown flush window expired — remaining buffered events lost",
					"silently dropped events at shutdown must leave a trace in the logs")
			} else {
				assert.NotContains(t, warnBuf.String(), "flush window expired", "a clean flush must not warn")
			}
		})
	}
}
