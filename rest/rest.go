// Package rest is the stdlib net/http stack that replaces the gRPC server +
// grpc-gateway pair (PRD #112, Phase 3). It is test-mounted only until the
// cutover slice (#134) — nothing routes production traffic here yet — but it
// is the permanent home: at cutover the single public listener serves every
// route through this package, and Phase 4 deletes the old stacks.
//
// # Middleware chain (PRD order — assembled in New)
//
//	sentry → metrics → auth → gzip → mux
//
// Extension points, outermost first:
//
//   - sentryMiddleware (placeholder, #132): panic-recovery at the front of
//     the chain; 5xx Sentry reports hook the writeError choke point.
//   - metricsMiddleware (placeholder, #130): Prometheus request counter and
//     latency histogram. Route label comes from r.Pattern (the mux's matched
//     pattern); key-id reaches this OUTER layer via a context label-holder
//     the auth middleware fills.
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
//  3. Register in routes(): mux.Handle("GET /api/v1/...",
//     requireScope("<scope>", handler)). Path parameters via r.PathValue.
//  4. Spec operation block in openapi/openapi.yaml (CI-enforced two-way
//     coverage, contract/spec_test.go).
//  5. Goldens green: add the route's battery case names to implementedCases
//     in rest_test.go — the replay harness does the rest.
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

// routes builds the pattern-routing mux: one Handle call per public route,
// each wrapped with its scope requirement. Unknown paths fall through to the
// JSON 404.
func routes(ds datastores.Datastore) *http.ServeMux {
	mux := http.NewServeMux()

	// --- milpacs (scope: read) -------------------------------------------
	mux.Handle("GET /api/v1/milpacs/ranks", requireScope("read", getAllRanks(ds)))

	// Catch-all: unknown paths under the API prefix get the JSON 404 body
	// (golden-pinned), not the stdlib text 404.
	mux.HandleFunc("/", notFound)

	return mux
}

// sentryMiddleware is the documented extension point for #132 (full Sentry
// wiring): panic-recovery middleware at the front of the chain, with 5xx
// reports emitted from the writeError choke point. Pass-through until that
// slice lands.
func sentryMiddleware(next http.Handler) http.Handler {
	return next
}

// metricsMiddleware is the documented extension point for #130 (Prometheus):
// request counter labeled route/method/status/key-id and duration histogram
// labeled route/method. Route label from r.Pattern after the mux matches;
// key-id via a context label-holder filled by AuthMiddleware. Pass-through
// until that slice lands.
func metricsMiddleware(next http.Handler) http.Handler {
	return next
}
