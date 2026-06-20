package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// getPositionGroups serves GET /api/v1/milpacs/position/groups: the position
// reference tree (groups → positions), golden-pinned by
// milpacs/position_groups. A near-clone of the ranks route (getAllRanks).
// Error message string frozen from the old handler (servers/grpc
// GetPositionGroups).
func getPositionGroups(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groups, err := ds.FindAllPositionGroups()
		if err != nil {
			writeError(w, r, codeInternal, "error fetching position groups: %v", err)
			return
		}
		writeJSON(w, r, types.PositionGroupsResponse{Groups: groups})
	})
}

// searchByPosition serves GET /api/v1/milpacs/position/search/{position_query...}:
// lite profiles whose position titles match the query (SQL LIKE substring,
// datastore-side). The legacy route was the gateway's multi-segment glob
// {position_query=**}; the ServeMux "..." wildcard reproduces it — slashes
// inside the query reach the handler (golden position/search_multi_segment),
// and the bare trailing-slash form binds the empty query the handler rejects
// (golden position/search_trailing_slash, message frozen from the old
// handler, servers/grpc SearchByPosition). The slashless /position/search
// form is registered separately (see routes()) and binds the same empty
// query — the gateway's ** matched zero segments.
//
// DELIBERATE BREAK (PRD #112, issue #128): the query reaches the handler
// STANDARD-decoded (r.PathValue — net/http's per-segment unescaping), not
// through the legacy gateway's own ** percent-decoding. The recorded forms
// (%20 → space) decode identically; only exotic encodings diverge. PATH
// CLEANING is the same break's second face (ruled, #128 round 3): the
// ServeMux cleans paths before matching, so an UNCLEAN query form the old
// glob served as a 200 (A//B, A/../B) is now a 307 to the cleaned path —
// answered in front of the mux with the contract JSON body by
// cleanPathRedirect (redirect.go); it never reaches this handler.
//
// Empty-result behavior is FROZEN AS-IS: plausible queries with no rows are
// {"profiles":{}} with 200, never 404 (golden position/search_empty_result;
// whether that's a bug is #137, outside this program).
func searchByPosition(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.PathValue("position_query")
		if query == "" {
			// The ** glob matched the empty segment and the old handler
			// rejected the proto zero value. Message frozen.
			writeError(w, r, codeInvalidArgument, "position query cannot be empty")
			return
		}
		roster, err := ds.FindProfilesByPosition(query)
		if err != nil {
			writeError(w, r, codeInternal, "error searching profiles by position: %v", err)
			return
		}
		if roster == nil {
			// Datastore invariant violated (non-nil roster on nil error —
			// datastores.Mysql always allocates): unreachable through the real
			// datastore, guarded so a future bug is a clean 500, not a
			// fabricated empty 200.
			writeError(w, r, codeInternal, "datastore returned no roster")
			return
		}
		writeJSON(w, r, roster)
	})
}
