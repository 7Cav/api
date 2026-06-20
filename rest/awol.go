package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// getAwol serves GET /api/v1/milpacs/awol: the list of members flagged
// absent without leave, golden-pinned by milpacs/awol. A near-clone of the
// ranks route (getAllRanks). Error message string frozen from the old
// handler (servers/grpc GetAwol).
func getAwol(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		awols, err := ds.FindAwol()
		if err != nil {
			writeError(w, r, codeInternal, "error fetching AWOL list: %v", err)
			return
		}
		writeJSON(w, r, types.AwolResponse{Awols: awols})
	})
}
