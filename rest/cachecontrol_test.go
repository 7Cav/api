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
//   - tickets routes: max-age=0 — the retired cache's middleware bypassed
//     the entire /api/v1/tickets prefix wholesale (middleware/cache.go @
//     92afba5^) — sensible, since tickets are a query-variant surface
//     (filters, cursors) a path-only key could not have cached correctly —
//     so tickets were NEVER cached, always served live. max-age=0 is the
//     honest signal for that (stale immediately).
//
// ERROR responses carry NO Cache-Control — also parity: the retired cache
// stored 200s only ("[CACHE] Non-200 response: %d, not caching"), so errors
// were always recomputed live on both surfaces.
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
// *last holds the header map AS OF COMMIT TIME — a recorder's live map keeps
// accepting writes after commit, but a real server drops a header stamped
// after the first body byte, so cloning the live map after ServeHTTP would
// pass a wire-invisible late stamp. commitSnapshot is the wire-faithful
// observer.
//
// The shared *http.Header pointer makes t.Parallel a silent cross-case
// bleed — replay loops over a captureHeader stack must stay sequential.
func captureHeader(h http.Handler, last *http.Header) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*last = nil
		h.ServeHTTP(&commitSnapshot{ResponseWriter: w, last: last}, r)
	})
}

// commitSnapshot clones the header map at commit time — the first
// WriteHeader, Write, or flush (FlushError, for symmetry with the production
// writers) — which is exactly when a real server snapshots headers onto the
// wire. Anything set afterwards is invisible to clients and must stay
// invisible to the battery.
type commitSnapshot struct {
	http.ResponseWriter
	last *http.Header
}

func (w *commitSnapshot) snap() {
	if *w.last == nil {
		*w.last = w.Header().Clone()
	}
}

func (w *commitSnapshot) WriteHeader(code int) {
	w.snap()
	w.ResponseWriter.WriteHeader(code)
}

func (w *commitSnapshot) Write(b []byte) (int, error) {
	w.snap()
	return w.ResponseWriter.Write(b)
}

func (w *commitSnapshot) FlushError() error {
	w.snap()
	return http.NewResponseController(w.ResponseWriter).Flush()
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
// they are implemented; no second hand-maintained CASE list (the group split
// in wantCacheControl is the one hand-maintained predicate, deliberately
// verbatim from the retired bypass).
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
	assert.Equal(t, "max-age=600", rr.Result().Header.Get("Cache-Control"),
		"roster-family 200s carry the retired cache's 10-minute freshness bound (ADR 0003 parity)")
}

// HEAD rides every GET pattern (read surface), and its 200 carries the same
// freshness signal — net/http suppresses the body, not the headers. Observed
// through a live httptest.Server like the HEAD body-suppression pin. One
// witness per route group: the mechanism is route-agnostic, but the witness
// is cheap and the groups carry different values.
func TestNewStack_HEADCarriesCacheControl(t *testing.T) {
	srv := httptest.NewServer(newStack(t))
	defer srv.Close()

	cases := []struct {
		name, path, key, want string
	}{
		{"roster_family", "/api/v1/milpacs/ranks", "cav7_readkey", "max-age=600"},
		{"tickets", "/api/v1/tickets/categories", "cav7_ticketskey", "max-age=0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodHead, srv.URL+tc.path, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+tc.key)
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer res.Body.Close()

			require.Equal(t, http.StatusOK, res.StatusCode)
			assert.Equal(t, tc.want, res.Header.Get("Cache-Control"))
		})
	}
}

// A gzipped 200 keeps the freshness signal: the cacheControl writer sits
// inside the gzip layer and stamps the shared header map before the first
// body byte commits the response.
func TestNewStack_GzippedResponseKeepsCacheControl(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))
	assert.Equal(t, "max-age=600", rr.Result().Header.Get("Cache-Control"))
}

// The non-route surfaces stay unsignaled — they are not read endpoints, and
// none of them is a 200: the clean-path 307 (answered in front of the mux),
// the wrong-method 405, and the {ticket_id}/{sub} dispatcher's 404 for an
// unknown sub-resource.
func TestNewStack_NonRouteSurfacesCarryNoCacheControl(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		name       string
		method     string
		path       string
		key        string
		wantStatus int
	}{
		{"clean_path_307", http.MethodGet, "/api/v1/milpacs/position/search/A//B", "cav7_readkey", http.StatusTemporaryRedirect},
		{"wrong_method_405", http.MethodPost, "/api/v1/milpacs/ranks", "cav7_readkey", http.StatusMethodNotAllowed},
		{"unknown_ticket_sub_404", http.MethodGet, "/api/v1/tickets/42/bogus", "cav7_ticketskey", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.key)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, tc.wantStatus, rr.Code)
			assert.Empty(t, rr.Header().Get("Cache-Control"))
		})
	}
}
