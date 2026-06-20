package rest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/rest"
	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// positionsGet replays one authenticated GET against the stack — the local
// shorthand for this file's request loop.
func positionsGet(t *testing.T, h http.Handler, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// A datastore failure on each #128 route must surface as the frozen Internal
// error shape with the handler-specific wrapped message (the corpus has no
// outage cases for these routes, so these new-stack tests pin them — message
// strings verbatim from the old handlers, servers/grpc).
func TestNewStack_PositionAndAwolOutagesAreInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findAllPositionGroups:  func() ([]*types.PositionGroup, error) { return nil, io.ErrUnexpectedEOF },
		findProfilesByPosition: func(string) (*types.LiteRoster, error) { return nil, io.ErrUnexpectedEOF },
		findAwol:               func() ([]*types.Awol, error) { return nil, io.ErrUnexpectedEOF },
	}, &stubReferenceCache{})

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/milpacs/position/groups", "error fetching position groups: unexpected EOF"},
		{"/api/v1/milpacs/position/search/Rifleman", "error searching profiles by position: unexpected EOF"},
		{"/api/v1/milpacs/awol", "error fetching AWOL list: unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := positionsGet(t, h, tc.path, "cav7_readkey")

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// An empty position-group catalog must serialize as {"groups":[]} — the
// allocation discipline (empty collections are [], never null) the goldens
// can only witness on populated routes.
func TestNewStack_EmptyPositionGroupsIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllPositionGroups: func() ([]*types.PositionGroup, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	rr := positionsGet(t, h, "/api/v1/milpacs/position/groups", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"groups":[]}`, strings.TrimSpace(rr.Body.String()))
}

// A lite roster whose profiles map the datastore left NIL (nil error) still
// emits {"profiles":{}} — the mapper allocates, so the frozen empty-result
// form (#137) cannot regress to null through a lazy datastore.
func TestNewStack_SearchNilProfilesMapIsEmptyObject(t *testing.T) {
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(string) (*types.LiteRoster, error) {
		return &types.LiteRoster{}, nil // Profiles map nil, not allocated
	}}, &stubReferenceCache{})

	rr := positionsGet(t, h, "/api/v1/milpacs/position/search/Rifleman", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"profiles":{}}`, strings.TrimSpace(rr.Body.String()))
}

// A NIL roster with a nil error violates the datastore invariant
// (datastores.Mysql always allocates): it must surface as the frozen
// Internal shape, not a panic or a fabricated empty 200.
func TestNewStack_SearchNilRosterWithNilErrorIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(string) (*types.LiteRoster, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	rr := positionsGet(t, h, "/api/v1/milpacs/position/search/Rifleman", "cav7_readkey")

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"datastore returned no roster","details":[]}`, rr.Body.String())
}

// A sparse lite profile must come through with its nils PRESERVED:
// unset User/Rank/Primary stay null on the wire (never fabricated as zeroed
// &types.User{}/&types.Rank{}) and empty Secondaries is [] — the
// recording-seed shape (seedDoeLite, contract/fake_datastore_test.go) minus
// User/Rank, so every nil-guard branch in the lite-roster response runs unset.
func TestNewStack_SearchSparseLiteProfilePreservesNils(t *testing.T) {
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(string) (*types.LiteRoster, error) {
		return &types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{2: {
			RealName:        "John Doe",
			UniformUrl:      "https://7cav.us/data/roster_uniforms/0/2.jpg",
			Roster:          types.RosterTypeCombat,
			Secondaries:     []*types.Position{},
			JoinDate:        "2026-01-15",
			ConsoleGamertag: "CavGamer77",
		}}}, nil
	}}, &stubReferenceCache{})

	rr := positionsGet(t, h, "/api/v1/milpacs/position/search/Rifleman", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"profiles":{"2":{
		"user":null,
		"rank":null,
		"realName":"John Doe",
		"uniformUrl":"https://7cav.us/data/roster_uniforms/0/2.jpg",
		"roster":"ROSTER_TYPE_COMBAT",
		"primary":null,
		"secondaries":[],
		"joinDate":"2026-01-15",
		"promotionDate":"",
		"discordId":"",
		"awardDate":"",
		"recordDate":"",
		"lastForumPostDate":"",
		"mos":"",
		"consoleGamertag":"CavGamer77"
	}}}`, rr.Body.String())
}

// --- Position-query decoding (deliberate break, PRD #112 / #128) -----------
//
// The query reaches the datastore STANDARD-decoded: r.PathValue unescapes
// each segment once (net/http), replacing the legacy gateway's own **
// percent-decoding. The goldens witness the happy %20 form end-to-end; this
// pins the datastore-facing value itself, including the multi-segment form
// where literal slashes survive while encoded characters inside segments
// decode.
func TestNewStack_SearchQueryDecodesOncePreservingSlashes(t *testing.T) {
	var got string
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(q string) (*types.LiteRoster, error) {
		got = q
		return &types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{}}, nil
	}}, &stubReferenceCache{})

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/milpacs/position/search/Regimental%20Technical%20Aide", "Regimental Technical Aide"},
		{"/api/v1/milpacs/position/search/Platoon/Leader", "Platoon/Leader"},
		// %2F is the canonical exotic encoding the deliberate break diverges
		// on: standard per-segment decoding yields a literal slash INSIDE the
		// bound value, indistinguishable from the unencoded multi-segment form.
		{"/api/v1/milpacs/position/search/Platoon%2FLeader", "Platoon/Leader"},
		{"/api/v1/milpacs/position/search/Platoon%20Sergeant/Bravo%26Charlie", "Platoon Sergeant/Bravo&Charlie"},
		// %25 is a literal percent: single decoding yields "100%", and the
		// datastore's own LIKE-escaping handles it from there.
		{"/api/v1/milpacs/position/search/100%25", "100%"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got = ""
			rr := positionsGet(t, h, tc.path, "cav7_readkey")

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Equal(t, tc.want, got, "datastore must see the standard-decoded query")
		})
	}
}

// The slashless form /position/search binds the empty query directly: the
// old gateway's ** glob matched ZERO segments (httprule OpPushM), so the old
// stack answered the handler's empty-query 400 here — NOT the 307 redirect
// to /search/ the bare ServeMux wildcard would send (the explicit slashless
// registration in routes() restores parity; no golden witnesses this form).
func TestNewStack_SearchWithoutTrailingSlashIsEmptyQuery400(t *testing.T) {
	h := newStack(t)

	rr := positionsGet(t, h, "/api/v1/milpacs/position/search", "cav7_readkey")

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"position query cannot be empty","details":[]}`, rr.Body.String())
}

// Wrong-method on the search wildcard keeps the 405 + Allow contract: the
// fallback's GET probe matches the {position_query...} pattern like any
// registered route (no ticketSub-style narrowing needed — every match is a
// real route).
func TestNewStack_WrongMethodOnSearchWildcardIs405WithAllow(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/milpacs/position/search/Rifleman", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, "GET, HEAD", rr.Header().Get("Allow"))
	assert.JSONEq(t, `{"code":12,"message":"Method Not Allowed","details":[]}`, rr.Body.String())
}

// positionFamilyScopePaths is THE path list of the #128 route family — five
// forms witnessing its four registrations (the search wildcard is witnessed
// twice: a multi-segmentable query and the bare trailing-slash empty-query
// form). ONE list, shared by the scope-403 loop below and the
// registration-completeness guard that follows: a family route can't be
// scope-tested without being guard-probed, or vice versa.
var positionFamilyScopePaths = []string{
	"/api/v1/milpacs/position/groups",
	"/api/v1/milpacs/position/search/Rifleman",
	"/api/v1/milpacs/position/search/",
	"/api/v1/milpacs/position/search",
	"/api/v1/milpacs/awol",
}

// The scope gate is witnessed PER route: a ticket-scoped key (read:tickets,
// not read) must 403 with the frozen body on each #128 route — the golden
// tier only witnesses one milpacs path, so this loop proves no route was
// registered without its requireScope gate. The search trailing-slash and
// slashless forms ride along: 403 answers BEFORE the empty-query 400 (the
// uniform tier order, ruled at #126 — 403-before-binding-400).
func TestNewStack_PositionAndAwolRoutes403UnderTicketScopedKey(t *testing.T) {
	h := newStack(t)

	for _, path := range positionFamilyScopePaths {
		t.Run(path, func(t *testing.T) {
			rr := positionsGet(t, h, path, "cav7_ticketskey")

			require.Equal(t, http.StatusForbidden, rr.Code)
			assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
		})
	}
}

// The completeness guard (#128 round 3, ruling 2): the round-2 slashless
// omission was an authz hole — the scope loop silently lost a registered
// route. This guard makes the loop's coverage self-checking: the EXPECTED set
// is every handle() registration in the position/awol family, derived from
// the real registration table (rest.RoutesForTest); the WITNESSED set is the
// patterns positionFamilyScopePaths actually matches, probed through
// mux.Handler. Deleting a loop entry (the slashless one included) — or
// registering another family route without adding its loop path — leaves a
// family pattern unwitnessed and goes red.
func TestNewStack_ScopeLoopCoversEveryPositionFamilyRegistration(t *testing.T) {
	mux, patterns := rest.RoutesForTest(&fakeDatastore{}, &stubReferenceCache{})

	// The family namespace: every position route plus the awol route — the
	// #128 registrations and any future sibling under the same prefixes.
	isFamily := func(pattern string) bool {
		p := strings.TrimPrefix(pattern, "GET ")
		return strings.HasPrefix(p, "/api/v1/milpacs/position") || p == "/api/v1/milpacs/awol"
	}

	witnessed := map[string]bool{}
	for _, path := range positionFamilyScopePaths {
		_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
		require.NotEqual(t, "/", pattern,
			"scope-loop path %s falls through to the catch-all — it witnesses no registered route", path)
		require.True(t, isFamily(pattern),
			"scope-loop path %s matched %q, outside the family it claims to witness", path, pattern)
		witnessed[pattern] = true
	}

	var family int
	for _, pattern := range patterns {
		if !isFamily(pattern) {
			continue
		}
		family++
		assert.True(t, witnessed[pattern],
			"registered family route %q has no witness in positionFamilyScopePaths — add its path to the scope-403 loop", pattern)
	}
	// Non-vacuousness: the family filter must see the four #128 registrations
	// (groups, search wildcard, slashless search, awol); fewer means the
	// filter — or the registration table seam — rotted.
	require.GreaterOrEqual(t, family, 4, "family filter saw %d registrations, expected the four #128 ones", family)
}

// The path-cleaning 307, pinned (ruled, #128 round 3 — enumerated deliberate
// break alongside percent-decoding, see searchByPosition): an UNCLEAN search
// path the old ** glob served as a 200 redirects exactly as the mux computes
// it — 307, Location the cleaned path — but carries the contract JSON body
// (shape + content type), never net/http's default HTML.
func TestNewStack_SearchUncleanPathIs307WithJSONBody(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		path         string
		wantLocation string
	}{
		{"/api/v1/milpacs/position/search/A//B", "/api/v1/milpacs/position/search/A/B"},
		{"/api/v1/milpacs/position/search/A/../B", "/api/v1/milpacs/position/search/B"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := positionsGet(t, h, tc.path, "cav7_readkey")

			require.Equal(t, http.StatusTemporaryRedirect, rr.Code)
			assert.Equal(t, tc.wantLocation, rr.Header().Get("Location"))
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"code":2,"message":"Temporary Redirect","details":[]}`, rr.Body.String())
		})
	}
}

// The #128 routes must NOT gain the ParseForm guard: their generated gateway
// handlers never called ParseForm (no query-bound fields), so malformed query
// syntax fell through and the request succeeded — same leniency pin as
// TestNewStack_ConnectedAccountRoutes_MalformedQuerySyntaxIgnored.
func TestNewStack_PositionAndAwolRoutes_MalformedQuerySyntaxIgnored(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/milpacs/position/search/Rifleman?junk=%zz",
		"/api/v1/milpacs/position/groups?junk=%zz",
		"/api/v1/milpacs/awol?junk=%zz",
	} {
		t.Run(path, func(t *testing.T) {
			rr := positionsGet(t, h, path, "cav7_readkey")

			require.Equal(t, http.StatusOK, rr.Code, "old gateway never ParseForm'd these routes")
		})
	}
}
