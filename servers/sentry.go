/*
 *  Copyright (C) 2021 7Cav.us
 *  This file is part of 7Cav-API <https://github.com/7cav/api>.
 *
 *  7Cav-API is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  7Cav-API is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with 7Cav-API. If not, see <http://www.gnu.org/licenses/>.
 */

package servers

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
)

// sentryShutdownFlushTimeout bounds how long shutdown waits for buffered
// events to reach Sentry before the process exits.
const sentryShutdownFlushTimeout = 2 * time.Second

// sentryStartupProbeTimeout bounds the boot-time delivery check. A var, not a
// const, only so tests can shrink the window; production never mutates it.
var sentryStartupProbeTimeout = 5 * time.Second

// setupSentry initialises Sentry error capture (errors only, no tracing) when
// SENTRY_DSN is present in the environment. Without a DSN it is a complete
// no-op: no init, no capture, local/dev unaffected. Returns whether capture
// was enabled.
//
// Temporary Phase 0 scaffolding (PRD #112): the rewritten stack gets its own
// first-class wiring in Phase 3.
func setupSentry() bool {
	dsn := viper.GetString("SENTRY_DSN")
	if dsn == "" {
		Info.Println("Sentry disabled — SENTRY_DSN not set")
		return false
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn: dsn,
		// Release reuses the build-time version injection
		// (-X github.com/7cav/api/servers.version=<tag>).
		Release: version,
		// Errors only — no tracing (PRD #112 Phase 0).
		EnableTracing: false,
		// Explicit zero: the SDK normalises 0 to its default of 1.0, so
		// every error event is sent (confirmed convention in #113).
		SampleRate: 0,
		// String panics (panic("msg")) become message events; without this
		// they would arrive with no stack trace at all.
		AttachStacktrace: true,
		// Env-gated SDK diagnostics: send failures, drops and rate limits
		// otherwise go to a debug logger defaulting to io.Discard. One log
		// line per event when on — too chatty for always-on, but lets ops
		// diagnose delivery without a rebuild.
		Debug:       viper.GetBool("SENTRY_DEBUG"),
		DebugWriter: Warn.Writer(),
		// Belt-and-braces scrubbing at the choke point — see scrubEvent for
		// the exact (request-material-only) scope of the guarantee.
		BeforeSend: scrubEvent,
	})
	if err != nil {
		// Telemetry must never take the API down: log and run without it.
		Warn.Printf("sentry init failed — continuing without error capture: %v", err)
		return false
	}

	if sentryStartupProbe() {
		Info.Println("Sentry error capture enabled (errors only), release:", version)
	}
	return true
}

// sentryStartupProbe pushes one canary event through the real transport and
// flushes. sentry.Init does no network I/O, and the SDK reports delivery
// failures (bad DSN host, blocked egress, rate limits) only to its internal
// debug logger — without this probe a broken pipeline looks exactly like a
// healthy one. Returns whether the flush confirmed the handoff.
func sentryStartupProbe() bool {
	sentry.CaptureMessage("sentry startup probe")
	if !sentry.Flush(sentryStartupProbeTimeout) {
		Warn.Println("sentry enabled but startup probe did not flush — events may not be reaching Sentry (check DSN/egress)")
		return false
	}
	return true
}

// flushSentryOnShutdown installs a signal handler that flushes buffered
// Sentry events before the process exits. The current stack has no graceful
// shutdown path (Start blocks on Serve and the process dies by signal); this
// is the minimal hook so error events from the final moments are not lost.
// Only installed when Sentry is enabled, so the no-DSN path keeps today's
// default signal behavior exactly — guarded here as well as at the call site,
// because installing the handler without a client would silently rewrite the
// process's signal semantics for nothing.
func flushSentryOnShutdown() {
	if sentry.CurrentHub().Client() == nil {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go watchShutdown(signals,
		func() bool { return sentry.Flush(sentryShutdownFlushTimeout) },
		os.Exit,
	)
}

// watchShutdown waits for a shutdown signal, flushes, then exits 0. Split
// from flushSentryOnShutdown so the flush-before-exit ordering is testable.
// Note on os.Exit: deferred functions do not run, and a second signal
// arriving during the flush window is absorbed by the still-installed
// handler — the process is already on its way down.
func watchShutdown(signals <-chan os.Signal, flush func() bool, exit func(code int)) {
	sig := <-signals
	Info.Printf("received %v — flushing sentry before exit", sig)
	if !flush() {
		Warn.Println("sentry flush timed out at shutdown — buffered events were dropped")
	}
	exit(0)
}

// scrubEvent is the BeforeSend hook closing the request-material vector: it
// strips authorization / proxy-authorization / cookie headers
// (case-insensitive), the cookie jar, and the raw query string from every
// outgoing error event. It does NOT scan exception text, breadcrumbs or
// extras — never put key material in error strings. If tracing is ever
// enabled, transactions bypass BeforeSend and need an equivalent
// BeforeSendTransaction hook.
func scrubEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event.Request == nil {
		return event
	}
	event.Request.Cookies = ""
	event.Request.QueryString = ""
	for name := range event.Request.Headers {
		switch strings.ToLower(name) {
		case "authorization", "cookie", "proxy-authorization":
			delete(event.Request.Headers, name)
		}
	}
	return event
}
