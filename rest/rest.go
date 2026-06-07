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
//     Outside auth, so rejected requests are counted. The route and key_id
//     labels reach this OUTER layer via the context label-holder
//     (metricLabels): AuthMiddleware fills the key-id slot, the routeLabel
//     wrapper inside the mux fills the route slot from r.Pattern. The
//     exposition is never served through this chain; the cutover slice
//     (#134) mounts MetricsHandler on its own INTERNAL-ONLY listener — until
//     then it is test-mounted only.
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
//     the frozen message string on failure. Query parameters go through
//     newQueryBinder: bind EVERY field first, then check b.Err() exactly
//     once and 400 its text verbatim — a forgotten Err() check silently
//     drops a frozen 400. Routes whose old generated handler called
//     req.ParseForm() parse strictly via bindListQuery instead of
//     r.URL.Query().
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
	"strings"

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
// maintains (servers.Start wires it today). It must be WARMED (Refresh run
// and the poller keeping it fresh), not merely non-nil: a cold cache
// degrades silently — empty categories, blank status/priority/prefix names,
// and category filters collapsing from subtree to exact-match. New refuses
// nil outright: with no recovery middleware in the chain, a nil cache is a
// guaranteed panic on the first tickets request against the real datastore.
func New(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	if rc == nil {
		panic("rest.New: nil TicketReferenceCache — pass the refreshed referencecache.Cache (see #134)")
	}
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
	handle(mux, "GET /api/v1/milpacs/profile/id/{user_id}", "read", getProfileByID(ds))
	handle(mux, "GET /api/v1/milpacs/profile/username/{username}", "read", getProfileByUsername(ds))
	// Historical path prefix: singular "milpac" on the connected-account
	// lookups, plural "milpacs" everywhere else. Frozen by the corpus.
	handle(mux, "GET /api/v1/milpac/discord/{discord_id}", "read", getProfileByDiscordID(ds))
	handle(mux, "GET /api/v1/milpac/gamertag/{gamertag}", "read", getProfileByGamertag(ds))
	// Roster routes (#127): one member set, three profile shapes. {roster}
	// binds the RosterType enum by name OR number (see types.ParseRosterType).
	handle(mux, "GET /api/v1/roster/{roster}", "read", getRoster(ds))
	handle(mux, "GET /api/v1/roster/{roster}/lite", "read", getLiteRoster(ds))
	handle(mux, "GET /api/v1/s1/uniforms/{roster}", "read", getS1UniformsRoster(ds))

	// --- tickets (scope: read:tickets) -----------------------------------
	// The literal /categories segment wins over {ticket_id} (mux precedence,
	// golden-pinned by tickets/categories).
	handle(mux, "GET /api/v1/tickets", "read:tickets", listTickets(ds, rc))
	handle(mux, "GET /api/v1/tickets/categories", "read:tickets", listCategories(ds, rc))
	handle(mux, "GET /api/v1/tickets/{ticket_id}", "read:tickets", getTicketById(ds, rc))
	handle(mux, "GET /api/v1/tickets/ref/{ticket_ref}", "read:tickets", getTicketByRef(ds, rc))
	// Exact pattern beats ref/{ticket_ref} in ServeMux precedence — see
	// refMessagesParity for why this path is a frozen 400, not a by-ref
	// lookup. Deliberately NOT scope-gated: the old 400 fired in the gateway
	// before the RPC, so RequireScope never ran.
	mux.Handle("GET /api/v1/tickets/ref/messages", refMessagesParity())
	mux.Handle(ticketSubPattern, ticketSubResource(ds))

	// The catch-all is route-labeled like every registered pattern: 404s and
	// 405s meter under its "/" pattern — bounded, and distinct from "" (a
	// request auth rejected before routing ever happened).
	mux.Handle("/", routeLabel(fallback(mux)))

	return mux
}

// ticketSubPattern is the registration shape for the messages route — shared
// with fallback, whose 405 probe must apply the same sub == "messages"
// narrowing this dispatcher does.
const ticketSubPattern = "GET /api/v1/tickets/{ticket_id}/{sub}"

// ticketSubResource dispatches GET /api/v1/tickets/{ticket_id}/{sub} — the
// registration shape for the messages route. ServeMux cannot register
// {ticket_id}/messages directly: it conflicts with ref/{ticket_ref} (the two
// overlap at /tickets/ref/messages and neither is more specific, a
// registration panic). ref/{ticket_ref} IS more specific than
// {ticket_id}/{sub}, so the wildcard alone would hand /tickets/ref/messages
// to the by-ref route — the OPPOSITE of the old gateway, whose prepend-order
// matching tried {ticket_id}/messages BEFORE ref/{ticket_ref} and emitted the
// frozen parse 400 (see refMessagesParity; the explicit literal registration
// in routes() restores that). This dispatcher then narrows the wildcard
// itself:
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
		if !knownTicketSub(r.PathValue("sub")) {
			notFound(w, r)
			return
		}
		messages.ServeHTTP(w, r)
	})
}

// knownTicketSub is the {ticket_id}/{sub} wildcard's narrowing — "messages"
// is the only real sub-resource. ONE definition shared by the dispatcher and
// the fallback's 405 probe: if they disagreed, a wrong-method request could
// 405 ("route exists") on a path whose GET is a 404.
func knownTicketSub(sub string) bool { return sub == "messages" }

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
// are covered with no per-route bookkeeping — EXCEPT the {ticket_id}/{sub}
// wildcard, which over-matches by construction: its dispatcher 404s every
// sub but "messages", so a probe hit on that pattern counts as a known route
// only under the same narrowing (knownTicketSub).
func fallback(mux *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			probe := r.Clone(r.Context())
			probe.Method = http.MethodGet
			if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
				if pattern != ticketSubPattern || knownTicketSub(lastSegment(probe.URL.Path)) {
					methodNotAllowed(w, r)
					return
				}
			}
		}
		notFound(w, r)
	}
}

// lastSegment returns the path's final segment — the {sub} binding of a
// ticketSubPattern match (mux.Handler reports the pattern but binds no path
// values on the probe).
func lastSegment(path string) string {
	return path[strings.LastIndexByte(path, '/')+1:]
}

// sentryMiddleware is the documented extension point for #132 (full Sentry
// wiring): panic-recovery middleware at the front of the chain, with 5xx
// reports emitted from the writeError choke point. Pass-through until that
// slice lands. Note for #132: metricsMiddleware already meters panics as
// status="500" and re-raises — recovery must stay OUTSIDE metrics: a recovery
// layer inside it that swallowed a panic without writing a response would
// meter as the implied 200, flattening error rates.
func sentryMiddleware(next http.Handler) http.Handler {
	return next
}
