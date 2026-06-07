package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
)

// RoutesForTest builds the bare route mux (no middleware) and returns it
// together with every pattern registered through handle() — the scope-gated
// registration table. The scope-loop completeness guard (#128 round 3,
// ruling 2; positions_test.go) derives its expected route set from this table
// and probes the mux for what its path list actually witnesses, so the guard
// cannot rot into a second hand-maintained list.
//
// Deliberately ABSENT from the table: the direct mux.Handle registrations —
// the refMessagesParity shim (scope-independent by ruling), the
// {ticket_id}/{sub} dispatcher (scope gate applied inside, see
// ticketSubResource) and the catch-all. They are not handle()-gated and carry
// their own pinned coverage.
func RoutesForTest(ds datastores.Datastore, rc datastores.TicketReferenceCache) (*http.ServeMux, []string) {
	var patterns []string
	onHandle = func(pattern string) { patterns = append(patterns, pattern) }
	defer func() { onHandle = nil }()
	mux := routes(ds, rc)
	return mux, patterns
}
