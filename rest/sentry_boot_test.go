package rest

import (
	"bytes"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupSentry_BootSendsNoEventAndWarnsOnUnreachableHost pins the boot
// contract after #259: with a DSN, SetupSentry initialises the client, runs
// the warn-only dial check, prints the enabled line, and hands the transport
// nothing. Sentry receives no event at boot.
//
// The emptiness check is authoritative, not a race won. With a custom
// Transport the SDK skips its async telemetry processor and hands events to
// the transport on the capturing goroutine (sentry-go client.go, processEvent),
// so a re-added boot event would sit in Events() before SetupSentry returns.
//
// 127.0.0.1:1 refuses instantly, so the dial check resolves no name and opens
// no outbound connection. The test shrinks the dial window anyway so a
// pathological environment cannot stall the suite.
func TestSetupSentry_BootSendsNoEventAndWarnsOnUnreachableHost(t *testing.T) {
	transport := &transportMock{}
	sentryTransport = transport
	viper.Set("SENTRY_DSN", "http://public@127.0.0.1:1/1")
	t.Cleanup(func() {
		sentry.CurrentHub().BindClient(nil)
		sentryTransport = nil
		viper.Set("SENTRY_DSN", "")
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

	assert.True(t, enabled, "a refused ingest host means degraded, never disabled. Capture stays on")
	assert.Empty(t, transport.Events(),
		"boot must hand the transport nothing. Sentry sees no event until the first real error")
	assert.Contains(t, infoBuf.String(), "Sentry error capture enabled",
		"the enabled line prints whenever init succeeded, whatever the dial check found")
	assert.Contains(t, warnBuf.String(), "127.0.0.1:1",
		"SetupSentry must still run the dial check, and the warning must name the unreachable host")
}

func TestSentryDialCheck_UnreachableHost_WarnsWithHost(t *testing.T) {
	// 127.0.0.1:1 refuses instantly. It is the deterministic stand-in for the
	// wrong-DSN-host misconfig class. Shrink the dial window anyway so a
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
