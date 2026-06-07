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
	"github.com/7cav/api/referencecache"
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
	validateApiKey         func(rawKey string) (*datastores.ApiKeyResult, error)
	findProfilesById       func(userIds ...uint64) ([]*proto.Profile, error)
	findProfilesByUsername func(username string) ([]*proto.Profile, error)
	findProfileByDiscordID func(discordId string) (*proto.Profile, error)
	findProfileByGamertag  func(gamertag string) (*proto.Profile, error)

	// Positions/AWOL overrides (#128; seeded defaults live in
	// fake_positions_test.go); a test sets one to inject an outage.
	findAllPositionGroups  func() ([]*proto.PositionGroup, error)
	findProfilesByPosition func(positionQuery string) (*proto.LiteRoster, error)
	findAwol               func() ([]*proto.Awol, error)

	// Tickets overrides (seeded defaults live in fake_tickets_test.go); a
	// test sets one to inject an outage or observe the bound filter.
	listTickets            func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error)
	getTicket              func(ticketID uint32) (*proto.Ticket, error)
	getTicketByRef         func(ref string) (*proto.Ticket, error)
	getTicketFirstMessages func(ticketID uint32, n int, includeHidden bool) ([]*proto.Message, uint32, error)
	listTicketMessages     func(ticketID uint32, afterCursor string, perPage uint32, includeHidden bool) ([]*proto.Message, string, bool, error)
	listCategories         func() ([]*proto.Category, error)

	// Roster overrides (seeded defaults live in fake_rosters_test.go).
	findRosterByType           func(proto.RosterType) (*proto.Roster, error)
	findLiteRosterByType       func(proto.RosterType) (*proto.LiteRoster, error)
	findS1UniformsRosterByType func(proto.RosterType) (*proto.S1UniformsRoster, error)

	// lastRC records the TicketReferenceCache the handlers handed the most
	// recent rc-consuming datastore call — the identity pin asserts it IS the
	// cache rest.New received (no copy, no substitute).
	lastRC datastores.TicketReferenceCache
}

// stubReferenceCache is the explicit no-op datastores.TicketReferenceCache
// the new-stack tests mount: rest.New refuses nil (a nil cache is a
// guaranteed panic on the first tickets request against the real datastore),
// and the fake datastore never consults it. A fresh pointer per test keeps
// the identity pin honest.
type stubReferenceCache struct{}

func (*stubReferenceCache) StatusName(uint32) string                       { return "" }
func (*stubReferenceCache) PriorityName(uint32) string                     { return "" }
func (*stubReferenceCache) PrefixName(uint32) string                       { return "" }
func (*stubReferenceCache) Category(uint32) *referencecache.CategoryRecord { return nil }
func (*stubReferenceCache) CategoryAncestors(uint32) []uint32              { return nil }
func (*stubReferenceCache) CategoryTree() []*referencecache.CategoryRecord { return nil }
func (*stubReferenceCache) ExpandSubtree(ids []uint32) []uint32            { return ids }

func (f *fakeDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	if f.validateApiKey != nil {
		return f.validateApiKey(rawKey)
	}
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
	return rest.New(&fakeDatastore{}, &stubReferenceCache{})
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
	"milpacs/position_groups",
	"milpacs/awol",
	"position/search_happy",
	"position/search_empty_result",
	"position/search_multi_segment",
	"position/search_trailing_slash",
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
	"tickets/categories",
	"tickets/list_default",
	"tickets/list_repeated_status_filter",
	"tickets/list_state_filter",
	"tickets/list_category_includes_subcategories",
	"tickets/list_category_exclude_subcategories",
	"tickets/list_starter_filter",
	"tickets/list_per_page_snake",
	"tickets/list_per_page_camel",
	"tickets/list_page_two",
	"tickets/list_invalid_cursor_camel",
	"tickets/list_unknown_param_ignored",
	"tickets/get_by_id_happy",
	"tickets/get_by_id_not_found",
	"tickets/get_by_id_parse_error",
	"tickets/get_by_ref_happy",
	"tickets/get_by_ref_not_found",
	"tickets/messages_default",
	"tickets/messages_per_page_snake",
	"tickets/messages_per_page_camel",
	"tickets/messages_page_two",
	"tickets/messages_invalid_cursor_snake",
	"tickets/messages_unknown_ticket",
	"tickets/messages_parse_error",
	"roster/combat_by_name",
	"roster/combat_by_number",
	"roster/reserve_empty",
	"roster/unspecified_by_name",
	"roster/unspecified_by_number",
	"roster/bogus_enum",
	"roster/internal_error",
	"roster/unknown_query_param_ignored",
	"roster/lite_combat_by_name",
	"roster/lite_combat_by_number",
	"roster/lite_reserve_empty",
	"roster/lite_unspecified",
	"s1/uniforms_combat_by_name",
	"s1/uniforms_combat_by_number",
	"s1/uniforms_unspecified",
	"auth/milpacs_missing_header",
	"auth/milpacs_raw_key",
	"auth/milpacs_invalid_key",
	// The 403 scope tier needs a real route to reach (requireScope is
	// per-route, inside the mux) — reachable since the profile-by-id slice.
	"auth/milpacs_wrong_scope",
	"auth/milpacs_no_scopes",
	"auth/tickets_missing_header",
	"auth/tickets_wrong_scope",
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
	}}, &stubReferenceCache{})

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
	}, &stubReferenceCache{})

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

// A datastore returning an EMPTY slice (or nil profile) with a nil error
// violates the documented invariant (non-empty slice on nil error) — possible
// only from a future datastore bug, never the real one, so no golden can
// witness it. It must surface as the frozen Internal shape, not a panic or a
// fabricated sparse 200.
func TestNewStack_EmptyProfileSliceWithNilErrorIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findProfilesById:       func(...uint64) ([]*proto.Profile, error) { return []*proto.Profile{}, nil },
		findProfilesByUsername: func(string) ([]*proto.Profile, error) { return nil, nil },
	}, &stubReferenceCache{})

	for _, path := range []string{
		"/api/v1/milpacs/profile/id/1",
		"/api/v1/milpacs/profile/username/Jarvis.A",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"datastore returned no profiles","details":[]}`, rr.Body.String())
		})
	}
}

func TestNewStack_NilProfileWithNilErrorIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findProfileByDiscordID: func(string) (*proto.Profile, error) { return nil, nil },
		findProfileByGamertag:  func(string) (*proto.Profile, error) { return nil, nil },
		// A non-empty slice carrying a nil element hits the same guard.
		findProfilesById: func(...uint64) ([]*proto.Profile, error) { return []*proto.Profile{nil}, nil },
	}, &stubReferenceCache{})

	for _, path := range []string{
		"/api/v1/milpac/discord/112233445566778899",
		"/api/v1/milpac/gamertag/CavGamer77",
		"/api/v1/milpacs/profile/id/1",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"datastore returned no profile","details":[]}`, rr.Body.String())
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
	// username-first precedence makes a valid value invisible — under either
	// spelling (the camelCase one exercises the dual-spelling leniency on the
	// parsed-but-invisible path, not just the error path).
	for _, query := range []string{"user_id=999", "userId=999"} {
		t.Run(query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?"+query, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
		})
	}
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

// Present-but-EMPTY ?user_id= on the username route is a 400: the old gateway
// parsed every PRESENT value — runtime/query.go has no empty-value guard, so
// "" went straight into strconv.ParseUint and errored. Present and non-empty
// are distinct states; treating present-empty as absent would be a silent 200.
func TestNewStack_UsernameRoute_EmptyUserIdQueryIs400(t *testing.T) {
	h := newStack(t)

	for _, spelling := range []string{"user_id", "userId"} {
		t.Run(spelling, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?"+spelling+"=", nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code)
			assert.JSONEq(t, `{"code":3,"message":"parsing field \"user_id\": strconv.ParseUint: parsing \"\": invalid syntax","details":[]}`, rr.Body.String())
		})
	}
}

// The asymmetry, pinned: present-but-empty ?username= on the by-id route IS a
// 200 — username is a STRING field, so the gateway bound "" without error and
// the old handler treated "" as unset, falling through to the path id.
func TestNewStack_ByIdRoute_EmptyUsernameQueryFallsThroughToPathId(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/2?username=", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"username":"John.Doe"`, "empty username binds as unset; path id 2 resolves")
}

// Cross-spelling: ?user_id=1&userId=2 is NOT a repeated-value error — the old
// gateway processed each url.Values key independently (each parsed, each set
// the field, last-iterated-wins nondeterministically, no error). Every value
// must parse; a bad one 400s with the gateway's parsing-field text regardless
// of order. When all parse, the camelCase value wins deterministically — a
// ruling standing in for the old map-order nondeterminism (converged with
// #129's query binder) — and is invisible anyway under username precedence.
func TestNewStack_UsernameRoute_CrossSpellingUserIdQuery(t *testing.T) {
	h := newStack(t)

	// The reference: the path-username profile with no query at all.
	ref := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A", nil)
	ref.Header.Set("Authorization", "Bearer cav7_readkey")
	refRec := httptest.NewRecorder()
	h.ServeHTTP(refRec, ref)
	require.Equal(t, http.StatusOK, refRec.Code)

	t.Run("both_parse_200_snake_first", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?user_id=1&userId=2", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, refRec.Body.String(), rr.Body.String(),
			"bound user_id is invisible under username precedence — response equals the path-username profile")
	})

	t.Run("both_parse_200_camel_first", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?userId=2&user_id=1", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, refRec.Body.String(), rr.Body.String())
	})

	t.Run("bad_snake_400_snake_first", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?user_id=abc&userId=5", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		assert.JSONEq(t, `{"code":3,"message":"parsing field \"user_id\": strconv.ParseUint: parsing \"abc\": invalid syntax","details":[]}`, rr.Body.String())
	})

	t.Run("bad_snake_400_camel_first", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?userId=5&user_id=abc", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		assert.JSONEq(t, `{"code":3,"message":"parsing field \"user_id\": strconv.ParseUint: parsing \"abc\": invalid syntax","details":[]}`, rr.Body.String())
	})
}

// Same-key repetition stays the deterministic gateway error (per-key check:
// runtime/query.go errored whenever ONE key carried multiple values for a
// singular field, citing the proto field name and that key's values).
func TestNewStack_UsernameRoute_RepeatedUserIdQueryIs400(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		name  string
		query string
	}{
		{"snake_repeated", "user_id=1&user_id=2"},
		{"camel_repeated", "userId=1&userId=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code)
			assert.JSONEq(t, `{"code":3,"message":"too many values for field \"user_id\": 1, 2","details":[]}`, rr.Body.String())
		})
	}
}

// --- Bracket-key folding (gateway DefaultQueryParser rewrite, frozen) -------
//
// The old gateway rewrote any query key matching ^(.*)\[(.*)\]$ to its base
// key with the bracket CONTENT prepended as an extra value (grpc-gateway
// v2.29.0 runtime/query.go valuesKeyRegexp): ?user_id[0]=1 became key
// "user_id", values ["0","1"] — which then tripped the singular-field
// too-many-values check. Verified against the real old stack in-process on
// both routes below. A bracket key whose base matches no bindable field is
// just another unknown parameter — ignored. Semantics converged with #129's
// query binder (same fold, implemented per-branch).
func TestNewStack_ProfileRoutes_BracketKeyFoldsIntoField(t *testing.T) {
	h := newStack(t)

	t.Run("username_route_user_id_bracket_is_too_many_values", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/username/Jarvis.A?user_id[0]=1", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		// Folded group, bracket content first: ["0","1"].
		assert.JSONEq(t, `{"code":3,"message":"too many values for field \"user_id\": 0, 1","details":[]}`, rr.Body.String())
	})

	t.Run("by_id_route_username_bracket_is_too_many_values", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/2?username[x]=y", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		assert.JSONEq(t, `{"code":3,"message":"too many values for field \"username\": x, y","details":[]}`, rr.Body.String())
	})

	t.Run("unmatched_bracket_key_ignored", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/1?junk[0]=x", nil)
		req.Header.Set("Authorization", "Bearer cav7_readkey")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code, "bracket key folding to a non-bindable base is unknown-param leniency")
		assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
	})
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

// Malformed query SYNTAX is a 400 on the profile by-id and by-username routes:
// the old gateway's generated handlers called req.ParseForm() and wrapped its
// error as InvalidArgument "%v" (request_MilpacService_GetProfile_0/_1) — even
// when the malformed pair was an unknown parameter. r.URL.Query() would drop
// the bad pair silently; the explicit ParseQuery guard preserves the 400.
func TestNewStack_ProfileRoutes_MalformedQuerySyntaxIs400(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		name string
		path string
		want string
	}{
		{"by_id_bad_escape", "/api/v1/milpacs/profile/id/1?username=%zz", `invalid URL escape \"%zz\"`},
		{"by_id_semicolon", "/api/v1/milpacs/profile/id/1?a=1;b=2", `invalid semicolon separator in query`},
		{"by_username_bad_escape", "/api/v1/milpacs/profile/username/Jarvis.A?user_id=%zz", `invalid URL escape \"%zz\"`},
		{"by_username_semicolon", "/api/v1/milpacs/profile/username/Jarvis.A?a=1;b=2", `invalid semicolon separator in query`},
		// The malformed pair being an UNKNOWN param changed nothing: ParseForm
		// ran before any field filtering.
		{"by_id_unknown_param_bad_escape", "/api/v1/milpacs/profile/id/1?junk=%zz", `invalid URL escape \"%zz\"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusBadRequest, rr.Code)
			assert.JSONEq(t, `{"code":3,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// The discord and gamertag routes must NOT gain the ParseForm guard: their
// generated gateway handlers never called ParseForm (verified against
// proto/milpacs.pb.gw.go — single path-bound field, no query binding), so
// malformed query syntax fell through to the lookup.
func TestNewStack_ConnectedAccountRoutes_MalformedQuerySyntaxIgnored(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/milpac/discord/112233445566778899?junk=%zz",
		"/api/v1/milpac/gamertag/CavGamer77?junk=%zz",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code, "old gateway never ParseForm'd these routes")
		})
	}
}

// --- Path-id parse base (gateway base-0 quirk, frozen) ----------------------
//
// The old gateway's runtime.Uint64 is strconv.ParseUint(val, 0, 64) — BASE 0
// (grpc-gateway v2.29.0 runtime/convert.go), so the path id accepts Go
// integer-literal prefixes (0x hex, 0b binary, leading-0 octal) and digit
// underscores. Verified against the real old stack in-process: /profile/id/0x1
// resolved relation 1, /profile/id/010 resolved relation EIGHT (octal!),
// /profile/id/09 was a 400 (octal with a 9 — invalid syntax), /profile/id/1_0
// resolved relation 10. Wrong-profile-resolution risk if the new stack parses
// base 10; frozen as-is, base 0.
//
// The not-found message names the PARSED number, not the raw path text — the
// old handler built it from the already-bound request field.
func TestNewStack_ByIdRoute_PathIdParsesBaseZero(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		name     string
		path     string
		wantCode int
		check    func(t *testing.T, body string)
	}{
		{"hex_0x1_resolves_relation_1", "/api/v1/milpacs/profile/id/0x1", http.StatusOK,
			func(t *testing.T, body string) {
				assert.Contains(t, body, `"username":"Jarvis.A"`)
				assert.Contains(t, body, `"userId":"3"`)
			}},
		{"binary_0b1_resolves_relation_1", "/api/v1/milpacs/profile/id/0b1", http.StatusOK,
			func(t *testing.T, body string) {
				assert.Contains(t, body, `"username":"Jarvis.A"`)
			}},
		{"octal_010_is_relation_8", "/api/v1/milpacs/profile/id/010", http.StatusNotFound,
			func(t *testing.T, body string) {
				// The fake seeds no relation 8: the not-found shape naming the
				// PARSED number 8 (not 10, not "010") proves base-0 + the
				// parsed-value message in one observation.
				assert.JSONEq(t, `{"code":5,"message":"no profile found for user ID: 8","details":[]}`, body)
			}},
		{"underscore_1_0_is_relation_10", "/api/v1/milpacs/profile/id/1_0", http.StatusNotFound,
			func(t *testing.T, body string) {
				assert.JSONEq(t, `{"code":5,"message":"no profile found for user ID: 10","details":[]}`, body)
			}},
		{"octal_09_is_invalid_syntax_400", "/api/v1/milpacs/profile/id/09", http.StatusBadRequest,
			func(t *testing.T, body string) {
				// Base 0 reads the leading 0 as octal; 9 is no octal digit.
				assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: user_id, error: strconv.ParseUint: parsing \"09\": invalid syntax","details":[]}`, body)
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, tc.wantCode, rr.Code)
			tc.check(t, rr.Body.String())
		})
	}
}

// Path parse precedes the username-override: a malformed path id 400s with
// the gateway's type-mismatch tier even when a valid ?username= is present —
// the generated handler parsed pathParams before ParseForm/query binding.
func TestNewStack_ByIdRoute_MalformedPathIdBeatsUsernameQuery(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/abc?username=Jarvis.A", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: user_id, error: strconv.ParseUint: parsing \"abc\": invalid syntax","details":[]}`, rr.Body.String())
}

// Combined malformed path AND malformed query syntax: the PATH error wins —
// the generated gateway handler parsed pathParams before ParseForm, so the
// type-mismatch tier answers even when the query would also 400. Pinned
// (verified against the real old stack in-process).
func TestNewStack_ByIdRoute_MalformedPathBeatsMalformedQuerySyntax(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/abc?username=%zz", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: user_id, error: strconv.ParseUint: parsing \"abc\": invalid syntax","details":[]}`, rr.Body.String())
}

// On the by-id route user_id is PATH-bound, so the gateway's field filter
// (filter_MilpacService_GetProfile_0) excluded it from query binding — under
// BOTH spellings (the filter keyed on the resolved field, not the query-key
// text). A ?user_id=abc / ?userId=abc that would 400 on the username route is
// just an ignored unknown parameter here.
func TestNewStack_ByIdRoute_UserIdQueryIgnoredBecausePathFiltered(t *testing.T) {
	h := newStack(t)

	for _, query := range []string{"user_id=abc", "userId=abc"} {
		t.Run(query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/1?"+query, nil)
			req.Header.Set("Authorization", "Bearer cav7_readkey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code, "path-filtered field: the query spelling binds nothing")
			assert.Contains(t, rr.Body.String(), `"username":"Jarvis.A"`)
		})
	}
}

// --- Scope-vs-binding ordering (ruled cutover break) -------------------------
//
// The OLD stack answered binding-400s before scope-403s: RequireScope lived
// inside the RPC bodies, after the gateway had already parsed path params —
// a wrong-scope key with a malformed path id got the 400. The new stack's
// uniform tier order (401 → 403 → route semantics) answers the 403 first.
// RULING: keep 403-first — a deliberate, documented cutover break (same
// precedent tier as the 405 ruling on review Critical 1/#125, recorded in
// PRD #112's enumerated breaks; ratified in the PR body). The sibling branch
// (#129) documents the same ruling at its scope gate. This pin is
// new-stack-only by design: no golden can witness it without poisoning the
// old-stack replay.
func TestNewStack_WrongScopeBeatsMalformedPath_RuledBreak(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/profile/id/abc", nil)
	req.Header.Set("Authorization", "Bearer cav7_ticketskey") // read:tickets ≠ read
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code,
		"403 answers before the path-binding 400 — ruled, NOT old-stack parity (old stack: 400 first)")
	assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
}

// The scope gate is witnessed PER profile route: a ticket-scoped key
// (read:tickets, not read) must 403 with the frozen body on each of the four.
// The golden tier only witnesses one path; this loop proves no route was
// registered without its requireScope gate.
func TestNewStack_AllProfileRoutes403UnderTicketScopedKey(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/milpacs/profile/id/1",
		"/api/v1/milpacs/profile/username/Jarvis.A",
		"/api/v1/milpac/discord/112233445566778899",
		"/api/v1/milpac/gamertag/CavGamer77",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer cav7_ticketskey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusForbidden, rr.Code)
			assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
		})
	}
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

// The fallback's GET probe must apply the SAME narrowing the
// {ticket_id}/{sub} dispatcher does: "messages" is the only known
// sub-resource. Without it, POST /tickets/42/bogus would 405 ("route
// exists") while GET on the same path 404s — the probe claiming a route the
// GET surface denies, violating the 405 ruling's own principle.
func TestNewStack_WrongMethodOnUnknownTicketSubResourceStays404(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets/42/bogus", nil)
	req.Header.Set("Authorization", "Bearer cav7_ticketskey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code, "GET on this path is a 404; the probe must agree")
	assert.Empty(t, rr.Header().Get("Allow"))
	assert.JSONEq(t, `{"code":5,"message":"Not Found","details":[]}`, rr.Body.String())
}

// The narrowing must not over-correct: the messages route itself is real,
// so wrong-method there keeps the 405 + Allow.
func TestNewStack_WrongMethodOnTicketMessagesIs405WithAllow(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tickets/42/messages", nil)
	req.Header.Set("Authorization", "Bearer cav7_ticketskey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, "GET, HEAD", rr.Header().Get("Allow"))
	assert.JSONEq(t, `{"code":12,"message":"Method Not Allowed","details":[]}`, rr.Body.String())
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
	srv := httptest.NewServer(rest.New(&fakeDatastore{}, &stubReferenceCache{}))
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

// --- Reference-cache wiring (#129 review F4) --------------------------------

// rest.New must refuse a nil TicketReferenceCache loudly at construction: a
// nil cache is a guaranteed panic on the first tickets request against the
// real datastore, with no recovery middleware in the chain yet — failing the
// wiring beats failing the first caller.
func TestNewStack_NilReferenceCachePanics(t *testing.T) {
	assert.PanicsWithValue(t,
		"rest.New: nil TicketReferenceCache — pass the refreshed referencecache.Cache (see #134)",
		func() { rest.New(&fakeDatastore{}, nil) })
}

// The rc handed to rest.New is the one reaching the datastore methods —
// pointer identity, not just non-nil: a handler quietly substituting its own
// cache would pass every other test.
func TestNewStack_ReferenceCacheReachesDatastoreByIdentity(t *testing.T) {
	rc := &stubReferenceCache{}
	f := &fakeDatastore{}
	h := rest.New(f, rc)

	for _, path := range []string{
		"/api/v1/tickets",
		"/api/v1/tickets/42",
		"/api/v1/tickets/ref/MF1UI9HE",
		"/api/v1/tickets/categories",
	} {
		f.lastRC = nil
		rr := ticketsGet(t, h, path)
		require.Equal(t, http.StatusOK, rr.Code, path)
		assert.Same(t, rc, f.lastRC, "%s: the rc reaching the datastore must be the one rest.New received", path)
	}
}

// An empty rank catalog must serialize as {"ranks":[]} — the allocation
// discipline (empty collections are [], never null) the goldens can only
// witness on populated routes.
func TestNewStack_EmptyRanksIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllRanks: func() ([]*proto.RankExpanded, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"ranks":[]}`, strings.TrimSpace(rr.Body.String()))
}

// --- Encoded path separators (deliberate break, PRD #112, ruled 2026-06-06) -
//
// The old gateway percent-decoded paths BEFORE routing, so %2F was
// routing-equivalent to a literal slash; ServeMux matches the escaped path,
// so %2F stays data within a single segment and binds into the path value —
// the non-wildcard sibling of the #128 search-segment break. Pinned
// new-stack-only: the corpus replays the old stack, which produces the
// pre-break behavior (roster generic 404, username generic 404, tickets
// messages 200 via decode-before-route, ticket_id parse stopping at "ref").
// The unknown-path case rides along as the family's no-divergence member —
// the fallback 404 is identical on both stacks.
func TestNewStack_EncodedSlashStaysSegmentData(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		path, key  string
		wantStatus int
		wantBody   string
	}{
		{"/api/v1/roster/combat%2Ffoo", "cav7_readkey", http.StatusBadRequest,
			`{"code":3,"message":"type mismatch, parameter: roster, error: combat/foo is not valid","details":[]}`},
		{"/api/v1/milpacs/profile/username/john%2Fdoe", "cav7_readkey", http.StatusNotFound,
			`{"code":5,"message":"no profile found for username: john/doe","details":[]}`},
		{"/api/v1/tickets/1%2Fmessages", "cav7_ticketskey", http.StatusBadRequest,
			`{"code":3,"message":"type mismatch, parameter: ticket_id, error: strconv.ParseUint: parsing \"1/messages\": invalid syntax","details":[]}`},
		{"/api/v1/tickets/ref%2Fmessages", "cav7_ticketskey", http.StatusBadRequest,
			`{"code":3,"message":"type mismatch, parameter: ticket_id, error: strconv.ParseUint: parsing \"ref/messages\": invalid syntax","details":[]}`},
		{"/api/v1/foo%2Fbar", "cav7_readkey", http.StatusNotFound,
			`{"code":5,"message":"Not Found","details":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := positionsGet(t, h, tc.path, tc.key)

			require.Equal(t, tc.wantStatus, rr.Code)
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			assert.JSONEq(t, tc.wantBody, rr.Body.String())
		})
	}
}
