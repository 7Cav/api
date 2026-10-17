package rest

import (
	"errors"
	"net/http"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// listLoas serves GET /api/v1/loas, every open and upcoming LOA plus the
// recent needs-review ones, read live from the LOA forums and ordered by
// thread id. An optional ended_since date, also spelled endedSince, adds the
// LOAs that ended on or after it and the needs-review ones posted on or after
// it. The read:loas scope gates it. The handler passes the request instant to
// the datastore, which takes today as its UTC date and refuses an
// ended_since more than 365 days before it. That refusal and a malformed
// date are 400s.
// A datastore failure is a 500 with fixed wording and never an empty list,
// because a consumer that replaces its copy on every sync would wipe it
// during an outage.
func listLoas(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := bindListQuery(w, r)
		if !ok {
			return
		}
		endedSince := b.dateField("ended_since")
		if err := b.Err(); err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		loas, err := ds.FindLoas(r.Context(), datastores.LoaQuery{Now: time.Now(), EndedSince: endedSince})
		if err != nil {
			if errors.Is(err, datastores.ErrEndedSinceTooFarBack) {
				Info.Printf("%s %s: %v", r.Method, r.URL.Path, err)
				writeError(w, r, codeInvalidArgument, "ended_since is more than 365 days before today's UTC date")
				return
			}
			// The wire text stays fixed, and the detail stays in the log.
			Error.Printf("%s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, r, codeInternal, "error fetching LOAs")
			return
		}
		writeJSON(w, r, types.LoasResponse{Loas: loas})
	})
}
