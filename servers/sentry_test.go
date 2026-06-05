package servers

import (
	"bytes"
	"context"
	"encoding/json"
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

// stubTransport is an in-memory sentry.Transport with a controllable Flush
// outcome — the seam for probing the delivery-failure warning paths.
type stubTransport struct {
	mu      sync.Mutex
	events  []*sentry.Event
	flushOK bool
}

func (t *stubTransport) Configure(sentry.ClientOptions)        {}
func (t *stubTransport) Flush(time.Duration) bool              { return t.flushOK }
func (t *stubTransport) FlushWithContext(context.Context) bool { return t.flushOK }
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
func bindStubClient(t *testing.T, flushOK bool) *stubTransport {
	t.Helper()
	transport := &stubTransport{flushOK: flushOK}
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

	enabled := setupSentry()

	assert.False(t, enabled, "a malformed DSN must disable capture, never take the API down")
	assert.Nil(t, sentry.CurrentHub().Client(), "no client may be bound after a failed init")
}

func TestSetupSentry_WithDSN_InitialisesErrorsOnlyClientWithRelease(t *testing.T) {
	viper.Set("SENTRY_DSN", "https://public@example.ingest.sentry.io/1")
	viper.Set("SENTRY_DEBUG", true)
	defer func() {
		viper.Set("SENTRY_DSN", "")
		viper.Set("SENTRY_DEBUG", false)
		sentry.CurrentHub().BindClient(nil)
	}()

	// Shrink the startup-probe flush window: the example DSN is not routable,
	// so the probe's real-transport flush must not stall the suite for 5s.
	restore := sentryStartupProbeTimeout
	sentryStartupProbeTimeout = 50 * time.Millisecond
	defer func() { sentryStartupProbeTimeout = restore }()

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
	assert.NotNil(t, opts.BeforeSend, "scrubber must be wired so bearer material can never leave the process")
}

func TestSentryStartupProbe_FlushTimeout_WarnsLoudly(t *testing.T) {
	bindStubClient(t, false)

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	ok := sentryStartupProbe()

	assert.False(t, ok)
	assert.Contains(t, warnBuf.String(), "startup probe did not flush",
		"a swallowed delivery failure is the exact silence this canary exists to break")
}

func TestSentryStartupProbe_Delivered_NoWarning(t *testing.T) {
	transport := bindStubClient(t, true)

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	ok := sentryStartupProbe()

	assert.True(t, ok)
	assert.Empty(t, warnBuf.String(), "a healthy probe must not warn")
	events := transport.Events()
	require.Len(t, events, 1, "the probe must push exactly one canary event through the transport")
	assert.Equal(t, "sentry startup probe", events[0].Message)
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
				assert.Contains(t, warnBuf.String(), "sentry flush timed out at shutdown",
					"silently dropped events at shutdown must leave a trace in the logs")
			} else {
				assert.NotContains(t, warnBuf.String(), "flush timed out", "a clean flush must not warn")
			}
		})
	}
}
