// Package rest is the stdlib net/http stack that replaced the gRPC server +
// grpc-gateway pair (PRD #112, Phase 3); the #134 cutover removed those and
// made this the single public production listener. The single public listener
// serves every route through New/routes.
//
// Most of this package is HTTP plumbing the gateway used to provide for free:
// gzip (gzip.go), query binding (query.go), the error-code→HTTP-status mapping
// (write.go), clean-path redirects (redirect.go), client-IP resolution
// (clientip.go), Cache-Control grouping (cachecontrol.go) and Sentry
// (sentry.go, sentry_boot.go). The request-handling business logic is the
// per-resource files — milpacs.go, rosters.go, positions.go, awol.go,
// tickets.go — and that is where new work usually lands.
//
// # Middleware chain (PRD order — assembled in chain, New's composition)
//
//	sentry → metrics → auth (→ sentryLabel) → gzip → clean-path 307 → mux
//
// Extension points, outermost first:
//
//   - sentryMiddleware (#132, sentry.go): panic recovery at the front of the
//     chain — catches the metrics layer's re-panic, reports the event, and
//     writes the contract 500; 5xx Sentry reports hook the writeError choke
//     point. Env-gated by SENTRY_DSN (rest.SetupSentry): without a client it
//     is a complete no-op. Its route/key-id tags reach this OUTER layer via
//     the sentryLabels context holder, filled by the sentryLabel wrapper
//     inside auth (the same mechanism as metricLabels below).
//   - metricsMiddleware (#130, metrics.go): Prometheus request counter
//     (route/method/status/key_id) and latency histogram (route/method).
//     Outside auth, so rejected requests are counted. The route and key_id
//     labels reach this OUTER layer via the context label-holder
//     (metricLabels): AuthMiddleware fills the key-id slot, the routeLabel
//     wrapper inside the mux fills the route slot from r.Pattern. The
//     exposition is never served through this chain; MetricsHandler is served
//     on its own INTERNAL-ONLY listener (:9090, servers/server.go).
//   - AuthMiddleware: bearer-key validation with the golden-pinned two-tier
//     plain-text 401s. Runs BEFORE routing, so an unknown path without
//     credentials is a 401, not a 404 (golden-pinned). Scope checks are
//     per-route, not here — see requireScope.
//   - GzipMiddleware: response compression. Inside auth (401s are never
//     gzipped), outside the mux (every routed response, including the JSON
//     404, compresses).
//   - cleanPathRedirect (redirect.go): the mux's clean-path 307 answered in
//     front of the mux with the contract JSON body and the bounded catch-all
//     metering label (ruled, #128 round 3 — enumerated deliberate break).
//     Inside gzip, so the redirect body compresses like every routed
//     response; clean paths pass through untouched.
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
//     <max-age>, handler) — the scope gate and the route group's
//     Cache-Control max-age (#131, cachecontrol.go) are required arguments,
//     not wrapping conventions. Path parameters via r.PathValue.
//     Wrong-method and unknown paths are already covered by the mux fallback
//     (405+Allow / JSON 404).
//  4. Spec operation block in openapi/openapi.yaml (CI-enforced two-way
//     coverage, contract/spec_test.go) — its 200 response must declare the
//     Cache-Control const matching the registered max-age (structural guard
//     in contract/spec_test.go, observed-equals-declared in
//     rest/spec_test.go).
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
// chain (sentry → metrics → auth (→ sentryLabel) → gzip → clean-path 307 →
// mux; the one definition lives on chain below). The returned handler serves
// the /api surface; non-API paths (the docs UI) are served by rest.DocsHandler
// on the same listener (composed in servers.servPublic).
//
// rc is the tickets reference cache (status/priority/prefix names, the
// category tree) the tickets datastore methods consume — at cutover (#134)
// the caller passes the refreshed referencecache.Cache the old stack already
// maintains (servers.Start wires it today). It must be WARMED (Refresh run
// and the poller keeping it fresh), not merely non-nil: a cold cache
// degrades silently — empty categories, blank status/priority/prefix names,
// and category filters collapsing from subtree to exact-match. New refuses
// nil outright: a nil cache is a guaranteed panic on the first tickets
// request against the real datastore (recovery exists only when SENTRY_DSN
// is set — and a panic-per-request service is broken either way).
func New(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	if rc == nil {
		panic("rest.New: nil TicketReferenceCache — pass the refreshed referencecache.Cache (see #134)")
	}
	return chain(ds, routes(ds, rc))
}

// chain wraps inner — the route mux, in production — in the PRD middleware
// order: sentry → metrics → auth (→ sentryLabel) → gzip → clean-path 307 →
// inner. ONE definition, shared by New and the composed flush-chain pin
// (flushchain_internal_test.go), so the assembly that test exercises IS the
// assembly production serves: a wrapper inserted here is exercised by the
// composition test by construction, and the test cannot rot into a
// hand-maintained mirror of an assembly that moved on (#174 review).
func chain(ds datastores.Datastore, inner http.Handler) http.Handler {
	return sentryMiddleware(
		metricsMiddleware(
			AuthMiddleware(ds,
				sentryLabel(
					GzipMiddleware(
						cleanPathRedirect(inner))))))
}

// routes builds the pattern-routing mux. Every route registers through
// handle() — scope-gated, cache-controlled — except the three handleRaw call
// sites: the ref/messages parity shim and the catch-all (ungated by ruling)
// and the {ticket_id}/{sub} dispatcher (gates inside itself). Everything no
// route pattern matches falls through to fallback (the JSON 404, or the
// 405+Allow for a known path under an unsupported method).
func routes(ds datastores.Datastore, rc datastores.TicketReferenceCache) *http.ServeMux {
	mux := http.NewServeMux()

	// --- milpacs (scope: read, max-age 600) --------------------------------
	handle(mux, "GET /api/v1/milpacs/ranks", "read", maxAgeRosterFamily, getAllRanks(ds))
	handle(mux, "GET /api/v1/milpacs/position/groups", "read", maxAgeRosterFamily, getPositionGroups(ds))
	handle(mux, "GET /api/v1/milpacs/awol", "read", maxAgeRosterFamily, getAwol(ds))
	// The "..." wildcard is the legacy gateway's {position_query=**} glob:
	// multi-segment queries and the bare trailing-slash form (empty query,
	// handler 400) both route here.
	handle(mux, "GET /api/v1/milpacs/position/search/{position_query...}", "read", maxAgeRosterFamily, searchByPosition(ds))
	// The slashless form, explicitly: the gateway's ** matched ZERO segments
	// (httprule OpPushM), so the old stack answered the handler's empty-query
	// 400 here — without this registration the mux would 307-redirect to the
	// canonical /search/ instead, a redirect the old stack never sent.
	handle(mux, "GET /api/v1/milpacs/position/search", "read", maxAgeRosterFamily, searchByPosition(ds))
	handle(mux, "GET /api/v1/milpacs/profile/id/{user_id}", "read", maxAgeRosterFamily, getProfileByID(ds))
	handle(mux, "GET /api/v1/milpacs/profile/username/{username}", "read", maxAgeRosterFamily, getProfileByUsername(ds))
	// Historical path prefix: singular "milpac" on the connected-account
	// lookups, plural "milpacs" everywhere else. Frozen by the corpus.
	handle(mux, "GET /api/v1/milpac/discord/{discord_id}", "read", maxAgeRosterFamily, getProfileByDiscordID(ds))
	handle(mux, "GET /api/v1/milpac/gamertag/{gamertag}", "read", maxAgeRosterFamily, getProfileByGamertag(ds))
	// Roster routes (#127): one member set, three profile shapes. {roster}
	// binds the RosterType enum by name OR number (see types.ParseRosterType).
	handle(mux, "GET /api/v1/roster/{roster}", "read", maxAgeRosterFamily, getRoster(ds))
	handle(mux, "GET /api/v1/roster/{roster}/lite", "read", maxAgeRosterFamily, getLiteRoster(ds))
	handle(mux, "GET /api/v1/s1/uniforms/{roster}", "read", maxAgeRosterFamily, getS1UniformsRoster(ds))

	// --- forum (scope: read, max-age 600) ----------------------------------
	// The forum permission-group directory (xf_user_group), distinct from the
	// NF Rosters position groups at /api/v1/milpacs/position/groups (ADR 0007).
	// Same read scope and read-family freshness bound as the milpac surface.
	handle(mux, "GET /api/v1/forum/groups", "read", maxAgeRosterFamily, getForumGroups(ds))

	// --- tickets (scope: read:tickets, max-age 0 — never cached, live) -----
	// The literal /categories segment wins over {ticket_id} (mux precedence,
	// golden-pinned by tickets/categories).
	handle(mux, "GET /api/v1/tickets", "read:tickets", maxAgeTickets, listTickets(ds, rc))
	handle(mux, "GET /api/v1/tickets/categories", "read:tickets", maxAgeTickets, listCategories(ds, rc))
	handle(mux, "GET /api/v1/tickets/{ticket_id}", "read:tickets", maxAgeTickets, getTicketById(ds, rc))
	handle(mux, "GET /api/v1/tickets/ref/{ticket_ref}", "read:tickets", maxAgeTickets, getTicketByRef(ds, rc))
	// Exact pattern beats ref/{ticket_ref} in ServeMux precedence — see
	// refMessagesParity for why this path is a frozen 400, not a by-ref
	// lookup. Deliberately NOT scope-gated: the old 400 fired in the gateway
	// before the RPC, so RequireScope never ran. No cacheControl wrap either:
	// the shim answers nothing but the frozen 400, and only 200s carry the
	// freshness signal. handleRaw applies routeLabel: the frozen 400 routed,
	// so it meters under this literal pattern, never route="" (#166).
	handleRaw(mux, "GET /api/v1/tickets/ref/messages", refMessagesParity())
	// handleRaw puts routeLabel OUTSIDE the dispatcher, mirroring handle()'s
	// wrap order: everything the {ticket_id}/{sub} registration answers — the
	// messages 200s, the scope 403s, the unknown-sub 404s — meters under its
	// pattern.
	handleRaw(mux, ticketSubPattern, ticketSubResource(ds))

	// The catch-all is route-labeled like every registered pattern: 404s and
	// 405s meter under its "/" pattern — bounded, and distinct from "" (a
	// request that never reached routing).
	handleRaw(mux, "/", fallback(mux))

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
//   - sub == "messages" → the scope-gated, freshness-signaled messages
//     handler (requireScope AND cacheControl applied HERE because handle()
//     cannot register this route — those two wraps stay explicit at the
//     registration site; the third per-route wrap, routeLabel, comes from
//     handleRaw at the registration in routes(), same as every route);
//   - anything else → the JSON 404, scope-INDEPENDENT, exactly like the mux
//     fallback for paths no route pattern matches (the old stack 404s these
//     without consulting scopes either).
func ticketSubResource(ds datastores.Datastore) http.Handler {
	// requireScope and cacheControl applied HERE because handle() cannot
	// register this route — the two wraps stay explicit at the registration
	// site. handleRaw's wrap, routeLabel, wraps this dispatcher at its
	// registration in routes() — OUTSIDE the scope gate, the same order
	// handle() applies, so even a 403 meters under this route (#166).
	messages := requireScope("read:tickets", cacheControl(maxAgeTickets, listTicketMessages(ds)))
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
// route's required scope, the route group's Cache-Control max-age (seconds —
// see cachecontrol.go for the reviewed per-group values), and its handler.
// Scope and max-age are required positional arguments — gating by wrapping
// convention is how a scope check (or a route's freshness signal) gets
// forgotten, and a forgotten check is invisible to every test that uses a
// fully-scoped key. routeLabel wraps OUTSIDE the scope gate so even a 403
// meters under the route it was denied on; cacheControl wraps INSIDE it so
// the freshness signal belongs to the route handler's own responses (it
// stamps 200s only, so the order is semantics-neutral — this one just reads
// truest).
func handle(mux *http.ServeMux, pattern, scope string, maxAgeSeconds int, h http.Handler) {
	handleRaw(mux, pattern, requireScope(scope, cacheControl(maxAgeSeconds, h)))
}

// handleRaw is the ONLY way a handler reaches the mux — handle() funnels
// through it, and the registrations handle() cannot express (the ref/messages
// parity shim, the {ticket_id}/{sub} dispatcher, the catch-all) call it
// directly. It carries the two invariants EVERY registration must carry,
// scope-gated or not:
//
//   - routeLabel, so route="" keeps meaning exactly one thing — the request
//     never reached routing (#166). A bare mux.Handle would regress that
//     silently: traffic meters under the empty label and no test that uses a
//     labeled route child notices.
//   - the onHandle feed, so the registration table the completeness guards
//     derive from (RoutesForTest) sees every registration — including the
//     route="" sweep in metrics_test.go, which drives traffic at every table
//     pattern (#173).
//
// Adding a route? Use handle(). Only a route whose scope gate cannot be
// expressed as one requireScope wrap belongs here — and then the per-route
// wraps it skips (scope, cache-control) must be applied explicitly inside the
// handler where they apply (the way ticketSubResource does), or their absence
// ruled and documented (the parity shim and the catch-all carry neither, by
// ruling).
func handleRaw(mux *http.ServeMux, pattern string, h http.Handler) {
	if onHandle != nil {
		onHandle(pattern)
	}
	mux.Handle(pattern, routeLabel(h))
}

// onHandle observes each registration (every handle() and handleRaw call).
// Nil in production; swapped only by tests (RoutesForTest, export_test.go) so
// registration-completeness guards — e.g. the scope-403 loop coverage guard
// (#128 round 3, ruling 2) and the route="" sweep (#173) — derive their
// expected route sets from the REAL registration table instead of a second
// hand-maintained list that could rot alongside the first.
var onHandle func(pattern string)

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
