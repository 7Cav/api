package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/types"
)

// The three roster routes (#127) serve the SAME member set in three profile
// shapes (the domain glossary's Profile / LiteProfile / S1UniformsProfile):
//
//	GET /api/v1/roster/{roster}        → Roster            (full)
//	GET /api/v1/roster/{roster}/lite   → LiteRoster        (lite)
//	GET /api/v1/s1/uniforms/{roster}   → S1UniformsRoster  (S1 uniforms tool)
//
// All three bind the RosterType enum path parameter in both golden-pinned
// forms (name or number — see types.ParseRosterType), reject the zero value
// with the frozen 'cannot request null roster type' 400, and serialize the
// native wire types directly to JSON (single marshal — the old stack's
// proto→JSON double-marshal was a known latency bug on the multi-megabyte
// rosters; do not reproduce it).
//
// RosterRequest's only field is path-bound, so nothing is query-bindable on
// these routes and the generated gateway handlers never called ParseForm
// (verified against proto/milpacs.pb.gw.go) — unknown parameters AND
// malformed query syntax are ignored alike (the unknown_query_param_ignored
// golden pins the former).

// bindRosterPath binds the {roster} path parameter: the gateway's enum parse
// (wrapped in the frozen type-mismatch 400 on failure, parameter name
// "roster" — the proto field the gateway cited) followed by the old
// handlers' zero-value guard. The guard ran in the RPC body AFTER the
// gateway bind, so a bogus literal answers the type-mismatch text while
// ROSTER_TYPE_UNSPECIFIED / 0 answer the null-roster text — order frozen by
// the bogus_enum and unspecified_* goldens. On failure the response is
// already written.
func bindRosterPath(w http.ResponseWriter, r *http.Request) (types.RosterType, bool) {
	rt, err := types.ParseRosterType(r.PathValue("roster"))
	if err != nil {
		// Gateway type-mismatch tier, enum-parse text leaked. Frozen.
		writeError(w, r, codeInvalidArgument, "type mismatch, parameter: %s, error: %v", "roster", err)
		return 0, false
	}
	if rt == types.RosterTypeUnspecified {
		// Old handler guard (servers/grpc GetRoster et al.). Frozen.
		writeError(w, r, codeInvalidArgument, "cannot request null roster type")
		return 0, false
	}
	return rt, true
}

// getRoster serves GET /api/v1/roster/{roster}: the full roster. Error
// message strings frozen from the old handler (servers/grpc GetRoster — the
// enum NAME interpolates into the Internal message via %s).
func getRoster(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := bindRosterPath(w, r)
		if !ok {
			return
		}
		roster, err := ds.FindRosterByType(proto.RosterType(rt))
		if err != nil {
			writeError(w, r, codeInternal, "fetch roster %s: %v", rt, err)
			return
		}
		if roster == nil {
			// Datastore invariant violated (non-nil roster on nil error):
			// unreachable through the real datastore, guarded so a future bug
			// is a clean 500, not a panic.
			writeError(w, r, codeInternal, "datastore returned no roster")
			return
		}
		out := types.Roster{Profiles: make(map[uint64]*types.Profile, len(roster.GetProfiles()))}
		for id, p := range roster.GetProfiles() {
			out.Profiles[id] = profileFromProto(p)
		}
		writeJSON(w, r, out)
	})
}

// getLiteRoster serves GET /api/v1/roster/{roster}/lite: the same member set
// as LiteProfile views. Error message strings frozen from the old handler
// (servers/grpc GetLiteRoster).
func getLiteRoster(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := bindRosterPath(w, r)
		if !ok {
			return
		}
		roster, err := ds.FindLiteRosterByType(proto.RosterType(rt))
		if err != nil {
			writeError(w, r, codeInternal, "fetch lite roster %s: %v", rt, err)
			return
		}
		if roster == nil {
			writeError(w, r, codeInternal, "datastore returned no roster")
			return
		}
		out := types.LiteRoster{Profiles: make(map[uint64]*types.LiteProfile, len(roster.GetProfiles()))}
		for id, p := range roster.GetProfiles() {
			out.Profiles[id] = liteProfileFromProto(p)
		}
		writeJSON(w, r, out)
	})
}

// liteProfileFromProto maps the datastore's proto-typed lite profile to the
// wire type. Same discipline as profileFromProto: collections always
// allocated, unset nested messages stay nil, keycloakId dropped (the wire
// type never had the field — documented break).
func liteProfileFromProto(p *proto.LiteProfile) *types.LiteProfile {
	out := &types.LiteProfile{
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
		out.User = &types.User{UserId: u.GetUserId(), Username: u.GetUsername()}
	}
	if rk := p.GetRank(); rk != nil {
		out.Rank = &types.Rank{
			RankShort:    rk.GetRankShort(),
			RankFull:     rk.GetRankFull(),
			RankImageUrl: rk.GetRankImageUrl(),
			RankId:       rk.GetRankId(),
		}
	}
	out.Primary = positionFromProto(p.GetPrimary())
	for _, s := range p.GetSecondaries() {
		out.Secondaries = append(out.Secondaries, positionFromProto(s))
	}
	return out
}

// getS1UniformsRoster serves GET /api/v1/s1/uniforms/{roster}: the same
// member set as S1UniformsProfile views. Error message strings frozen from
// the old handler (servers/grpc GetS1UniformsRoster).
func getS1UniformsRoster(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt, ok := bindRosterPath(w, r)
		if !ok {
			return
		}
		roster, err := ds.FindS1UniformsRosterByType(proto.RosterType(rt))
		if err != nil {
			writeError(w, r, codeInternal, "fetch s1 uniforms roster %s: %v", rt, err)
			return
		}
		if roster == nil {
			writeError(w, r, codeInternal, "datastore returned no roster")
			return
		}
		out := types.S1UniformsRoster{Profiles: make(map[uint64]*types.S1UniformsProfile, len(roster.GetProfiles()))}
		for id, p := range roster.GetProfiles() {
			out.Profiles[id] = s1UniformsProfileFromProto(p)
		}
		writeJSON(w, r, out)
	})
}

// s1UniformsProfileFromProto maps the datastore's proto-typed S1 uniforms
// profile to the wire type — same discipline as the other two mappers.
func s1UniformsProfileFromProto(p *proto.S1UniformsProfile) *types.S1UniformsProfile {
	out := &types.S1UniformsProfile{
		RealName:                 p.GetRealName(),
		UniformUrl:               p.GetUniformUrl(),
		UniformDate:              p.GetUniformDate(),
		UniformUpdateTriggerDate: p.GetUniformUpdateTriggerDate(),
		Roster:                   types.RosterType(p.GetRoster()),
		PrimaryPositionTitle:     p.GetPrimaryPositionTitle(),
		Secondaries:              make([]*types.S1UniformsPosition, 0, len(p.GetSecondaries())),
		JoinDate:                 p.GetJoinDate(),
		PromotionDate:            p.GetPromotionDate(),
		AreaOfResponsibility:     p.GetAreaOfResponsibility(),
	}
	if u := p.GetUser(); u != nil {
		out.User = &types.User{UserId: u.GetUserId(), Username: u.GetUsername()}
	}
	if rk := p.GetRank(); rk != nil {
		out.Rank = &types.S1UniformsRank{
			RankShort:    rk.GetRankShort(),
			RankFull:     rk.GetRankFull(),
			RankImageUrl: rk.GetRankImageUrl(),
		}
	}
	for _, s := range p.GetSecondaries() {
		out.Secondaries = append(out.Secondaries, s1PositionFromProto(s))
	}
	return out
}

// s1PositionFromProto maps an S1 uniforms position, preserving nil (null on
// the wire) — the S1 counterpart of positionFromProto.
func s1PositionFromProto(p *proto.S1UniformsPosition) *types.S1UniformsPosition {
	if p == nil {
		return nil
	}
	return &types.S1UniformsPosition{PositionTitle: p.GetPositionTitle()}
}
