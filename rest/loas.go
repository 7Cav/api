package rest

import (
	"net/http"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// listLoas serves GET /api/v1/loas, every open and upcoming LOA plus the
// recent needs-review ones, read live from the LOA forums and ordered by
// thread id. The read:loas scope gates it. The handler passes the request
// instant to the datastore, which takes today as its UTC date. A datastore
// failure is a 500 with fixed wording and never an empty list, because a
// consumer that replaces its copy on every sync would wipe it during an
// outage.
func listLoas(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loas, err := ds.FindLoas(r.Context(), datastores.LoaQuery{Now: time.Now()})
		if err != nil {
			// The wire text stays fixed, and the detail stays in the log.
			Error.Printf("%s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, r, codeInternal, "error fetching LOAs")
			return
		}
		writeJSON(w, r, types.LoasResponse{Loas: loas})
	})
}
