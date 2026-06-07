package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
)

// RoutesForTest builds the bare route mux (no middleware) and returns it
// together with the COMPLETE registration table: every pattern registered
// through handle() AND the direct handleRaw registrations handle() cannot
// express — the refMessagesParity shim, the {ticket_id}/{sub} dispatcher,
// the catch-all (#173; before that the table deliberately excluded them, so
// the completeness machinery never saw a direct registration). Guards derive
// their expected route sets from this table instead of a second
// hand-maintained list that could rot alongside the first: the scope-loop
// completeness guard (#128 round 3, ruling 2; positions_test.go) filters it
// to the position/awol family, and the route="" sweep (#173;
// metrics_test.go) drives traffic at every pattern in it.
func RoutesForTest(ds datastores.Datastore, rc datastores.TicketReferenceCache) (*http.ServeMux, []string) {
	var patterns []string
	onHandle = func(pattern string) { patterns = append(patterns, pattern) }
	defer func() { onHandle = nil }()
	mux := routes(ds, rc)
	return mux, patterns
}
