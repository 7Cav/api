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
//   - metricsMiddleware (placeholder, #130): Prometheus request counter and
//     latency histogram. BOTH labels must reach this OUTER layer via the
//     context label-holder mechanism — route label included: AuthMiddleware's
//     r.WithContext clones the request, so the mux sets Pattern on the inner
//     copy only and this layer's request keeps Pattern == "". See
//     metricsMiddleware below for the mechanism.
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
//
// rc is the tickets reference cache (status/priority/prefix names, the
// category tree) the tickets datastore methods consume — at cutover (#134)
// the caller passes the refreshed referencecache.Cache the old stack already
// maintains (servers.Start wires it today).
func New(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return sentryMiddleware(
		metricsMiddleware(
			AuthMiddleware(ds,
				GzipMiddleware(
					routes(ds, rc)))))
}

// routes builds the pattern-routing mux: one handle call per public route,
// each gated on its scope. Everything no route pattern matches falls through
// to fallback (the JSON 404, or the 405+Allow for a known path under an
// unsupported method).
func routes(ds datastores.Datastore, rc datastores.TicketReferenceCache) *http.ServeMux {
	mux := http.NewServeMux()

	// --- milpacs (scope: read) -------------------------------------------
	handle(mux, "GET /api/v1/milpacs/ranks", "read", getAllRanks(ds))

	// --- tickets (scope: read:tickets) -----------------------------------
	// The literal /categories segment wins over {ticket_id} (mux precedence,
	// golden-pinned by tickets/categories).
	handle(mux, "GET /api/v1/tickets", "read:tickets", listTickets(ds, rc))
	handle(mux, "GET /api/v1/tickets/categories", "read:tickets", listCategories(ds, rc))
	handle(mux, "GET /api/v1/tickets/{ticket_id}", "read:tickets", getTicketById(ds, rc))
	handle(mux, "GET /api/v1/tickets/ref/{ticket_ref}", "read:tickets", getTicketByRef(ds, rc))
	mux.Handle("GET /api/v1/tickets/{ticket_id}/{sub}", ticketSubResource(ds))

	mux.HandleFunc("/", fallback(mux))

	return mux
}

// ticketSubResource dispatches GET /api/v1/tickets/{ticket_id}/{sub} — the
// registration shape for the messages route. ServeMux cannot register
// {ticket_id}/messages directly: it conflicts with ref/{ticket_ref} (the two
// overlap at /tickets/ref/messages and neither is more specific, a
// registration panic). ref/{ticket_ref} IS more specific than
// {ticket_id}/{sub}, so registering the wildcard keeps the ref route winning
// all of /tickets/ref/* — matching the old gateway, where
// /tickets/ref/messages is a by-ref lookup of the ref "messages". This
// dispatcher then narrows the wildcard itself:
//
//   - sub == "messages" → the scope-gated messages handler (requireScope
//     applied HERE because handle() cannot register this route — the scope
//     gate stays explicit at the registration site);
//   - anything else → the JSON 404, scope-INDEPENDENT, exactly like the mux
//     fallback for paths no route pattern matches (the old stack 404s these
//     without consulting scopes either).
func ticketSubResource(ds datastores.Datastore) http.Handler {
	messages := requireScope("read:tickets", listTicketMessages(ds))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("sub") != "messages" {
			notFound(w, r)
			return
		}
		messages.ServeHTTP(w, r)
	})
}

// handle registers one public route: a method-qualified mux pattern, the
// route's required scope, and its handler. The scope is a required positional
// argument — gating by wrapping convention is how a scope check gets
// forgotten, and a forgotten check is invisible to every test that uses a
// fully-scoped key.
func handle(mux *http.ServeMux, pattern, scope string, h http.Handler) {
	mux.Handle(pattern, requireScope(scope, h))
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
// slice lands.
func sentryMiddleware(next http.Handler) http.Handler {
	return next
}

// metricsMiddleware is the documented extension point for #130 (Prometheus):
// request counter labeled route/method/status/key-id and duration histogram
// labeled route/method. Pass-through until that slice lands.
//
// Label plumbing (#130, precise mechanism): NEITHER label can be read off
// this layer's *http.Request after next.ServeHTTP returns. AuthMiddleware
// calls r.WithContext, which CLONES the request — the mux then sets Pattern
// on that inner clone, and the auth middleware attaches the key to the inner
// context — so the request this layer holds keeps Pattern == "" and carries
// no key. Both labels must instead travel through a context LABEL-HOLDER:
// this middleware puts a pointer to a mutable holder struct into the context
// before calling next (context values survive WithContext clones because the
// clone wraps the same parent context); AuthMiddleware fills the key-id slot,
// and the route slot is filled from r.Pattern inside the mux (e.g. by the
// wrapper handle registers), where the matched pattern is actually set.
func metricsMiddleware(next http.Handler) http.Handler {
	return next
}
