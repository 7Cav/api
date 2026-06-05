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
		return false
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn: dsn,
		// Release reuses the build-time version injection
		// (-X github.com/7cav/api/servers.version=<tag>).
		Release: version,
		// Errors only — no tracing (PRD #112 Phase 0).
		EnableTracing: false,
		// SampleRate left at zero so the SDK applies its default of 1.0
		// (every error event is sent — confirmed convention in #113).
		// Belt-and-braces: our interceptor/middleware never attach bearer
		// material, but scrub at the choke point so nothing future-added
		// can leak it either.
		BeforeSend: scrubEvent,
	})
	if err != nil {
		// Telemetry must never take the API down: log and run without it.
		Warn.Printf("sentry init failed — continuing without error capture: %v", err)
		return false
	}

	Info.Println("Sentry error capture enabled (errors only), release:", version)
	return true
}

// flushSentryOnShutdown installs a signal handler that flushes buffered
// Sentry events before the process exits. The current stack has no graceful
// shutdown path (Start blocks on Serve and the process dies by signal); this
// is the minimal hook so error events from the final moments are not lost.
// Only installed when Sentry is enabled, so the no-DSN path keeps today's
// default signal behavior exactly.
func flushSentryOnShutdown() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go watchShutdown(signals,
		func() { sentry.Flush(sentryShutdownFlushTimeout) },
		os.Exit,
	)
}

// watchShutdown waits for a shutdown signal, flushes, then exits 0. Split
// from flushSentryOnShutdown so the flush-before-exit ordering is testable.
func watchShutdown(signals <-chan os.Signal, flush func(), exit func(code int)) {
	sig := <-signals
	Info.Printf("received %v — flushing sentry before exit", sig)
	flush()
	exit(0)
}

// scrubEvent is the BeforeSend hook: it strips credential-bearing request
// material (Authorization headers, cookies) from every outgoing event so a
// bearer token can never appear in a Sentry payload.
func scrubEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event.Request == nil {
		return event
	}
	event.Request.Cookies = ""
	for name := range event.Request.Headers {
		switch strings.ToLower(name) {
		case "authorization", "cookie", "proxy-authorization":
			delete(event.Request.Headers, name)
		}
	}
	return event
}
