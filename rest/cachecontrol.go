package rest

import (
	"errors"
	"fmt"
	"net/http"
)

// Cache-Control freshness signal (#131): every read route's 200 responses
// carry `Cache-Control: max-age=…` — the cooperative signal that replaces
// what the retired response cache's existence used to imply (PRD #112;
// Phase 2 deleted the cache). With the cache gone this is how respectful
// consumers learn how fresh the data is and how often to poll, instead of
// guessing. ETag/conditional requests and rate limiting are explicitly
// deferred (the update_time-polling trick the old cache used is the natural
// backend for the ETag work when it comes).
//
// Per-route-group values, reviewed at PR time:
//
//   - maxAgeRosterFamily (600): parity reproduction, not a guess. The
//     retired response cache (cache/manager.go @ 92afba5^, ADR 0003) polled
//     information_schema.tables.update_time over its 9 monitored tables
//     every 10 minutes and flushed on any change, so consumers already
//     tolerated up-to-10-minute staleness on every roster-family route.
//   - maxAgeTickets (0): also parity, not a judgment about the data. The
//     retired cache's middleware bypassed the entire /api/v1/tickets prefix
//     wholesale (middleware/cache.go @ 92afba5^) — every tickets response
//     was served live. The middleware records no rationale for the bypass —
//     sensible, since tickets are a query-variant surface (filters, cursors)
//     a path-only key could not have cached correctly — but the bypass
//     itself is the verifiable fact. max-age=0 (stale immediately) is the
//     honest signal for never-cached; anything larger would invent a
//     freshness bound that never existed.
//
// The header goes on 200s ONLY — also parity: the retired cache stored 200s
// and nothing else ("[CACHE] Non-200 response: %d, not caching"), so error
// responses were always recomputed live on both surfaces. A cacheable 500
// or 404 would be a regression the old stack never had.
const (
	maxAgeRosterFamily = 600 // seconds; scope "read" — the surface the old cache covered
	maxAgeTickets      = 0   // seconds; scope "read:tickets" — the surface the old cache bypassed
)

// cacheControl wraps a route handler, stamping the route group's
// `Cache-Control: max-age=…` on its 200 responses. Non-200 responses pass
// through untouched (see the package constants above for why). It is a
// required argument of handle() — like the scope gate, a wrapping convention
// is how a route silently loses its freshness signal.
func cacheControl(maxAgeSeconds int, next http.Handler) http.Handler {
	if maxAgeSeconds < 0 {
		// Registration-time constant — a negative max-age is invalid on the
		// wire and always a programming error, so fail at startup.
		panic(fmt.Sprintf("cacheControl: negative max-age %d", maxAgeSeconds))
	}
	value := fmt.Sprintf("max-age=%d", maxAgeSeconds)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheControlWriter{ResponseWriter: w, value: value}, r)
	})
}

// cacheControlWriter injects the Cache-Control header at commit time — the
// first FINAL (non-1xx) WriteHeader, Write, or flush — and only when the
// response is a 200. A forwarded informational WriteHeader (1xx minus 101 —
// rationale on the informational predicate) commits nothing (#165): the
// stamp decision belongs to the final status that follows, exactly as
// net/http's own writer leaves that set uncommitted (101 latches, there and
// here).
// Commit time is the only safe moment: setting the header eagerly would leak
// it onto error responses written later (writeError never clears headers),
// and onto the contract 500 the sentry layer writes after a handler panic.
// On a 200 the Set is unconditional: a handler-set Cache-Control is
// discarded — the registration table is the single source of truth for
// freshness bounds. The stamped surface is exactly 200, not 2xx (the
// structural spec test covers every declared 2xx): a non-200 2xx route
// needs a deliberate extension of both this wrapper and that test.
type cacheControlWriter struct {
	http.ResponseWriter
	value     string
	committed bool
}

func (w *cacheControlWriter) WriteHeader(code int) {
	if informational(code) {
		// A non-latching 1xx (the predicate excludes 101) never commits —
		// forward and keep the stamp decision for the final status (#165;
		// rationale on the informational predicate).
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if !w.committed {
		w.committed = true
		if code == http.StatusOK {
			w.Header().Set("Cache-Control", w.value)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *cacheControlWriter) Write(b []byte) (int, error) {
	if !w.committed {
		// A Write without an explicit WriteHeader is the net/http implied
		// 200 — the writeJSON path every handler success takes.
		w.committed = true
		w.Header().Set("Cache-Control", w.value)
	}
	return w.ResponseWriter.Write(b)
}

// FlushError stamps the header on the implied 200 before delegating the
// flush — a flush before the first write commits the response, so without
// this the header would miss the wire and the later Write would stamp a dead
// map. Delegating through a fresh ResponseController keeps the downstream
// search semantics identical.
//
// If the delegated flush reports http.ErrNotSupported, the stamp this call
// made is rolled back. Leaving it would poison the live header map and lie
// committed=true: a handler reacting to the failed flush by writing an
// error would commit a non-200 carrying max-age, the exact leak class the
// type doc forbids. The rollback's premise — nothing of the final response
// reached the wire, so nothing committed — holds for wrappers that fail
// before writing anything of it:
// an unflushable wrapper ABOVE gzip, or a chain with no flushable bottom and
// no gzip in between. It is NOT universal: gzip's FlushError (rest/gzip.go)
// pushes its header and sync block downstream BEFORE the delegated flush can
// fail, so a future unflushable wrapper BELOW gzip would surface
// ErrNotSupported here after bytes had already latched a Write-committing
// base — a commit this rollback would wrongly undo. Unreachable today
// (everything below gzip is flushable); noted so such a wrapper isn't added
// casually. A genuine I/O error keeps the state: by then net/http has
// already snapshotted the headers onto the wire, so the commit really
// happened.
func (w *cacheControlWriter) FlushError() error {
	stamped := false
	if !w.committed {
		// A flush without an explicit WriteHeader commits the net/http
		// implied 200 — same decision as the Write path above.
		w.committed = true
		w.Header().Set("Cache-Control", w.value)
		stamped = true
	}
	err := http.NewResponseController(w.ResponseWriter).Flush()
	if stamped && errors.Is(err, http.ErrNotSupported) {
		w.Header().Del("Cache-Control")
		w.committed = false
	}
	return err
}

// Unwrap exposes the underlying writer to http.ResponseController so inner
// layers keep Flusher/Hijacker/deadline access through this wrapper. The
// model is commitWriter in sentry.go, not statusWriter: this wrapper has
// commit-time semantics, so Unwrap alone would open the tunnel's blind spot
// — FlushError above closes it.
func (w *cacheControlWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
