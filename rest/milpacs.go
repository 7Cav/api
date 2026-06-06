package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/types"
)

// getAllRanks serves GET /api/v1/milpacs/ranks: the rank reference list,
// golden-pinned by milpacs/ranks. Error message string frozen from the old
// handler (servers/grpc GetAllRanks).
func getAllRanks(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranks, err := ds.FindAllRanks()
		if err != nil {
			writeError(w, r, codeInternal, "error fetching ranks: %v", err)
			return
		}
		writeJSON(w, r, types.RanksResponse{Ranks: ranksFromProto(ranks)})
	})
}

// ranksFromProto maps the datastore's proto-typed rows to the wire types.
// The mapping layer disappears at cutover (#134) when the Datastore
// interface itself moves to the types package; until then each handler owns
// its map — and the allocation discipline: empty collections are allocated
// ([] on the wire), never nil.
func ranksFromProto(in []*proto.RankExpanded) []*types.RankExpanded {
	out := make([]*types.RankExpanded, 0, len(in))
	for _, r := range in {
		out = append(out, &types.RankExpanded{
			RankShort:        r.GetRankShort(),
			RankFull:         r.GetRankFull(),
			RankImageUrl:     r.GetRankImageUrl(),
			RankId:           r.GetRankId(),
			RankDisplayOrder: r.GetRankDisplayOrder(),
		})
	}
	return out
}
