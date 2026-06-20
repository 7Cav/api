package rest

// Sentry boot-lifecycle wiring for the new stack: the three pieces ported from
// Phase 0's servers/sentry.go at the single-listener cutover (#134). SetupSentry
// (sentry.go) calls sentryDialCheck and sentryStartupProbe at boot; the
// composition root installs FlushSentryOnShutdown when capture is enabled.
//
// Kept in their own file so the always-on init (sentry.go) and the boot/shutdown
// lifecycle stay legible apart; they share the package's sentryTransport test
// seam and sentryDebugEnabled.

import (
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
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

// FlushSentryOnShutdown installs a signal handler that flushes buffered
// Sentry events before the process exits. The stack has no graceful shutdown
// path (Start blocks on Serve and the process dies by signal); this is the
// minimal hook so error events from the final moments are not lost. The
// composition root calls it only when SetupSentry returned true, so the no-DSN
// path keeps the default signal behavior exactly — guarded here as well as at
// the call site, because installing the handler without a client would
// silently rewrite the process's signal semantics for nothing.
func FlushSentryOnShutdown() {
	if sentry.CurrentHub().Client() == nil {
		// Only reachable on programmer error — the call site gates on
		// SetupSentry. Loud so misuse never silently skips the handler.
		Warn.Println("FlushSentryOnShutdown called without an initialised sentry client — shutdown flush handler not installed")
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
// from FlushSentryOnShutdown so the flush-before-exit ordering is testable.
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
