package rest

import (
	"net/http"

	"github.com/7cav/api/datastores"
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
		roster, err := ds.FindRosterByType(rt)
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
		writeJSON(w, r, roster)
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
		roster, err := ds.FindLiteRosterByType(rt)
		if err != nil {
			writeError(w, r, codeInternal, "fetch lite roster %s: %v", rt, err)
			return
		}
		if roster == nil {
			writeError(w, r, codeInternal, "datastore returned no roster")
			return
		}
		writeJSON(w, r, roster)
	})
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
		roster, err := ds.FindS1UniformsRosterByType(rt)
		if err != nil {
			writeError(w, r, codeInternal, "fetch s1 uniforms roster %s: %v", rt, err)
			return
		}
		if roster == nil {
			writeError(w, r, codeInternal, "datastore returned no roster")
			return
		}
		writeJSON(w, r, roster)
	})
}
