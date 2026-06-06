package rest

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/types"
	"gorm.io/gorm"
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

// getProfileByID serves GET /api/v1/milpacs/profile/id/{user_id}: the full
// profile looked up by MILPAC RELATION KEY — the path value matches the
// milpac relation key, not the forum user id (frozen as-is, PRD #112; the
// profile_by_id_happy golden proves relation wins). Error message strings
// frozen from the old stack (servers/grpc GetProfile + the gateway's
// type-mismatch text).
//
// Binding quirk (PRD request-side leniency, frozen): username — the
// ProfileRequest field the path does NOT bind — remains query-bindable, and
// the old handler checked username BEFORE user_id, so a username query
// overrides the path id entirely (including the zero-id guard).
func getProfileByID(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path binding first, query binding second — gateway order: a
		// malformed path id 400s even when a username query is present.
		userID, err := strconv.ParseUint(r.PathValue("user_id"), 10, 64)
		if err != nil {
			// Gateway type-mismatch tier, parse-error text leaked. Frozen.
			writeError(w, r, codeInvalidArgument, "type mismatch, parameter: %s, error: %v", "user_id", err)
			return
		}
		username, err := queryField(r, "username", "username")
		if err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		if username != "" {
			serveProfileByUsername(w, r, ds, username)
			return
		}
		if userID == 0 {
			// id 0 parses but is the proto zero value. Frozen.
			writeError(w, r, codeInvalidArgument, "no username or user ID provided")
			return
		}
		profiles, err := ds.FindProfilesById(userID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, r, codeNotFound, "no profile found for user ID: %d", userID)
				return
			}
			writeError(w, r, codeInternal, "fetch profile by user id: %v", err)
			return
		}
		writeProfile(w, r, profiles[0])
	})
}

// getProfileByUsername serves GET /api/v1/milpacs/profile/username/{username}:
// the username binding of the same lookup. Error message strings frozen from
// the old handler (servers/grpc GetProfile, username branch).
//
// Binding quirk (PRD request-side leniency, frozen): user_id remains
// query-bindable here — the value still parses (a malformed one 400s, as the
// gateway did before the handler ran) but the handler's username-first
// precedence makes a valid one invisible.
func getProfileByUsername(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, err := queryField(r, "user_id", "userId"); err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		} else if raw != "" {
			if _, err := strconv.ParseUint(raw, 10, 64); err != nil {
				// Gateway query-binding parse error, text frozen from
				// grpc-gateway runtime (populateField).
				writeError(w, r, codeInvalidArgument, "parsing field %q: %v", "user_id", err)
				return
			}
		}
		serveProfileByUsername(w, r, ds, r.PathValue("username"))
	})
}

// getProfileByDiscordID serves GET /api/v1/milpac/discord/{discord_id}
// (singular "milpac" — the historical path, frozen): the full profile looked
// up by Discord snowflake. Error message strings frozen from the old handler
// (servers/grpc GetUserViaDiscordId). DiscordIdRequest's only field is
// path-bound, so nothing is query-bindable here; the old handler's
// empty-value guard is unreachable through the mux (an empty segment never
// matches the pattern).
func getProfileByDiscordID(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discordID := r.PathValue("discord_id")
		profile, err := ds.FindProfileByDiscordID(discordID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, r, codeNotFound, "no user found for discordid: %s", discordID)
				return
			}
			writeError(w, r, codeInternal, "fetch profile by discord id: %v", err)
			return
		}
		writeProfile(w, r, profile)
	})
}

// getProfileByGamertag serves GET /api/v1/milpac/gamertag/{gamertag}: the
// full profile looked up by console gamertag. Error message strings frozen
// from the old handler (servers/grpc GetGamertagProfile — note its NotFound
// formats the gamertag with %v where the discord handler uses %s; identical
// output for strings, kept verbatim anyway). Single path-bound field, so
// nothing is query-bindable; the empty-value guard is unreachable through
// the mux.
func getProfileByGamertag(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gamertag := r.PathValue("gamertag")
		profile, err := ds.FindProfileByGamertag(gamertag)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, r, codeNotFound, "no user found for gamertag: %v", gamertag)
				return
			}
			writeError(w, r, codeInternal, "fetch profile by gamertag: %v", err)
			return
		}
		writeProfile(w, r, profile)
	})
}

// serveProfileByUsername is the shared username lookup: the by-username
// route's body, and the by-id route's username-query override path.
func serveProfileByUsername(w http.ResponseWriter, r *http.Request, ds datastores.Datastore, username string) {
	profiles, err := ds.FindProfilesByUsername(username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, codeNotFound, "no profile found for username: %s", username)
			return
		}
		writeError(w, r, codeInternal, "fetch profile by username: %v", err)
		return
	}
	writeProfile(w, r, profiles[0])
}

// queryField reads a singular query-bindable message field, accepting both
// the proto (snake_case) and JSON (camelCase) key spellings — the gateway's
// dual-spelling leniency (PRD-frozen). Absent → "". Repeated values on a
// singular field are an error with the gateway's message shape; the proto
// field name is the one error messages cite.
func queryField(r *http.Request, protoName, jsonName string) (string, error) {
	q := r.URL.Query()
	vals := q[protoName]
	if jsonName != protoName {
		vals = append(vals, q[jsonName]...)
	}
	switch len(vals) {
	case 0:
		return "", nil
	case 1:
		return vals[0], nil
	default:
		return "", fmt.Errorf("too many values for field %q: %s", protoName, strings.Join(vals, ", "))
	}
}

// writeProfile maps one datastore profile to the wire type and writes it.
func writeProfile(w http.ResponseWriter, r *http.Request, p *proto.Profile) {
	writeJSON(w, r, profileFromProto(p))
}

// profileFromProto maps the datastore's proto-typed profile to the wire type.
// Allocation discipline: collections are always allocated ([] on the wire,
// never null); unset nested messages stay nil (null on the wire). keycloakId
// is dropped here — the wire type never had the field (documented break).
func profileFromProto(p *proto.Profile) *types.Profile {
	out := &types.Profile{
		RealName:          p.GetRealName(),
		UniformUrl:        p.GetUniformUrl(),
		Roster:            types.RosterType(p.GetRoster()),
		Secondaries:       make([]*types.Position, 0, len(p.GetSecondaries())),
		Records:           make([]*types.Record, 0, len(p.GetRecords())),
		Awards:            make([]*types.Award, 0, len(p.GetAwards())),
		JoinDate:          p.GetJoinDate(),
		PromotionDate:     p.GetPromotionDate(),
		DiscordId:         p.GetDiscordId(),
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
	for _, rec := range p.GetRecords() {
		out.Records = append(out.Records, &types.Record{
			RecordDetails: rec.GetRecordDetails(),
			RecordType:    types.RecordType(rec.GetRecordType()),
			RecordDate:    rec.GetRecordDate(),
			RecordUid:     rec.GetRecordUid(),
		})
	}
	for _, a := range p.GetAwards() {
		out.Awards = append(out.Awards, &types.Award{
			AwardDetails:  a.GetAwardDetails(),
			AwardName:     a.GetAwardName(),
			AwardDate:     a.GetAwardDate(),
			AwardImageUrl: a.GetAwardImageUrl(),
			AwardUid:      a.GetAwardUid(),
		})
	}
	return out
}

// positionFromProto maps a position, preserving nil (null on the wire).
func positionFromProto(p *proto.Position) *types.Position {
	if p == nil {
		return nil
	}
	return &types.Position{PositionTitle: p.GetPositionTitle(), PositionId: p.GetPositionId()}
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
