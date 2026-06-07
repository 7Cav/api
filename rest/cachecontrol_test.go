package rest_test

// Cache-Control freshness signal (#131): every read endpoint's 200 responses
// carry `Cache-Control: max-age=…` — the cooperative signal that replaces
// what the retired response cache's existence used to imply (PRD #112,
// Phase 2 deleted it). Values per route group (reviewed at PR time):
//
//   - roster-family routes (everything under scope "read"): max-age=600 —
//     parity with the retired cache's consumer-visible freshness bound: its
//     10-minute update_time poller (cache/manager.go @ 92afba5^, ADR 0003)
//     meant consumers already tolerated up-to-10-minute staleness there.
//   - tickets routes: max-age=0 — the old cache was keyed by path only, so
//     the per-user tickets surface was NEVER cached; always served live.
//     max-age=0 is the honest signal for that (stale immediately).
//
// ERROR responses carry NO Cache-Control — also parity: the retired cache
// stored 200s only ("Non-200 response: not caching"), so errors were always
// recomputed live on both surfaces.
//
// These pins are deliberately NEW-STACK-ONLY (same precedent as the 405 and
// %2F families): the golden corpus replays against the old stack too, and
// the old stack sends no Cache-Control — a recorded header would poison the
// old-stack suite (or, worse, pin the header's ABSENCE).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/contract"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureHeader wraps a stack so battery replays can observe response headers
// the contract harness deliberately filters out: contract.RunCase records only
// the contractHeaders allowlist (Cache-Control must stay OFF that list — the
// goldens replay against the old stack, which sends none). After each request
// *last holds the full header map of the response just served.
func captureHeader(h http.Handler, last *http.Header) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		*last = w.Header().Clone()
	})
}

// wantCacheControl is the expected freshness signal for one observed
// response: the route group's max-age on 200s, nothing anywhere else. The
// tickets predicate is verbatim the retired cache middleware's bypass
// (middleware/cache.go @ 92afba5^) — the exact line that made tickets
// never-cached is the line that now classifies them as always-live.
func wantCacheControl(status int, path string) string {
	if status != http.StatusOK {
		return ""
	}
	if path == "/api/v1/tickets" || strings.HasPrefix(path, "/api/v1/tickets/") {
		return "max-age=0"
	}
	return "max-age=600"
}

// TestNewStack_CacheControlAcrossBattery rides the full implemented battery
// (the golden layer's request set) and asserts the freshness signal on every
// observed response: 200s carry their route group's max-age, everything else
// — the 401 tiers, scope 403s, binding 400s, not-found 404s, injected-outage
// 500s — carries NO Cache-Control at all. Because the loop derives from
// implementedCases, a future route's battery cases are covered the moment
// they are implemented; no second hand-maintained list.
func TestNewStack_CacheControlAcrossBattery(t *testing.T) {
	var last http.Header
	h := captureHeader(newStack(t), &last)

	known := map[string]contract.Case{}
	for _, c := range contract.Cases() {
		known[c.Name] = c
	}

	sawGroup := map[string]bool{}
	for _, name := range implementedCases {
		c, ok := known[name]
		require.True(t, ok, "implementedCases entry %q names no battery case", name)
		t.Run(name, func(t *testing.T) {
			g, _, err := contract.RunCase(h, c)
			require.NoError(t, err)

			path := c.Path
			if i := strings.IndexByte(path, '?'); i >= 0 {
				path = path[:i]
			}
			want := wantCacheControl(g.Status, path)
			assert.Equal(t, want, last.Get("Cache-Control"),
				"status %d on %s", g.Status, c.Path)
			if g.Status == http.StatusOK {
				sawGroup[want] = true
			}
		})
	}

	// Non-vacuousness: the battery must witness a 200 in BOTH route groups —
	// otherwise the tickets value (or the roster one) passes by absence.
	assert.True(t, sawGroup["max-age=600"], "no roster-family 200 witnessed the 600 bound")
	assert.True(t, sawGroup["max-age=0"], "no tickets 200 witnessed the always-live signal")
}

func TestNewStack_RanksCarriesRosterFamilyCacheControl(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "max-age=600", rr.Header().Get("Cache-Control"),
		"roster-family 200s carry the retired cache's 10-minute freshness bound (ADR 0003 parity)")
}
