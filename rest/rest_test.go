package rest_test

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/contract"
	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const goldensDir = "../contract/goldens"

func init() {
	// The auth middleware Warn-logs every rejected request; the battery
	// replays many. Errors stay visible.
	rest.Info.SetOutput(io.Discard)
	rest.Warn.SetOutput(io.Discard)
}

// fakeDatastore seeds the new stack for golden replay. It must agree with
// the recording seed (contract/fake_datastore_test.go) for everything the
// implemented routes serve: the battery's bearer tokens, the two-rank
// catalog, and the two seed profiles (relation 1 ↔ user 3 Jarvis.A, relation
// 2 ↔ user 8 John.Doe — the divergence keeps the relation-key semantic
// observable). Unimplemented methods panic via the embedded nil interface —
// a loud failure if a test reaches further than the routes it mounts.
type fakeDatastore struct {
	datastores.Datastore
	findAllRanks           func() ([]*proto.RankExpanded, error)
	findProfilesById       func(userIds ...uint64) ([]*proto.Profile, error)
	findProfilesByUsername func(username string) ([]*proto.Profile, error)
	findProfileByDiscordID func(discordId string) (*proto.Profile, error)
	findProfileByGamertag  func(gamertag string) (*proto.Profile, error)
}

func (f *fakeDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	scopes := func(names ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, n := range names {
			m[n] = struct{}{}
		}
		return m
	}
	switch rawKey {
	case "cav7_readkey":
		return &datastores.ApiKeyResult{KeyId: 101, UserId: 3, Scopes: scopes("read")}, nil
	case "cav7_ticketskey":
		return &datastores.ApiKeyResult{KeyId: 102, UserId: 8, Scopes: scopes("read:tickets")}, nil
	case "cav7_noscopekey":
		return &datastores.ApiKeyResult{KeyId: 103, UserId: 9, Scopes: scopes()}, nil
	default:
		return nil, nil // zero rows — generic Unauthorized, leaks nothing
	}
}

func (f *fakeDatastore) FindAllRanks() ([]*proto.RankExpanded, error) {
	if f.findAllRanks != nil {
		return f.findAllRanks()
	}
	return []*proto.RankExpanded{
		{RankShort: "MG", RankFull: "Major General", RankImageUrl: "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618", RankId: 4, RankDisplayOrder: 4},
		{RankShort: "PVT", RankFull: "Private", RankImageUrl: "https://7cav.us/data/roster_ranks/0/22.jpg", RankId: 22, RankDisplayOrder: 22},
	}, nil
}

// errOutage is the injected failure for the Internal-error goldens: the
// message leaks into the response body via the handlers' error wrapping —
// that leak is frozen behavior (mirrors the recording seed's errOutage).
var errOutage = errors.New("simulated datastore outage")

// seedJarvis mirrors the recording seed's rich profile: every collection
// populated, relation 1 ↔ user 3. KeycloakId is deliberately set — the
// corpus transform stripped it from the goldens, so the mapper dropping it
// is observable in the replay.
func seedJarvis() *proto.Profile {
	return &proto.Profile{
		User: &proto.User{UserId: 3, Username: "Jarvis.A"},
		Rank: &proto.Rank{
			RankShort:    "MG",
			RankFull:     "Major General",
			RankImageUrl: "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618",
			RankId:       4,
		},
		RealName:   "Adam Jarvis",
		UniformUrl: "https://7cav.us/data/roster_uniforms/0/1.jpg",
		Roster:     proto.RosterType_ROSTER_TYPE_COMBAT,
		Primary:    &proto.Position{PositionTitle: "Regimental Technical Aide", PositionId: 773},
		Secondaries: []*proto.Position{
			{PositionTitle: "S6 Web Developer", PositionId: 812},
		},
		Records: []*proto.Record{
			{
				RecordDetails: "Promoted to Major General (O-8)",
				RecordType:    proto.RecordType_RECORD_TYPE_PROMOTION,
				RecordDate:    "2020-10-17",
				RecordUid:     46,
			},
			{
				RecordDetails: "Completed 18th Combat Mission (Operation Pride of Charlie, Fall 2020)",
				RecordType:    proto.RecordType_RECORD_TYPE_OPERATION,
				RecordDate:    "2020-09-27",
				RecordUid:     13863,
			},
		},
		Awards: []*proto.Award{
			{
				AwardDetails:  "For technical excellence & dedication <est. 2014>",
				AwardName:     "Commendation Medal",
				AwardDate:     "2021-03-01",
				AwardImageUrl: "https://7cav.us/data/awards/ccm.jpg",
				AwardUid:      901,
			},
		},
		JoinDate:          "2014-02-08",
		PromotionDate:     "2020-10-17",
		KeycloakId:        "3f8e2a10-dead-beef-cafe-0123456789ab",
		DiscordId:         "112233445566778899",
		LastForumPostDate: "2026-05-30",
		Mos:               "11B",
		ConsoleGamertag:   "",
	}
}

// seedDoe mirrors the recording seed's sparse profile: unset nested messages
// nil (null on the wire), collections empty ([]), strings "". Relation 2 ↔
// user 8.
func seedDoe() *proto.Profile {
	return &proto.Profile{
		User:            &proto.User{UserId: 8, Username: "John.Doe"},
		Rank:            &proto.Rank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "https://7cav.us/data/roster_ranks/0/22.jpg", RankId: 22},
		RealName:        "John Doe",
		UniformUrl:      "https://7cav.us/data/roster_uniforms/0/2.jpg",
		Roster:          proto.RosterType_ROSTER_TYPE_COMBAT,
		Primary:         nil, // → "primary": null
		Secondaries:     []*proto.Position{},
		Records:         []*proto.Record{},
		Awards:          []*proto.Award{},
		JoinDate:        "2026-01-15",
		ConsoleGamertag: "CavGamer77",
	}
}

// FindProfilesById keys on the MILPAC RELATION ID, mirroring the recording
// seed (and datastores.Mysql: gorm First keys on the milpacs.Profile primary
// key = relation_id). 777 is the injected-outage id for the 500 golden.
func (f *fakeDatastore) FindProfilesById(userIds ...uint64) ([]*proto.Profile, error) {
	if f.findProfilesById != nil {
		return f.findProfilesById(userIds...)
	}
	switch userIds[0] {
	case 1:
		return []*proto.Profile{seedJarvis()}, nil
	case 2:
		return []*proto.Profile{seedDoe()}, nil
	case 777:
		return nil, errOutage
	default:
		return nil, gorm.ErrRecordNotFound
	}
}

func (f *fakeDatastore) FindProfilesByUsername(username string) ([]*proto.Profile, error) {
	if f.findProfilesByUsername != nil {
		return f.findProfilesByUsername(username)
	}
	switch username {
	case "Jarvis.A":
		return []*proto.Profile{seedJarvis()}, nil
	case "John.Doe":
		return []*proto.Profile{seedDoe()}, nil
	default:
		return nil, gorm.ErrRecordNotFound
	}
}

func (f *fakeDatastore) FindProfileByDiscordID(discordId string) (*proto.Profile, error) {
	if f.findProfileByDiscordID != nil {
		return f.findProfileByDiscordID(discordId)
	}
	if discordId == "112233445566778899" {
		return seedJarvis(), nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeDatastore) FindProfileByGamertag(gamertag string) (*proto.Profile, error) {
	if f.findProfileByGamertag != nil {
		return f.findProfileByGamertag(gamertag)
	}
	if gamertag == "CavGamer77" {
		return seedDoe(), nil
	}
	return nil, gorm.ErrRecordNotFound
}

func newStack(t *testing.T) http.Handler {
	t.Helper()
	return rest.New(&fakeDatastore{})
}

// implementedCases names the battery cases the new stack serves today. Each
// fan-out slice (#126–#129) appends its route's cases as it lands; at
// cutover (#134) this list covers the whole battery.
//
// The auth 401-tier cases are here from the tracer on: the middleware
// rejects them BEFORE routing, so they replay exactly against any path —
// including ones whose routes don't exist yet.
var implementedCases = []string{
	"milpacs/ranks",
	"milpacs/profile_by_id_happy",
	"milpacs/profile_by_id_sparse",
	"milpacs/profile_by_id_not_found",
	"milpacs/profile_by_id_zero",
	"milpacs/profile_by_id_parse_error",
	"milpacs/profile_by_id_internal_error",
	"milpacs/profile_by_username_happy",
	"milpacs/profile_by_username_not_found",
	"milpacs/discord_happy",
	"milpacs/discord_not_found",
	"milpacs/gamertag_happy",
	"milpacs/gamertag_not_found",
	"auth/milpacs_missing_header",
	"auth/milpacs_raw_key",
	"auth/milpacs_invalid_key",
	// The 403 scope tier needs a real route to reach (requireScope is
	// per-route, inside the mux) — reachable since the profile-by-id slice.
	"auth/milpacs_wrong_scope",
	"auth/milpacs_no_scopes",
	"auth/tickets_missing_header",
	"auth/tickets_raw_key",
	"auth/tickets_invalid_key",
	"auth/unknown_path_authenticated",
	"auth/unknown_path_unauthenticated",
}

// TestNewStack_GoldenReplay replays every implemented battery case against
// the new stack mounted in-process and compares each response semantically
// against the committed golden — the outer red→green loop of the rewrite.
func TestNewStack_GoldenReplay(t *testing.T) {
	h := newStack(t)

	known := map[string]contract.Case{}
	for _, c := range contract.Cases() {
		known[c.Name] = c
	}

	for _, name := range implementedCases {
		c, ok := known[name]
		require.True(t, ok, "implementedCases entry %q names no battery case", name)
		t.Run(name, func(t *testing.T) {
			got, raw, err := contract.RunCase(h, c)
			require.NoError(t, err)

			want, err := contract.LoadGolden(goldensDir, c.Name)
			require.NoError(t, err)

			diffs := contract.CompareGolden(want, got)
			for _, d := range diffs {
				t.Error(d)
			}
			if len(diffs) > 0 {
				t.Logf("raw response (%d bytes): %.2000s", len(raw), raw)
			}
		})
	}
}

// scopeCases drives the auth tiers the battery only witnesses on
// not-yet-implemented routes (the 403 scope tier needs a real route to
// reach) against the ranks route instead. The committed auth goldens are the
// expectation — identical body/status/headers, only the request path
// differs, because both the scope-failure surface and the 401 tiers are
// route-independent.
func TestNewStack_AuthTiersOnRanksRoute(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		golden string        // committed golden carrying the expected response
		auth   contract.Auth // tier to present to the ranks route
	}{
		{"auth/milpacs_wrong_scope", contract.AuthReadTickets},
		{"auth/milpacs_no_scopes", contract.AuthNoScopes},
		{"auth/milpacs_missing_header", contract.AuthNone},
		{"auth/milpacs_raw_key", contract.AuthRawKey},
		{"auth/milpacs_invalid_key", contract.AuthInvalidKey},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			want, err := contract.LoadGolden(goldensDir, tc.golden)
			require.NoError(t, err)
			require.Equal(t, tc.auth, want.Auth, "tier drifted from the recorded golden")

			c := contract.Case{
				Name:   tc.golden,
				Method: http.MethodGet,
				Path:   "/api/v1/milpacs/ranks",
				Auth:   tc.auth,
				Notes:  "synthesized: the recorded tier replayed against the ranks route",
			}
			got, raw, err := contract.RunCase(h, c)
			require.NoError(t, err)

			// Same response, different request path: rewrite the recorded
			// request metadata to the synthesized case before comparing.
			want.Path = c.Path

			diffs := contract.CompareGolden(want, got)
			for _, d := range diffs {
				t.Error(d)
			}
			if len(diffs) > 0 {
				t.Logf("raw response (%d bytes): %.2000s", len(raw), raw)
			}
		})
	}
}

// TestNewStack_GzipRoundTrip pins the gzip layer's place in the chain with a
// real round-trip: a 200 with Accept-Encoding: gzip must decompress back to
// the ranks payload through an intact trailer.
func TestNewStack_GzipRoundTrip(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))
	assert.Empty(t, rr.Result().Header.Get("Content-Length"),
		"stale uncompressed Content-Length must be stripped")

	zr, err := gzip.NewReader(rr.Body)
	require.NoError(t, err, "body must be a valid gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.Contains(t, string(decoded), `"rankFull":"Major General"`)
}

// Gzip sits OUTSIDE the mux (PRD order): ERROR responses compress too, not
// just handler 200s — the 403 scope tier is written inside the mux (per-route
// requireScope → writeError), so a gzipped 403 that decompresses back to the
// frozen JSON pins the gzip-outside-mux half of the chain-order criterion.
func TestNewStack_GzipErrorResponseRoundTrip(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_ticketskey") // read:tickets ≠ read
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))

	zr, err := gzip.NewReader(rr.Body)
	require.NoError(t, err, "body must be a valid gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, string(decoded))
}

// Auth sits OUTSIDE gzip in the chain (PRD order): a 401 must come back
// uncompressed even when the client advertises gzip — exactly as the old
// stack behaves and as the plain-text golden tier implies.
func TestNewStack_401IsNeverGzipped(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Empty(t, rr.Result().Header.Get("Content-Encoding"))
	assert.True(t, strings.HasPrefix(rr.Body.String(), "Unauthorized"),
		"401 body must be the plain-text tier, not a gzip stream")
}

// The datastore failing must surface as the frozen Internal error shape
// through the choke point (message text mirrors the old handler verbatim).
func TestNewStack_RanksDatastoreOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, io.ErrUnexpectedEOF
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":13,"message":"error fetching ranks: unexpected EOF","details":[]}`, rr.Body.String())
}

// A datastore failure on each profile lookup must surface as the frozen
// Internal error shape with the handler-specific wrapped message (the by-id
// variant is golden-pinned via profile_by_id_internal_error; the corpus has
// no outage cases for the other three, so these pin them).
func TestNewStack_ProfileLookupOutagesAreInternalJSON(t *testing.T) {
	outage := func() ([]*proto.Profile, error) { return nil, io.ErrUnexpectedEOF }
	h := rest.New(&fakeDatastore{
		findProfilesByUsername: func(string) ([]*proto.Profile, error) { return outage() },
		findProfileByDiscordID: func(string) (*proto.Profile, error) { return nil, io.ErrUnexpectedEOF },
		findProfileByGamertag:  func(string) (*proto.Profile, error) { return nil, io.ErrUnexpectedEOF },
	})

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/milpacs/profile/username/Jarvis.A", "fetch profile by username: unexpected EOF"},
		{"/api/v1/milpac/discord/112233445566778899", "fetch profile by discord id: unexpected EOF"},
		{"/api/v1/milpac/gamertag/CavGamer77", "fetch profile by gamertag: unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// --- Profile query-binding quirks (PRD #112, request-side leniency) --------
//
// Unbound message fields remain query-bindable on profile routes: the old
// gateway bound any ProfileRequest field NOT consumed by the path template
// from the query string (filter_MilpacService_GetProfile_0/_1 in the
// generated gateway code), and the old handler checked username BEFORE
// user_id — so ?username= on the by-id route silently overrides the path
// value. Frozen as-is by the PRD; no goldens witness it (the corpus records
// path-only requests), so these new-stack tests pin it instead.

func TestNewStack_ByIdRoute_UsernameQueryOverridesPathId(t *testing.T) {
	h := newStack(t)

	// Path id 2 is John.Doe; the username query must win and return Jarvis.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/2?username=Jarvis.A", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
	assert.Contains(t, rr.Body.String(), `"userId":"3"`)
}

func TestNewStack_ByIdRoute_UsernameQueryPrecedesZeroIdGuard(t *testing.T) {
	h := newStack(t)

	// The old handler checks username first: id 0 with a username query is a
	// successful username lookup, not the zero-id 400.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/0?username=Jarvis.A", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
}

func TestNewStack_ByIdRoute_UnknownUsernameQueryIs404NamingUsername(t *testing.T) {
	h := newStack(t)

	// Precedence holds on the error path too: the 404 names the username,
	// even though the path id would have resolved.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/1?username=Ghost.User", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.JSONEq(t, `{"code":5,"message":"no profile found for username: Ghost.User","details":[]}`, rr.Body.String())
}

func TestNewStack_ByIdRoute_UnknownQueryParamsIgnored(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/1?totally_unknown=1&alsoUnknown=x", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
}

func TestNewStack_UsernameRoute_UserIdQueryBindsButPathUsernameWins(t *testing.T) {
	h := newStack(t)

	// user_id is the username route's query-bindable field; the handler's
	// username-first precedence makes a valid value invisible.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?user_id=999", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
}

func TestNewStack_UsernameRoute_MalformedUserIdQueryIs400(t *testing.T) {
	h := newStack(t)

	// Binding still parses the value (the old gateway 400'd before the
	// handler ran) — camelCase spelling accepted, proto field name in the
	// message, parse-error text leaked, exactly the gateway's wording.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?userId=abc", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"parsing field \"user_id\": strconv.ParseUint: parsing \"abc\": invalid syntax","details":[]}`, rr.Body.String())
}

func TestNewStack_ByIdRoute_RepeatedUsernameQueryIs400(t *testing.T) {
	h := newStack(t)

	// Singular fields reject repeated values (gateway behavior, message
	// shape preserved).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/2?username=a&username=b", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"too many values for field \"username\": a, b","details":[]}`, rr.Body.String())
}

// --- Wrong-method contract (human ruling on review Critical 1, #125) -------
//
// Wrong-method on an existing route returns 405 Method Not Allowed with an
// Allow: GET, HEAD header; HEAD stays a supported read verb (works on valid
// routes, returns no body); 404 is reserved for genuinely unknown routes
// only.
//
// Rationale (ruled, recorded in the PRD #112 enumerated-breaks list): this
// API is read-only by published contract — no write endpoints exist and the
// docs say so. Any POST/PATCH/etc. was never a supported call, so there is no
// legitimate consumer whose error-handling the change can break; the
// behavior-neutral cutover guarantee covers the GET/HEAD surface actually
// published, which is untouched. That removes the only reason to shim the old
// stack's 501 and frees the correct code: 405 (vs the un-pinned new code's
// 404) preserves the useful "route exists, method doesn't" signal, and Allow
// gives a mistaken-but-legitimate client the answer in one round trip. Public
// docs mean 405 leaks nothing. When write endpoints arrive, those routes
// advertise their own Allow and inherit this default — a forward-compatible
// pattern, not a one-off.
//
// These pins are deliberately NEW-STACK-ONLY (plain tests here, not battery
// cases): the golden corpus replays against the old stack too, and the old
// stack answers 501 — a shared case would poison the old-stack suite. The 405
// is net-new decided behavior, not recorded-from-old-stack behavior.

func TestNewStack_WrongMethodOnKnownRouteIs405WithAllow(t *testing.T) {
	h := newStack(t)

	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/milpacs/ranks", nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
			assert.Equal(t, "GET, HEAD", rr.Header().Get("Allow"),
				"Allow must answer the client in one round trip")
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			// Body verbatim from the old stack's wrong-method response (code
			// 12 Unimplemented): only the HTTP status (501→405) and the Allow
			// header change — consumers matching on the body see no
			// difference.
			assert.JSONEq(t, `{"code":12,"message":"Method Not Allowed","details":[]}`, rr.Body.String())
		})
	}
}

func TestNewStack_WrongMethodOnUnknownRouteStays404(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/does/not/exist", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code, "404 is reserved for genuinely unknown routes")
	assert.Empty(t, rr.Header().Get("Allow"), "an unknown route has no methods to advertise")
	assert.JSONEq(t, `{"code":5,"message":"Not Found","details":[]}`, rr.Body.String())
}

// Auth runs BEFORE routing (chain order), so a wrong-method request without
// credentials is answered by the auth tier, not the method fallback: 401
// scheme-tier plain text, no Allow header. The 405 only exists for callers
// who already authenticated.
func TestNewStack_WrongMethodWithoutCredsIs401NotAllow(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/milpacs/ranks", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code, "auth precedes method handling")
	assert.Empty(t, rr.Header().Get("Allow"), "no method advertisement to unauthenticated callers")
	assert.Equal(t, "Unauthorized: expected 'Authorization: Bearer <key>' header",
		strings.TrimSpace(rr.Body.String()), "scheme-tier 401 body, frozen")
}

// HEAD is a supported read verb: Go's mux matches HEAD against GET patterns
// (kept deliberately), and net/http suppresses the response body for HEAD at
// the server. The body suppression lives in the real server's ResponseWriter
// — a ResponseRecorder would show a body — so this test observes through a
// live httptest.Server.
func TestNewStack_HEADOnKnownRouteIs200WithNoBody(t *testing.T) {
	srv := httptest.NewServer(rest.New(&fakeDatastore{}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodHead, srv.URL+"/api/v1/milpacs/ranks", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Empty(t, body, "net/http suppresses the body on HEAD responses")
}

// An empty rank catalog must serialize as {"ranks":[]} — the allocation
// discipline (empty collections are [], never null) the goldens can only
// witness on populated routes.
func TestNewStack_EmptyRanksIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, nil
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"ranks":[]}`, strings.TrimSpace(rr.Body.String()))
}
