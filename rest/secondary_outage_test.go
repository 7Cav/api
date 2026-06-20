package rest_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/rest"
	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Outage-classification pins for issues #154/#155 (reversed ruling 2026-06-20):
// a secondary-position lookup OR forum-post-date aggregation that fails mid
// request now propagates so the route answers the contract 500 outage path,
// not a degraded 200 with a fabricated blank position / blanked
// lastForumPostDate. The corpus only witnesses happy paths, so these pin the
// outage path new-stack-only. The datastore-side propagation itself is pinned
// at the harness level (datastores/outages_harness_test.go); these pin the
// HTTP classification the propagated error must receive.

// secondaryOutageGet drives one authenticated GET against the mounted stack.
func secondaryOutageGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// #154 — a secondary-position lookup failure on ANY of the four single-profile
// routes must surface as the contract 500, NOT a 404. All four handlers share
// the identical errors.Is(gorm.ErrRecordNotFound)→404 else→500 branch and all
// reach collectSecondaryPositions via generateProtoProfile, so the guard must
// hold on every one — not just by-id.
//
// The datastore wraps the secondary First() error with %v (see
// collectSecondaryPositions): masking the gorm not-found sentinel keeps a
// vanished secondary row — while the MEMBER still resolves — on the outage
// path, instead of the single-profile handler's 404 "no profile found" branch
// mislabeling the present member as missing. This is the precise trap the %v
// (not %w) choice avoids. Each fake reproduces the post-wrap error shape the
// datastore now returns; each route's own internal-error prefix is asserted in
// full, so a %w regression (which would flip every row to 404) flips these to
// red. The wrapped-error guard below proves the shape is the actual %v one.
func TestNewStack_SecondaryPositionLookupFailureIsInternalNot404(t *testing.T) {
	// The shape datastores.collectSecondaryPositions returns: the gorm
	// sentinel's MESSAGE preserved but its IDENTITY masked (%v, not %w), so
	// errors.Is(err, gorm.ErrRecordNotFound) is false. If the datastore
	// regressed to %w, this guard's premise breaks and the route 404s a present
	// member — exactly what the per-route 500 assertions below would catch.
	wrapped := fmt.Errorf("collect secondary positions: lookup secondary position %q: %v", "20", gorm.ErrRecordNotFound)
	require.False(t, errors.Is(wrapped, gorm.ErrRecordNotFound),
		"the propagated secondary-lookup error must NOT alias gorm.ErrRecordNotFound, or the single-profile routes 404 a present member")

	// One fake serving every single-profile method the wrapped secondary error;
	// each route is driven independently so a missing per-route 500 guard fails
	// its own row.
	fake := &fakeDatastore{
		findProfilesById:       func(...uint64) ([]*types.Profile, error) { return nil, wrapped },
		findProfilesByUsername: func(string) ([]*types.Profile, error) { return nil, wrapped },
		findProfileByDiscordID: func(string) (*types.Profile, error) { return nil, wrapped },
		findProfileByGamertag:  func(string) (*types.Profile, error) { return nil, wrapped },
	}
	h := rest.New(fake, &stubReferenceCache{})

	cases := []struct {
		name string
		path string
		// want is each handler's own internal-error wrap of the propagated
		// secondary failure (prefixes frozen from rest/milpacs.go).
		want string
	}{
		{
			name: "by_id",
			path: "/api/v1/milpacs/profile/id/1",
			want: `fetch profile by user id: collect secondary positions: lookup secondary position \"20\": record not found`,
		},
		{
			name: "by_username",
			path: "/api/v1/milpacs/profile/username/Jarvis.A",
			want: `fetch profile by username: collect secondary positions: lookup secondary position \"20\": record not found`,
		},
		{
			name: "by_discord",
			path: "/api/v1/milpac/discord/112233445566778899",
			want: `fetch profile by discord id: collect secondary positions: lookup secondary position \"20\": record not found`,
		},
		{
			name: "by_gamertag",
			path: "/api/v1/milpac/gamertag/CavGamer77",
			want: `fetch profile by gamertag: collect secondary positions: lookup secondary position \"20\": record not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := secondaryOutageGet(t, h, tc.path)
			require.Equal(t, http.StatusInternalServerError, rr.Code,
				"a secondary-lookup outage on a resolving member is the contract 500, never a 404")
			assert.JSONEq(t, `{"code":13,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// Regression guard for the masking decision: a genuine WHOLE-METHOD
// gorm.ErrRecordNotFound (the member itself does not exist) must STILL map to
// 404. The #154 fix masks only the secondary-lookup sentinel; it must not
// disturb the real not-found path.
func TestNewStack_GenuineProfileNotFoundStill404(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findProfilesById: func(...uint64) ([]*types.Profile, error) { return nil, gorm.ErrRecordNotFound },
	}, &stubReferenceCache{})

	rr := secondaryOutageGet(t, h, "/api/v1/milpacs/profile/id/1")

	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.JSONEq(t, `{"code":5,"message":"no profile found for user ID: 1","details":[]}`, rr.Body.String())
}

// #155 — a forum-post-date aggregation failure on the by-id FULL-profile route
// must surface as the contract 500. getLatestForumPostDates wraps the query
// error with %w; a LEFT JOIN aggregation never yields gorm.ErrRecordNotFound,
// so this stays off the 404 branch and on the outage path. The fake reproduces
// the post-wrap error shape (processProfiles propagates it verbatim).
func TestNewStack_ForumPostDateFailureIsInternal(t *testing.T) {
	wrapped := fmt.Errorf("fetch forum post dates: %w", errOutage)
	require.False(t, errors.Is(wrapped, gorm.ErrRecordNotFound))

	h := rest.New(&fakeDatastore{
		findProfilesById: func(...uint64) ([]*types.Profile, error) { return nil, wrapped },
	}, &stubReferenceCache{})

	rr := secondaryOutageGet(t, h, "/api/v1/milpacs/profile/id/1")

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t,
		`{"code":13,"message":"fetch profile by user id: fetch forum post dates: simulated datastore outage","details":[]}`,
		rr.Body.String())
}

// #154/#155 — the roster routes (which map ANY datastore error to the contract
// 500 unconditionally) answer the outage path for both propagated classes, on
// all three roster shapes. This complements the by-id classification pins
// above: rosters have no 404 branch to mislabel, so the only correct answer is
// the 500.
func TestNewStack_RosterSecondaryAndPostDateOutagesAreInternal(t *testing.T) {
	secondary := fmt.Errorf("error generating profiles: collect secondary positions: lookup secondary position %q: %v", "20", gorm.ErrRecordNotFound)
	postDate := fmt.Errorf("fetch forum post dates: %w", errOutage)

	cases := []struct {
		name string
		path string
		fake *fakeDatastore
		want string
	}{
		{
			name: "full/secondary",
			path: "/api/v1/roster/ROSTER_TYPE_COMBAT",
			fake: &fakeDatastore{findRosterByType: func(types.RosterType) (*types.Roster, error) { return nil, secondary }},
			want: `fetch roster ROSTER_TYPE_COMBAT: error generating profiles: collect secondary positions: lookup secondary position \"20\": record not found`,
		},
		{
			name: "lite/postdate",
			path: "/api/v1/roster/ROSTER_TYPE_COMBAT/lite",
			fake: &fakeDatastore{findLiteRosterByType: func(types.RosterType) (*types.LiteRoster, error) { return nil, postDate }},
			want: "fetch lite roster ROSTER_TYPE_COMBAT: fetch forum post dates: simulated datastore outage",
		},
		{
			name: "s1/secondary",
			path: "/api/v1/s1/uniforms/ROSTER_TYPE_COMBAT",
			fake: &fakeDatastore{findS1UniformsRosterByType: func(types.RosterType) (*types.S1UniformsRoster, error) { return nil, secondary }},
			want: `fetch s1 uniforms roster ROSTER_TYPE_COMBAT: error generating profiles: collect secondary positions: lookup secondary position \"20\": record not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := rest.New(tc.fake, &stubReferenceCache{})
			rr := secondaryOutageGet(t, h, tc.path)
			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// #154 — the position-search LITE route (FindProfilesByPosition →
// processLiteProfiles → generateLiteProtoProfile → collectSecondaryPositions)
// is the one lite consumer of the changed helper with no HTTP outage test. Its
// handler (searchByPosition, rest/positions.go) maps ANY datastore error to the
// contract 500 unconditionally — no 404 branch — so a propagated secondary
// failure must surface as the 500 with the frozen "error searching profiles by
// position" wrap, never a fabricated empty {"profiles":{}} 200.
func TestNewStack_PositionSearchSecondaryOutageIsInternal(t *testing.T) {
	secondary := fmt.Errorf("error generating profiles: collect secondary positions: lookup secondary position %q: %v", "20", gorm.ErrRecordNotFound)

	h := rest.New(&fakeDatastore{
		findProfilesByPosition: func(string) (*types.LiteRoster, error) { return nil, secondary },
	}, &stubReferenceCache{})

	rr := secondaryOutageGet(t, h, "/api/v1/milpacs/position/search/Military%20Police")

	require.Equal(t, http.StatusInternalServerError, rr.Code,
		"a propagated secondary-lookup outage on the position-search route is the contract 500, never a degraded empty 200")
	assert.JSONEq(t,
		`{"code":13,"message":"error searching profiles by position: error generating profiles: collect secondary positions: lookup secondary position \"20\": record not found","details":[]}`,
		rr.Body.String())
}
