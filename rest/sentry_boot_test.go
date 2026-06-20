package rest

import (
	"bytes"
	"context"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainTransport is an in-memory sentry.Transport with a controllable Flush
// outcome. flushDrains=false simulates a drain timeout (queue still busy when
// the window closes — the hang-class failure mode), NOT a delivery failure:
// per the SDK's Flush contract, fast send failures dequeue and "flush"
// successfully. It complements transportMock (sentry_test.go), whose Flush is
// fixed true — these boot-lifecycle tests need to drive the not-drained path.
type drainTransport struct {
	mu          sync.Mutex
	events      []*sentry.Event
	flushDrains bool
}

func (t *drainTransport) Configure(sentry.ClientOptions)        {}
func (t *drainTransport) Flush(time.Duration) bool              { return t.flushDrains }
func (t *drainTransport) FlushWithContext(context.Context) bool { return t.flushDrains }
func (t *drainTransport) Close()                                {}

func (t *drainTransport) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

func (t *drainTransport) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

// bindDrainClient binds a drain-transport Sentry client to the global hub for
// the duration of the test, restoring the unbound (disabled) state afterwards.
func bindDrainClient(t *testing.T, flushDrains bool) *drainTransport {
	t.Helper()
	transport := &drainTransport{flushDrains: flushDrains}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return transport
}

func TestSentryStartupProbe_DrainTimeout_WarnsLoudly(t *testing.T) {
	bindDrainClient(t, false)

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	ok := sentryStartupProbe()

	assert.False(t, ok)
	assert.Contains(t, warnBuf.String(), "startup probe did not flush",
		"a hang-class transport stall is the one failure mode the drain probe can see — it must be loud")
}

func TestSentryStartupProbe_FlushDrains_NoWarning(t *testing.T) {
	transport := bindDrainClient(t, true)

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
		Transport:  &drainTransport{flushDrains: true},
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

// TestSetupSentry_ProbeStall_WarnsAndWithholdsEnabledLine pins that SetupSentry
// actually INVOKES the startup probe against the client it just configured: a
// drain transport injected through the sentryTransport seam reports
// Flush=not-drained, so the stalled-transport premise holds by construction —
// no socket dial manufactures the stall, and machine load cannot invert the
// outcome (#179: a refused dial is a fast send outcome that drains the queue,
// so the old local-server stall lost the timing race under load). Deleting the
// sentryStartupProbe() call from SetupSentry turns this red (no stall warning,
// the enabled line prints, no canary reaches the transport).
func TestSetupSentry_ProbeStall_WarnsAndWithholdsEnabledLine(t *testing.T) {
	transport := &drainTransport{flushDrains: false}
	sentryTransport = transport
	t.Cleanup(func() { sentryTransport = nil })

	// 127.0.0.1:1 refuses instantly — the suite's deterministic stand-in for
	// the dial-check pre-check, which is frozen and irrelevant to the stall
	// premise. Shrink its window anyway so a pathological environment cannot
	// stall the suite. The probe window itself no longer needs shrinking: the
	// stub's Flush reports not-drained immediately, regardless of load.
	viper.Set("SENTRY_DSN", "http://public@127.0.0.1:1/1")
	t.Cleanup(func() {
		viper.Set("SENTRY_DSN", "")
		sentry.CurrentHub().BindClient(nil)
	})
	restoreDial := sentryDialCheckTimeout
	sentryDialCheckTimeout = 500 * time.Millisecond
	defer func() { sentryDialCheckTimeout = restoreDial }()

	var warnBuf, infoBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)
	Info.SetOutput(&infoBuf)
	defer Info.SetOutput(os.Stdout)

	enabled := SetupSentry(testRelease)

	assert.True(t, enabled, "a stalled probe means degraded, never disabled — capture stays on")
	assert.Contains(t, warnBuf.String(), "startup probe did not flush",
		"a transport that cannot drain within the window must be loud at boot")
	assert.NotContains(t, infoBuf.String(), "Sentry error capture enabled",
		"the success line must be withheld when the probe could not drain")
	events := transport.Events()
	require.Len(t, events, 1,
		"the canary must reach the transport of the client SetupSentry just configured — the probe ran against THAT client, not some pre-bound stub")
	assert.Equal(t, "sentry startup probe", events[0].Message)
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

// TestFlushSentryOnShutdown_NoClient_WarnsAndSkips pins the guard's
// visibility: it only fires on programmer error (the call site must gate on
// SetupSentry), and silent misuse would mean shutdown flushes everyone assumes
// exist were never installed.
func TestFlushSentryOnShutdown_NoClient_WarnsAndSkips(t *testing.T) {
	require.Nil(t, sentry.CurrentHub().Client(), "test requires the disabled state")

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	FlushSentryOnShutdown()

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
