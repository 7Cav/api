package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
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
		writeJSON(w, r, types.PositionGroupsResponse{Groups: positionGroupsFromProto(groups)})
	})
}

// positionGroupsFromProto maps the datastore's proto-typed rows to the wire
// types. The mapping layer disappears at cutover (#134); until then each
// handler owns its map — and the allocation discipline: empty collections are
// allocated ([] on the wire), never nil.
func positionGroupsFromProto(in []*proto.PositionGroup) []*types.PositionGroup {
	out := make([]*types.PositionGroup, 0, len(in))
	for _, g := range in {
		group := &types.PositionGroup{
			GroupId:           g.GetGroupId(),
			GroupTitle:        g.GetGroupTitle(),
			GroupDisplayOrder: g.GetGroupDisplayOrder(),
			Positions:         make([]*types.PositionExpanded, 0, len(g.GetPositions())),
		}
		for _, p := range g.GetPositions() {
			group.Positions = append(group.Positions, &types.PositionExpanded{
				PositionTitle:             p.GetPositionTitle(),
				PositionId:                p.GetPositionId(),
				PositionDisplayOrder:      p.GetPositionDisplayOrder(),
				PositionPossibleSecondary: p.GetPositionPossibleSecondary(),
			})
		}
		out = append(out, group)
	}
	return out
}
