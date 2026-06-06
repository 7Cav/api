// Package rest is the stdlib net/http stack that replaces the gRPC server +
// grpc-gateway pair (PRD #112, Phase 3). Two of its layers ALREADY serve
// production: the legacy gateway delegates auth and gzip to
// rest.AuthMiddleware/rest.GzipMiddleware (single source, so the stacks
// cannot diverge while both are in-tree). The handler stack itself
// (New/routes) is test-mounted only until the cutover slice (#134); this
// package is the permanent home — at cutover the single public listener
// serves every route through it, and Phase 4 deletes the old stacks.
//
// # Middleware chain (PRD order — assembled in New)
//
//	sentry → metrics → auth → gzip → mux
//
// Extension points, outermost first:
//
//   - sentryMiddleware (placeholder, #132): panic-recovery at the front of
//     the chain; 5xx Sentry reports hook the writeError choke point.
//   - metricsMiddleware (#130, metrics.go): Prometheus request counter
//     (route/method/status/key_id) and latency histogram (route/method).
//     Outside auth, so rejected requests are counted. Both labels reach this
//     OUTER layer via the context label-holder (metricLabels): AuthMiddleware
//     fills the key-id slot, the routeLabel wrapper inside the mux fills the
//     route slot from r.Pattern. The exposition is served by MetricsHandler
//     on the INTERNAL-ONLY listener, never through this chain.
//   - AuthMiddleware: bearer-key validation with the golden-pinned two-tier
//     plain-text 401s. Runs BEFORE routing, so an unknown path without
//     credentials is a 401, not a 404 (golden-pinned). Scope checks are
//     per-route, not here — see requireScope.
//   - GzipMiddleware: response compression. Inside auth (401s are never
//     gzipped), outside the mux (every routed response, including the JSON
//     404, compresses).
//   - mux: the Go 1.22+ pattern-routing http.ServeMux.
//
// # Adding a route (the fan-out recipe, #126–#129)
//
//  1. Wire types in types/ (follow the conventions in the package doc).
//  2. Handler in this package: map the datastore result to the wire types
//     (allocate empty collections!), writeJSON on success, writeError with
//     the frozen message string on failure.
//  3. Register in routes(): handle(mux, "GET /api/v1/...", "<scope>",
//     handler) — the scope gate is a required argument, not a wrapping
//     convention. Path parameters via r.PathValue. Wrong-method and unknown
//     paths are already covered by the mux fallback (405+Allow / JSON 404).
//  4. Spec operation block in openapi/openapi.yaml (CI-enforced two-way
//     coverage, contract/spec_test.go).
//  5. Goldens green: add the route's battery case names to implementedCases
//     in rest_test.go — the replay harness does the rest.
//  6. Classify every new case's request path in specRoutes
//     (rest/spec_test.go) so the new-stack spec validation covers the
//     route's observed responses — an unclassified path fails that suite,
//     it never skips.
package rest

import (
	"log"
	"net/http"
	"os"

	"github.com/7cav/api/datastores"
)

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

// New assembles the new stack: the route mux wrapped in the PRD middleware
// chain (sentry → metrics → auth → gzip → mux). The returned handler serves
// the /api surface; non-API paths (the docs UI) are the cutover slice's
// concern (#134) and 404 here until then.
func New(ds datastores.Datastore) http.Handler {
	return sentryMiddleware(
		metricsMiddleware(
			AuthMiddleware(ds,
				GzipMiddleware(
					routes(ds)))))
}

// routes builds the pattern-routing mux: one handle call per public route,
// each gated on its scope. Everything no route pattern matches falls through
// to fallback (the JSON 404, or the 405+Allow for a known path under an
// unsupported method).
func routes(ds datastores.Datastore) *http.ServeMux {
	mux := http.NewServeMux()

	// --- milpacs (scope: read) -------------------------------------------
	handle(mux, "GET /api/v1/milpacs/ranks", "read", getAllRanks(ds))

	// The catch-all is route-labeled like every registered pattern: 404s and
	// 405s meter under its "/" pattern — bounded, and distinct from "" (a
	// request auth rejected before routing ever happened).
	mux.Handle("/", routeLabel(fallback(mux)))

	return mux
}

// handle registers one public route: a method-qualified mux pattern, the
// route's required scope, and its handler. The scope is a required positional
// argument — gating by wrapping convention is how a scope check gets
// forgotten, and a forgotten check is invisible to every test that uses a
// fully-scoped key. routeLabel wraps OUTSIDE the scope gate so even a 403
// meters under the route it was denied on.
func handle(mux *http.ServeMux, pattern, scope string, h http.Handler) {
	mux.Handle(pattern, routeLabel(requireScope(scope, h)))
}

// fallback serves every request no route pattern matched, splitting two
// surfaces the catch-all would otherwise conflate:
//
//   - a KNOWN path under an unsupported method → 405 + Allow (enumerated
//     break, ruled at #125: the old stack answered 501; 404 — the un-pinned
//     behavior before this — would discard the "route exists" signal). The
//     mux cannot produce this itself: its built-in 405 only fires when NO
//     pattern matches, and the "/" catch-all always matches.
//   - a genuinely unknown path → the JSON 404 body (golden-pinned).
//
// The split re-asks the mux whether the same path matches under GET (the
// read surface is GET/HEAD-only; HEAD matches GET patterns and never lands
// here). A probe pattern of "/" is the catch-all matching itself — an
// unknown path. Pattern-based, so parameterized fan-out routes (#126–#129)
// are covered with no per-route bookkeeping.
func fallback(mux *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			probe := r.Clone(r.Context())
			probe.Method = http.MethodGet
			if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
				methodNotAllowed(w, r)
				return
			}
		}
		notFound(w, r)
	}
}

// sentryMiddleware is the documented extension point for #132 (full Sentry
// wiring): panic-recovery middleware at the front of the chain, with 5xx
// reports emitted from the writeError choke point. Pass-through until that
// slice lands. Note for #132: metricsMiddleware already meters panics as
// status="500" and re-raises — recovery must stay OUTSIDE metrics or panicked
// requests vanish from the counters.
func sentryMiddleware(next http.Handler) http.Handler {
	return next
}

// metricsMiddleware lives in metrics.go (#130): Prometheus request counter
// labeled route/method/status/key_id and duration histogram labeled
// route/method, plumbed through the context label-holder (metricLabels).
