package rest

import (
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
//   - maxAgeTickets (0): the old cache was keyed by path only, so the
//     per-user tickets surface was NEVER cached — always served live.
//     max-age=0 (stale immediately) is the honest signal for per-user data;
//     anything larger would invent a freshness bound that never existed.
//
// The header goes on 200s ONLY — also parity: the retired cache stored 200s
// and nothing else ("Non-200 response: not caching"), so error responses
// were always recomputed live on both surfaces. A cacheable 500 or 404
// would be a regression the old stack never had.
const (
	maxAgeRosterFamily = 600 // seconds; scope "read" — the surface the old cache covered
	maxAgeTickets      = 0   // seconds; scope "read:tickets" — never cached, per-user data
)

// cacheControl wraps a route handler, stamping the route group's
// `Cache-Control: max-age=…` on its 200 responses. Non-200 responses pass
// through untouched (see the package constants above for why). It is a
// required argument of handle() — like the scope gate, a wrapping convention
// is how a route silently loses its freshness signal.
func cacheControl(maxAgeSeconds int, next http.Handler) http.Handler {
	value := fmt.Sprintf("max-age=%d", maxAgeSeconds)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheControlWriter{ResponseWriter: w, value: value}, r)
	})
}

// cacheControlWriter injects the Cache-Control header at commit time — the
// first WriteHeader or Write call — and only when the response is a 200.
// Commit time is the only safe moment: setting the header eagerly would leak
// it onto error responses written later (writeError never clears headers),
// and onto the contract 500 the sentry layer writes after a handler panic.
type cacheControlWriter struct {
	http.ResponseWriter
	value     string
	committed bool
}

func (w *cacheControlWriter) WriteHeader(code int) {
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

// Unwrap exposes the underlying writer to http.ResponseController so inner
// layers keep Flusher/Hijacker/deadline access through this wrapper (same
// rule as statusWriter in metrics.go).
func (w *cacheControlWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
