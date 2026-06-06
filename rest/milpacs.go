package rest

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
// the old handler checked username BEFORE user_id, so a NON-EMPTY username
// query overrides the path id entirely (including the zero-id guard). A
// present-but-empty ?username= does not override: the gateway bound "" and
// the old handler treated "" as unset.
func getProfileByID(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path binding first, query binding second — gateway order: a
		// malformed path id 400s even when a username query is present.
		//
		// BASE 0, not 10 — the gateway's runtime.Uint64 was
		// strconv.ParseUint(val, 0, 64) (grpc-gateway v2.29.0
		// runtime/convert.go), so the path id inherits Go integer-literal
		// parsing: 0x1 is hex 1, 010 is OCTAL 8 (a wrong-profile hazard if
		// parsed base 10), 0b1 is binary 1, 1_0 is 10, and 09 is invalid
		// syntax (octal with a 9). Frozen as-is (PRD #112 leniency tier).
		userID, err := strconv.ParseUint(r.PathValue("user_id"), 0, 64)
		if err != nil {
			// Gateway type-mismatch tier, parse-error text leaked. Frozen.
			writeError(w, r, codeInvalidArgument, "type mismatch, parameter: %s, error: %v", "user_id", err)
			return
		}
		if err := checkQuerySyntax(r); err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		usernameVals, err := queryField(r, "username", "username")
		if err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		username := ""
		if len(usernameVals) > 0 {
			username = usernameVals[len(usernameVals)-1]
		}
		// Non-empty, not merely present: username is a STRING field, so the
		// gateway bound a present-but-empty value as "" without error, and the
		// old handler treated "" as unset — ?username= falls through to the
		// path id (the asymmetry with the numeric user_id binding, where
		// present-empty 400s in the parser).
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
		if len(profiles) == 0 {
			// Datastore invariant violated (non-empty slice on nil error —
			// see datastores.Datastore): unreachable through the real
			// datastore, guarded so a future bug is a clean 500, not a panic.
			writeError(w, r, codeInternal, "datastore returned no profiles")
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
// query-bindable here — every PRESENT value still parses (a malformed OR
// EMPTY one 400s, as the gateway did before the handler ran: no empty-value
// guard in runtime/query.go) but the handler's username-first precedence
// makes a valid one invisible.
func getProfileByUsername(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := checkQuerySyntax(r); err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		userIDVals, err := queryField(r, "user_id", "userId")
		if err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		// Parse EVERY present value — both spellings, including "" (the
		// gateway had no empty-value guard, so ?user_id= is a 400). A bad
		// value always errored in the old gateway regardless of map order;
		// the snake-spelling value parsing first here is the deterministic
		// stand-in. When all parse, the last (camelCase) value is the bound
		// one — a RULING (see queryField), invisible anyway under the
		// handler's username-first precedence.
		for _, raw := range userIDVals {
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
	if len(profiles) == 0 {
		// Datastore invariant violated (non-empty slice on nil error — see
		// datastores.Datastore): unreachable through the real datastore,
		// guarded so a future bug is a clean 500, not a panic.
		writeError(w, r, codeInternal, "datastore returned no profiles")
		return
	}
	writeProfile(w, r, profiles[0])
}

// checkQuerySyntax rejects malformed query-string SYNTAX on the two profile
// routes whose old generated gateway handlers called req.ParseForm() and 400'd
// on its error (request_MilpacService_GetProfile_0/_1 in proto/milpacs.pb.gw.go,
// wrapped as InvalidArgument "%v" — text leaked verbatim, e.g. `invalid URL
// escape "%zz"`, `invalid semicolon separator in query`). The error fired even
// when the malformed pair was an unknown parameter, BEFORE field filtering.
// r.URL.Query() would silently drop bad pairs, so the parse is explicit.
//
// SCOPE: the discord and gamertag routes must NOT call this — their generated
// handlers never called ParseForm (single path-bound field, no query binding).
func checkQuerySyntax(r *http.Request) error {
	_, err := url.ParseQuery(r.URL.RawQuery)
	return err
}

// bracketKeyRegexp is the old gateway's bracket-key rewrite, mirrored exactly
// (grpc-gateway v2.29.0 runtime/query.go valuesKeyRegexp): a query key
// matching ^(.*)\[(.*)\]$ folds into its base key (match 1) with the bracket
// CONTENT (match 2) PREPENDED as an extra value — ?user_id[0]=1 is key
// "user_id", values ["0","1"]. The content is a VALUE, never an index, so a
// matching bracket key on these singular fields is always the too-many-values
// 400. Greedy: a[b][c] folds to base "a[b]" — matching no field, ignored.
var bracketKeyRegexp = regexp.MustCompile(`^(.*)\[(.*)\]$`)

// queryField reads a singular query-bindable message field, accepting both
// the proto (snake_case) and JSON (camelCase) key spellings — the gateway's
// dual-spelling leniency (PRD-frozen). It returns every present value, one
// per spelling, because the gateway processed each url.Values KEY
// independently: each value parsed, each set the field, no cross-spelling
// error. The proto field name is the one error messages cite (the gateway
// cited fieldDescriptor.FullName().Name() whatever the key spelling).
//
//   - len(vals) == 0: field absent. Present-but-EMPTY is NOT absent — the
//     gateway parsed every present value (no empty-value guard in
//     runtime/query.go), so "" still reaches the caller's parser.
//   - Repetition under ONE key errors with the gateway's deterministic
//     "too many values" shape (per-key check, that key's values joined).
//   - Bracket keys whose base (bracketKeyRegexp match 1) is either spelling
//     FOLD into the field as their own per-key group, bracket content first —
//     the gateway rewrote the key before binding, and resolved the rewritten
//     key by text name AND JSON name, so both spellings fold. The fold always
//     carries ≥2 values on a singular field → the too-many-values 400,
//     quoting the folded group. Non-matching bracket keys are just unknown
//     parameters — ignored.
//   - Order is deterministic: snake-spelling value first, camelCase next,
//     bracket groups last sorted by raw key — so a caller binding
//     last-value-wins lands on the camelCase value. That precedence is a
//     RULING (converged with #129's query binder), standing in for the old
//     gateway's map-iteration nondeterminism, not parity.
func queryField(r *http.Request, protoName, jsonName string) (vals []string, err error) {
	q := r.URL.Query()
	groups := [][]string{q[protoName]}
	if jsonName != protoName {
		groups = append(groups, q[jsonName])
	}
	var bracketKeys []string
	for key := range q {
		if m := bracketKeyRegexp.FindStringSubmatch(key); len(m) == 3 && (m[1] == protoName || m[1] == jsonName) {
			bracketKeys = append(bracketKeys, key)
		}
	}
	sort.Strings(bracketKeys)
	for _, key := range bracketKeys {
		m := bracketKeyRegexp.FindStringSubmatch(key)
		groups = append(groups, append([]string{m[2]}, q[key]...))
	}
	for _, kv := range groups {
		if len(kv) > 1 {
			return nil, fmt.Errorf("too many values for field %q: %s", protoName, strings.Join(kv, ", "))
		}
		vals = append(vals, kv...)
	}
	return vals, nil
}

// writeProfile maps one datastore profile to the wire type and writes it. A
// nil profile with no error is a datastore-invariant violation (unreachable
// through the real datastore): a clean 500, not a fabricated sparse 200 —
// the proto getters would happily marshal a zero-value profile.
func writeProfile(w http.ResponseWriter, r *http.Request, p *proto.Profile) {
	if p == nil {
		writeError(w, r, codeInternal, "datastore returned no profile")
		return
	}
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
