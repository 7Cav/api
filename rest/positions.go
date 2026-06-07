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
		writeJSON(w, r, liteRosterFromProto(roster))
	})
}

// liteRosterFromProto maps the datastore's proto-typed lite roster to the
// wire type. Allocation discipline: the profiles map is always allocated
// ({} on the wire, never null); unset nested messages stay nil (null on the
// wire). keycloakId is dropped — the wire type never had the field
// (documented break).
func liteRosterFromProto(in *proto.LiteRoster) *types.LiteRoster {
	out := &types.LiteRoster{Profiles: make(map[uint64]*types.LiteProfile, len(in.GetProfiles()))}
	for id, p := range in.GetProfiles() {
		lp := &types.LiteProfile{
			RealName:          p.GetRealName(),
			UniformUrl:        p.GetUniformUrl(),
			Roster:            types.RosterType(p.GetRoster()),
			Secondaries:       make([]*types.Position, 0, len(p.GetSecondaries())),
			JoinDate:          p.GetJoinDate(),
			PromotionDate:     p.GetPromotionDate(),
			DiscordId:         p.GetDiscordId(),
			AwardDate:         p.GetAwardDate(),
			RecordDate:        p.GetRecordDate(),
			LastForumPostDate: p.GetLastForumPostDate(),
			Mos:               p.GetMos(),
			ConsoleGamertag:   p.GetConsoleGamertag(),
		}
		if u := p.GetUser(); u != nil {
			lp.User = &types.User{UserId: u.GetUserId(), Username: u.GetUsername()}
		}
		if rk := p.GetRank(); rk != nil {
			lp.Rank = &types.Rank{
				RankShort:    rk.GetRankShort(),
				RankFull:     rk.GetRankFull(),
				RankImageUrl: rk.GetRankImageUrl(),
				RankId:       rk.GetRankId(),
			}
		}
		lp.Primary = positionFromProto(p.GetPrimary())
		for _, s := range p.GetSecondaries() {
			lp.Secondaries = append(lp.Secondaries, positionFromProto(s))
		}
		out.Profiles[id] = lp
	}
	return out
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
