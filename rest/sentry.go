package rest

// First-class Sentry wiring for the new stack (#132, PRD #112): the
// permanent replacement for Phase 0's temporary wrapping of the old stack
// (servers/sentry.go + the gRPC interceptor + the gateway middleware), which
// the cutover slice (#134) deletes with the stacks it wraps. Three pieces,
// one file:
//
//   - SetupSentry — env-gated init (SENTRY_DSN, exactly as in Phase 0): no
//     DSN means nothing is initialised, and every capture point below is a
//     pass-through.
//   - sentryMiddleware — panic recovery at the FRONT of the chain (PRD order:
//     sentry → metrics → auth → gzip → mux). Sits OUTSIDE metrics, which
//     meters a panic as status="500" and re-raises — this layer catches the
//     re-panic, reports it, and writes the contract 500 so the request still
//     completes. sentryLabel (inside auth, outside gzip) fills the route and
//     key-id tags the recovery point cannot see.
//   - reportServerError — the 5xx report hook the writeStatusJSON choke
//     point calls (the error-writer behind writeError; methodNotAllowed
//     reaches it without writeError): one place, every error.
//
// What this file deliberately does NOT reproduce — servers/sentry.go's three
// boot-lifecycle pieces, which the cutover slice (#134) must port before
// deleting that file: sentryDialCheck (warn-only TCP reachability check of
// the DSN ingest host at boot), sentryStartupProbe (one canary event flushed
// through the real transport before any listener opens), and
// flushSentryOnShutdown (the SIGTERM/interrupt flush handler servers.Start
// installs after setupSentry). Dropping them silently would make a
// Sentry-down misconfig invisible at boot and lose every still-buffered
// event on SIGTERM.
//
// Tag discipline (PRD #112): events carry the validated key ID and the
// matched route pattern — bearer material NEVER (same rule as the metrics
// key_id label); scrubEvent strips request auth material at the BeforeSend
// choke point as belt-and-braces.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/spf13/viper"
)

// sentryTransport overrides the SDK's default HTTP transport. Always nil in
// production; swapped only by tests so the full SetupSentry path is
// exercisable without network (same seam idiom as write.go's marshalJSON).
// Caveat: a non-nil Transport puts the SDK on its legacy direct-send path,
// not production's async telemetry-processor pipeline — tests do not observe
// production queueing/drop semantics.
var sentryTransport sentry.Transport

// SetupSentry initialises Sentry error capture (errors only, no tracing) when
// SENTRY_DSN is present in the environment — the same gate as Phase 0's
// servers.setupSentry, which this replaces at cutover (#134). Without a DSN
// nothing is initialised — no client, no capture, local/dev unaffected; the
// only side effect is one Info line making the disabled state visible at
// boot. Returns whether capture was enabled.
//
// release is the build-time version (the -X servers.version injection today;
// the cutover slice passes it through) — it stamps every event's Release tag.
func SetupSentry(release string) bool {
	dsn := viper.GetString("SENTRY_DSN")
	if dsn == "" {
		Info.Println("Sentry disabled — SENTRY_DSN not set")
		return false
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn: dsn,
		// Release tagging from the build-time version (PRD #112).
		Release: release,
		// Errors only — no tracing (PRD #112).
		EnableTracing: false,
		// Explicit zero: the SDK normalises 0 to its default of 1.0, so
		// every error event is sent (confirmed convention in #113).
		SampleRate: 0,
		// String panics (panic("msg")) become message events; without this
		// they would arrive with no stack trace at all.
		AttachStacktrace: true,
		// Env-gated SDK diagnostics (SENTRY_DEBUG) — delivery failures are
		// otherwise invisible without a rebuild.
		Debug: sentryDebugEnabled(),
		// SDK debug lines land on the Warn logger's underlying writer with
		// the SDK's own "[Sentry] " prefix — NOT the app's "WARNING: "
		// prefix. Log scrapers keyed on our prefixes will not match them.
		DebugWriter: Warn.Writer(),
		// Belt-and-braces scrubbing at the choke point — see scrubEvent for
		// the exact (request-material-only) scope of the guarantee.
		BeforeSend: scrubEvent,
		// nil in production — tests swap the seam for a capture mock.
		Transport: sentryTransport,
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
		Info.Println("Sentry error capture enabled (errors only), release:", release,
			"— startup probe flushed (queue drained; delivery not verified — set SENTRY_DEBUG=true to confirm)")
	}
	return true
}

// sentryDebugEnabled reads SENTRY_DEBUG strictly (mirrors Phase 0, which this
// file replaces at cutover). viper.GetBool silently maps unparseable values
// ("yes", "on", typos) to false — a mid-incident operator trap: debug looks
// enabled but nothing logs. Unset stays silently off; a set but unparseable
// value warns and is treated as off.
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

// sentryLabels is the mutable per-request holder the sentry middleware shares
// with sentryLabel — the same channel-through-the-context mechanism as
// metricLabels (#130), and for the same reason: the recovery point is the
// OUTERMOST layer, where the request never carries the matched pattern (the
// mux sets it on auth's inner clone) or the validated key (attached to the
// inner context). It also carries the panic de-dup flag the writeStatusJSON
// choke point consults: the recovery's own contract-500 write must not turn
// one panic into a second (message) event.
type sentryLabels struct {
	hub      *sentry.Hub
	route    string // mux pattern, e.g. "GET /api/v1/milpacs/ranks"; "" if never routed
	keyID    string // decimal key id, e.g. "7"; "" if no key validated
	panicked bool   // a panic event was already captured for this request
}

// sentryLabelsContextKey is the private context key for *sentryLabels.
type sentryLabelsContextKey struct{}

// sentryLabelsFromContext returns the request's holder, or nil when the
// sentry middleware is a pass-through (no SENTRY_DSN — every consumer below
// then no-ops, the complete-no-op guarantee).
func sentryLabelsFromContext(ctx context.Context) *sentryLabels {
	v, _ := ctx.Value(sentryLabelsContextKey{}).(*sentryLabels)
	return v
}

// sentryMiddleware is the panic-recovery layer at the FRONT of the chain (PRD
// order: sentry → metrics → auth → gzip → mux). It sits OUTSIDE metrics by
// ruling: the metrics middleware meters a panic as status="500" (unwritten
// case) and RE-RAISES — this layer catches that re-panic with metering
// already done, reports the event (tagged via the sentryLabels holder
// sentryLabel filled on the way out), and writes the contract 500 error shape
// so the request still completes.
//
// Without an initialised client (no SENTRY_DSN) it is a complete no-op:
// requests flow unchanged and panics propagate exactly as they do today
// (net/http recovers them per-connection) — Phase 0 parity.
//
// A response already committed when the panic arrives (a handler that wrote
// before panicking, or any gzipped request — the gzip layer's deferred Close
// emits stream bytes during unwind) cannot be turned into a 500: the event is
// still captured, then the panic re-raises so net/http aborts the connection
// and the client sees the truncation instead of trusting a half response.
func sentryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sentry.CurrentHub().Client() == nil {
			next.ServeHTTP(w, r)
			return
		}

		hub := sentry.CurrentHub().Clone()
		hub.Scope().SetTag("transport", "http") // Phase 0 dashboard continuity
		labels := &sentryLabels{hub: hub}
		r = r.WithContext(context.WithValue(r.Context(), sentryLabelsContextKey{}, labels))
		cw := &commitWriter{ResponseWriter: w}

		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler { // raw comparison — net/http's own idiom
				// The stdlib's DELIBERATE-abort sentinel (net/http suppresses
				// its stack; e.g. httputil.ReverseProxy panics with it on
				// client disconnects): not an error, no event, no 500
				// rewrite. Re-raise for net/http to honour.
				panic(rec)
			}
			// Log FIRST — the recovery swallows what net/http would have
			// logged, and the SDK capture below runs synchronously (stack
			// symbolisation reads source files, BeforeSend runs) and could
			// itself fail or panic: the original panic and stack must already
			// be in the process logs before any of that runs.
			Error.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
			labels.panicked = true
			scope := hub.Scope()
			if labels.route != "" {
				scope.SetTag("route", labels.route)
			}
			if labels.keyID != "" {
				scope.SetTag("key_id", labels.keyID)
			}
			hub.RecoverWithContext(r.Context(), rec) // event queued; ID unused — async transport
			if cw.committed {
				// Status already on the wire — see the doc comment. net/http
				// logs this re-raise a second time: accepted duplication.
				panic(rec)
			}
			writeError(cw, r, codeInternal, "Internal Server Error")
		}()

		next.ServeHTTP(cw, r)
	})
}

// sentryLabel fills the sentryLabels holder with the matched route pattern
// and the validated key id — the recovery point's only channel to them. It
// mounts INSIDE auth (the key is on this request's context) and OUTSIDE the
// mux, deliberately on the same *http.Request pointer the mux dispatches:
// ServeMux assigns r.Pattern in place before calling the handler, so the
// DEFERRED read observes it even while a handler panic unwinds — exactly the
// path the recovery layer needs it on. The companion of routeLabel (#130),
// which fills the metrics holder the same way one layer further in.
func sentryLabel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		labels := sentryLabelsFromContext(r.Context())
		if labels == nil { // sentry disabled — complete no-op
			next.ServeHTTP(w, r)
			return
		}
		defer func() {
			labels.route = r.Pattern
			if key := KeyFromContext(r.Context()); key != nil {
				// The id, never the bearer token (same rule as metrics).
				labels.keyID = strconv.FormatUint(uint64(key.KeyId), 10)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// reportServerError is the 5xx report hook the writeStatusJSON choke point
// calls for every status ≥ 500 — one place, every error (#132). r is the
// request the FAILING LAYER holds: inside the mux it carries the matched
// pattern and the validated key (both tagged; the id, never the bearer
// token); on the pre-routing tiers (auth's datastore-outage 503) both are
// absent and the tags are simply omitted — same semantics as the empty
// metrics labels.
//
// No-op when the request never passed an enabled sentry middleware: no
// SENTRY_DSN (the complete-no-op guarantee), or a chain that does not mount
// it. Also a no-op
// for the recovery layer's own contract-500 write after a panic: that event
// is already captured, and one failure must not become two issues.
func reportServerError(r *http.Request, status int) {
	labels := sentryLabelsFromContext(r.Context())
	if labels == nil || labels.panicked {
		return
	}
	labels.hub.WithScope(func(scope *sentry.Scope) {
		if r.Pattern != "" {
			scope.SetTag("route", r.Pattern)
		}
		if key := KeyFromContext(r.Context()); key != nil {
			scope.SetTag("key_id", strconv.FormatUint(uint64(key.KeyId), 10))
		}
		scope.SetTag("http_status", strconv.Itoa(status))
		scope.SetLevel(sentry.LevelError)
		// Group by method+status, not path (Phase 0 idiom, kept verbatim so
		// Sentry issues survive the cutover): parameterized paths must not
		// fan one failure into N issues. The pattern stays on the route tag.
		scope.SetFingerprint([]string{"http-5xx", r.Method, strconv.Itoa(status)})
		labels.hub.CaptureMessage(fmt.Sprintf("HTTP %d %s %s", status, r.Method, r.URL.Path)) // event queued; ID unused — async transport
	})
}

// commitWriter tracks whether the FINAL response started on the wire, so the
// panic recovery knows whether the contract 500 can still be written. A
// forwarded informational WriteHeader (1xx minus 101 — rationale on the
// informational predicate) latches nothing (#165): those precede the final
// response and leave it rewritable, exactly as net/http's own writer treats
// them (101 is the exception: the stdlib commits on it, and so does this
// latch). Created by
// the OUTERMOST middleware but the INNERMOST wrapper in write delegation —
// writes run gzipWriter → statusWriter → commitWriter → the server's writer
// (metrics builds its statusWriter around this one). Unwrap keeps
// http.ResponseController tunnelling through it (same obligation as the
// metrics statusWriter that wraps it). FlushError closes the tunnel's blind
// spot: without it ResponseController.Flush would reach the base writer via
// Unwrap WITHOUT setting committed, and a flush-then-panic would write a
// contract 500 over a 200 already on the wire.
type commitWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *commitWriter) WriteHeader(code int) {
	if informational(code) {
		// A non-latching 1xx (the predicate excludes 101) never commits —
		// forward and keep the latch for the final status (#165; rationale
		// on the informational predicate): an informational response leaves
		// the wire rewritable, so the recovery can still honestly write the
		// contract 500.
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *commitWriter) Write(b []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(b)
}

// FlushError marks the response committed before delegating the flush — see
// the type doc for the blind spot this closes. Delegating through a fresh
// ResponseController keeps the downstream search semantics identical
// (http.ErrNotSupported surfaces naturally when nothing below can flush).
//
// If the delegated flush reports http.ErrNotSupported, no layer below could
// flush — nothing of the final response reached the wire — and the latch this
// call set is rolled back (#164; mirror of cacheControlWriter's rollback):
// leaving it would lie committed=true on a wire the final response never
// touched, sending a later handler panic down the
// re-panic path (connection abort) instead of the contract 500 the recovery
// can still honestly write. The latch only rolls back when this call was the
// first to set it — after a prior Write or a prior latching WriteHeader (a
// final status, or the 101 carve-out — see the informational predicate) the
// commit already happened and the state keeps: bytes or the final status
// line are genuinely out, or the stdlib latched on the 101 itself. (A
// forwarded non-latching 1xx sets no latch at all (#165); a 101 latches like
// a final status, so a flush after it correctly finds the latch already set
// and never rolls back.) A genuine I/O error also
// keeps it: by then the delegate really flushed, so the commit happened.
func (w *commitWriter) FlushError() error {
	latched := !w.committed
	w.committed = true
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if latched && errors.Is(err, http.ErrNotSupported) {
		w.committed = false
	}
	return err
}

func (w *commitWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// scrubEvent is the BeforeSend hook closing the request-material vector: it
// strips authorization / proxy-authorization / cookie headers
// (case-insensitive), the cookie jar, and the raw query string from every
// outgoing error event (mirrors Phase 0's servers.scrubEvent, which this file
// replaces at cutover). It does NOT scan exception text, breadcrumbs or
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
