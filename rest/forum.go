package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// getForumGroups serves GET /api/v1/forum/groups: the whole forum
// permission-group directory (xf_user_group) as a bare JSON array of
// {groupId, groupName} pairs, ordered by groupId ascending (ADR 0007). Gated
// by the read scope. The response is a List so an empty directory serializes
// as [], never null — even if the datastore hands back a nil slice.
func getForumGroups(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groups, err := ds.FindForumGroups()
		if err != nil {
			writeError(w, r, codeInternal, "error fetching forum groups: %v", err)
			return
		}
		writeJSON(w, r, types.List[*types.ForumGroup](groups))
	})
}
