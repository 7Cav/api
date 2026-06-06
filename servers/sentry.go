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
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
)

// sentryShutdownFlushTimeout bounds how long shutdown waits for buffered
// events to reach Sentry before the process exits.
const sentryShutdownFlushTimeout = 2 * time.Second

// sentryStartupProbeTimeout bounds the boot-time drain check. A var, not a
// const, only so tests can shrink the window; production never mutates it.
var sentryStartupProbeTimeout = 5 * time.Second

// sentryDialCheckTimeout bounds the boot-time TCP reachability pre-check of
// the DSN ingest host — generous enough for a cold DNS resolve plus a
// cross-region handshake, small enough to keep the worst-case boot delay
// acceptable (it stacks with the probe window before any listener opens). A
// var, not a const, only so tests can shrink the window; production never
// mutates it.
var sentryDialCheckTimeout = 3 * time.Second

// setupSentry initialises Sentry error capture (errors only, no tracing) when
// SENTRY_DSN is present in the environment. Without a DSN nothing is
// initialised — no client, no capture, local/dev unaffected; the only side
// effect is one Info line making the disabled state visible at boot. Returns
// whether capture was enabled.
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
		// they would arrive with no stack trace at all. Side effect: HTTP 5xx
		// CaptureMessage events also gain a (middleware-frame) Threads stack —
		// grouping is unaffected because they set an explicit fingerprint.
		AttachStacktrace: true,
		// Env-gated SDK diagnostics: send failures, drops and rate limits
		// otherwise go to a debug logger defaulting to io.Discard. One log
		// line per event when on — too chatty for always-on, but lets ops
		// diagnose delivery without a rebuild.
		Debug: sentryDebugEnabled(),
		// SDK debug lines land on the Warn logger's underlying writer with
		// the SDK's own "[Sentry] " prefix — NOT the app's "WARNING: "
		// prefix. Log scrapers keyed on our prefixes will not match them.
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

	// Warn-only reachability pre-check: catches the misconfig class the probe
	// below structurally cannot (fast send failures drain the queue and so
	// still "flush"). Never changes the enabled/degraded semantics.
	sentryDialCheck(dsn)

	// Probe failure still returns true — enabled-degraded, not disabled: a
	// slow-network false positive must not turn off capture. Do NOT refactor
	// this into `return sentryStartupProbe()`.
	if sentryStartupProbe() {
		Info.Println("Sentry error capture enabled (errors only), release:", version,
			"— startup probe flushed (queue drained; delivery not verified — set SENTRY_DEBUG=true to confirm)")
	}
	return true
}

// sentryDebugEnabled reads SENTRY_DEBUG strictly. viper.GetBool silently maps
// unparseable values ("yes", "on", typos) to false — a mid-incident operator
// trap: debug looks enabled but nothing logs. Unset stays silently off; a set
// but unparseable value warns and is treated as off.
func sentryDebugEnabled() bool {
	raw := viper.GetString("SENTRY_DEBUG")
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		Warn.Printf("SENTRY_DEBUG=%q is not a boolean (use \"true\" or \"false\") — treating as off", raw)
		return false
	}
	return enabled
}

// sentryDialCheck is a boot-time, warn-only TCP reachability check of the DSN
// ingest host. It exists because the startup probe cannot see fast send
// failures — DNS errors and refused connections drain the transport queue in
// milliseconds, so the probe's flush still reports success (see
// sentryStartupProbe). A failed dial here names the unreachable host while
// the probe would stay silent. Warn-only by design: the network may heal, and
// a boot-time blip must not disable capture.
func sentryDialCheck(rawDSN string) {
	dsn, err := sentry.NewDsn(rawDSN)
	if err != nil {
		// Unreachable in practice — sentry.Init already parsed this DSN.
		return
	}
	addr := net.JoinHostPort(dsn.GetHost(), strconv.Itoa(dsn.GetPort()))
	conn, err := net.DialTimeout("tcp", addr, sentryDialCheckTimeout)
	if err != nil {
		Warn.Printf("sentry DSN host %s is unreachable (%v) — error events will not be delivered; set SENTRY_DEBUG=true for per-event transport diagnostics", addr, err)
		return
	}
	_ = conn.Close()
}

// sentryStartupProbe pushes one canary event through the real transport and
// flushes. sentry.Init does no network I/O, so without this the pipeline is
// first exercised by the first real error.
//
// What a true return PROVES — per the SDK's documented Flush contract (queue
// drained, NOT delivered): the transport finished its send attempts within
// the window. That catches hang-class failures (blackholed egress, connects
// slower than the window) and guarantees one event exercised the full
// pipeline so SENTRY_DEBUG has something to report. What it does NOT prove:
// delivery. The transport worker dequeues on ANY send outcome, so DNS
// failures, refused connections, Sentry-side rejections (bad DSN key → 4xx)
// and rate-limit drops all complete in milliseconds, drain the queue, and
// "flush" successfully — those classes are visible only with
// SENTRY_DEBUG=true (and partially via sentryDialCheck).
func sentryStartupProbe() bool {
	// Event hygiene: a fixed fingerprint + info level fold every container
	// restart into one low-severity Sentry issue instead of resolve→reopen
	// churn; the probe tag makes canaries filterable. Cloned hub so none of
	// this leaks into the global scope.
	var id *sentry.EventID
	hub := sentry.CurrentHub().Clone()
	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetFingerprint([]string{"sentry-startup-probe"})
		scope.SetTag("probe", "true")
		scope.SetLevel(sentry.LevelInfo)
		id = hub.CaptureMessage("sentry startup probe")
	})
	if id == nil {
		// Nil event ID = the client dropped the event before the transport
		// saw it (e.g. a BeforeSend veto) — nothing queued, so a "successful"
		// flush below would be vacuous.
		Warn.Println("sentry startup probe was dropped client-side — no canary reached the transport (check BeforeSend/sampling)")
		return false
	}
	if !hub.Flush(sentryStartupProbeTimeout) {
		Warn.Println("sentry enabled but startup probe did not flush — transport still busy after the window; events may not be reaching Sentry (check DSN/egress, or set SENTRY_DEBUG=true)")
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
		// Only reachable on programmer error — the call site gates on
		// setupSentry(). Loud so misuse never silently skips the handler.
		Warn.Println("flushSentryOnShutdown called without an initialised sentry client — shutdown flush handler not installed")
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
		// "Remaining", not "all": events sent before the window closed made
		// it out — only what was still buffered is gone.
		Warn.Println("sentry shutdown flush window expired — remaining buffered events lost")
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
