package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
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
		writeJSON(w, r, types.AwolResponse{Awols: awolsFromProto(awols)})
	})
}

// awolsFromProto maps the datastore's proto-typed rows to the wire types.
// The mapping layer disappears at cutover (#134); until then each handler
// owns its map — and the allocation discipline: empty collections are
// allocated ([] on the wire), never nil.
func awolsFromProto(in []*proto.Awol) []*types.Awol {
	out := make([]*types.Awol, 0, len(in))
	for _, a := range in {
		out = append(out, &types.Awol{
			GroupName: a.GetGroupName(),
			RankName:  a.GetRankName(),
			Username:  a.GetUsername(),
			UserId:    a.GetUserId(),
			HumanDate: a.GetHumanDate(),
			Timestamp: a.GetTimestamp(),
			PostId:    a.GetPostId(),
			MilpacId:  a.GetMilpacId(),
		})
	}
	return out
}
