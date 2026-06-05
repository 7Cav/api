package servers

import (
	"encoding/json"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupSentry_NoDSN_Disabled(t *testing.T) {
	viper.Set("SENTRY_DSN", "")
	defer viper.Set("SENTRY_DSN", "")

	enabled := setupSentry()

	assert.False(t, enabled, "setupSentry must be a no-op when SENTRY_DSN is unset")
	assert.Nil(t, sentry.CurrentHub().Client(), "no client may be bound without a DSN")
}

func TestSetupSentry_WithDSN_InitialisesErrorsOnlyClientWithRelease(t *testing.T) {
	viper.Set("SENTRY_DSN", "https://public@example.ingest.sentry.io/1")
	defer func() {
		viper.Set("SENTRY_DSN", "")
		sentry.CurrentHub().BindClient(nil)
	}()

	enabled := setupSentry()

	assert.True(t, enabled)
	client := sentry.CurrentHub().Client()
	require.NotNil(t, client, "a client must be bound when SENTRY_DSN is set")
	assert.Equal(t, version, client.Options().Release, "release must reuse the build-time version injection")
	assert.False(t, client.Options().EnableTracing, "errors only — no tracing")
	assert.NotNil(t, client.Options().BeforeSend, "scrubber must be wired so bearer material can never leave the process")
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
		Cookies: "session=abc123",
	}

	scrubbed := scrubEvent(event, nil)

	require.NotNil(t, scrubbed, "scrubbing must sanitise, never drop, the event")
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
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	var order []string

	go watchShutdown(signals,
		func() { order = append(order, "flush") },
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
}
