package rest

// Sentry boot-lifecycle wiring for the new stack: the two pieces that survive
// from Phase 0's servers/sentry.go after the single-listener cutover (#134) and
// #259. SetupSentry (sentry.go) calls sentryDialCheck at boot; the composition
// root installs FlushSentryOnShutdown when capture is enabled.
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

// sentryDialCheckTimeout bounds the boot-time TCP reachability pre-check of
// the DSN ingest host. Generous enough for a cold DNS resolve plus a
// cross-region handshake, small enough to keep the worst-case boot delay
// acceptable. This is the only network wait before any listener opens. A
// var, not a const, only so tests can shrink the window; production never
// mutates it.
var sentryDialCheckTimeout = 3 * time.Second

// sentryDialCheck is a boot-time, warn-only TCP reachability check of the DSN
// ingest host. sentry.Init does no network I/O, so without this check a wrong
// host, a DNS typo or a closed egress path stays invisible until the first
// real error fails to send. A failed dial names the unreachable host in the
// log before any listener opens. Warn-only by design: the network may heal,
// and a boot-time blip must not disable capture.
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
