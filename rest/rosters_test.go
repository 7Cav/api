package rest_test

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/contract"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rosterGet drives one authenticated GET against the mounted stack.
func rosterGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// rosterRoutePaths is the three-shape surface under one happy path value —
// the per-route loops below witness shared behavior on every shape, so no
// route can be registered without it.
var rosterRoutePaths = []string{
	"/api/v1/roster/ROSTER_TYPE_COMBAT",
	"/api/v1/roster/ROSTER_TYPE_COMBAT/lite",
	"/api/v1/s1/uniforms/ROSTER_TYPE_COMBAT",
}

// The scope gate is witnessed PER roster route: a ticket-scoped key
// (read:tickets, not read) must 403 with the frozen body on each of the
// three shapes (same loop as the profile routes — proves no route was
// registered without its requireScope gate).
func TestNewStack_AllRosterRoutes403UnderTicketScopedKey(t *testing.T) {
	h := newStack(t)

	for _, path := range rosterRoutePaths {
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

// Uniform tier order on the roster surface (the ruled cutover break, same
// ruling as the profile routes): a wrong-scope key with a BOGUS enum literal
// gets the 403, not the binding 400 — requireScope wraps the handler, so the
// scope gate answers before bindRosterPath runs.
func TestNewStack_RosterWrongScopeBeatsBogusEnum_RuledBreak(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/roster/IMAGINARY_ROSTER",
		"/api/v1/roster/IMAGINARY_ROSTER/lite",
		"/api/v1/s1/uniforms/IMAGINARY_ROSTER",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer cav7_ticketskey")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusForbidden, rr.Code,
				"403 answers before the enum-binding 400 — ruled, NOT old-stack parity")
			assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
		})
	}
}

// The bogus-enum 400 wraps the leaked parse text on ALL three bindings (the
// corpus pins it on the full roster only; lite and s1 share bindRosterPath,
// pinned here).
func TestNewStack_BogusEnumIs400OnEveryRosterBinding(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/roster/IMAGINARY_ROSTER/lite",
		"/api/v1/s1/uniforms/IMAGINARY_ROSTER",
	} {
		t.Run(path, func(t *testing.T) {
			rr := rosterGet(t, h, path)
			require.Equal(t, http.StatusBadRequest, rr.Code)
			assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: roster, error: IMAGINARY_ROSTER is not valid","details":[]}`, rr.Body.String())
		})
	}
}

// A number that parses but names no cataloged roster is the same gateway
// rejection — the old gateway checked the enum's VALUE set, not just syntax.
func TestNewStack_OutOfCatalogNumberIs400(t *testing.T) {
	h := newStack(t)

	rr := rosterGet(t, h, "/api/v1/roster/7")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: roster, error: 7 is not valid","details":[]}`, rr.Body.String())
}

// The number form inherits the gateway's base-0 parse on the live route:
// /roster/0x1 is the combat roster — byte-identical to /roster/1 (the same
// frozen leniency tier as the profile path id, witnessed end-to-end).
func TestNewStack_RosterNumberFormParsesBaseZero(t *testing.T) {
	h := newStack(t)

	want := rosterGet(t, h, "/api/v1/roster/1")
	require.Equal(t, http.StatusOK, want.Code)

	got := rosterGet(t, h, "/api/v1/roster/0x1")
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, want.Body.String(), got.Body.String())
}

// Every cataloged non-zero number serves: the numeric form is the upstream
// roster_id FK, golden-pinned for 1 — the rest of the catalog answers 200
// with the seeded empty-roster shape.
func TestNewStack_EveryCatalogedRosterNumberServes(t *testing.T) {
	h := newStack(t)

	for n := 2; n <= 6; n++ {
		path := fmt.Sprintf("/api/v1/roster/%d", n)
		if n == 5 {
			continue // ROSTER_TYPE_ARLINGTON is the seeded outage (golden-pinned 500)
		}
		t.Run(path, func(t *testing.T) {
			rr := rosterGet(t, h, path)
			require.Equal(t, http.StatusOK, rr.Code)
			assert.JSONEq(t, `{"profiles":{}}`, rr.Body.String())
		})
	}
}

// RosterRequest's only field is path-bound, so the generated gateway
// handlers never called ParseForm (verified against proto/milpacs.pb.gw.go):
// malformed query SYNTAX falls through on all three routes — same leniency
// as the discord/gamertag routes, the opposite of the profile id/username
// routes. The unknown_query_param_ignored golden pins the unknown-param half.
func TestNewStack_RosterRoutes_MalformedQuerySyntaxIgnored(t *testing.T) {
	h := newStack(t)

	for _, base := range rosterRoutePaths {
		path := base + "?junk=%zz"
		t.Run(path, func(t *testing.T) {
			rr := rosterGet(t, h, path)
			require.Equal(t, http.StatusOK, rr.Code, "old gateway never ParseForm'd the roster routes")
		})
	}
}

// A datastore failure on the lite and s1 bindings surfaces as the frozen
// Internal shape with each handler's own wrapped message — the full-roster
// variant is golden-pinned (roster/internal_error); the corpus has no outage
// cases for these two, so these pin them. The enum NAME interpolates via %s.
func TestNewStack_LiteAndS1OutagesAreInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findLiteRosterByType:       func(proto.RosterType) (*proto.LiteRoster, error) { return nil, io.ErrUnexpectedEOF },
		findS1UniformsRosterByType: func(proto.RosterType) (*proto.S1UniformsRoster, error) { return nil, io.ErrUnexpectedEOF },
	}, &stubReferenceCache{})

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/roster/ROSTER_TYPE_COMBAT/lite", "fetch lite roster ROSTER_TYPE_COMBAT: unexpected EOF"},
		{"/api/v1/s1/uniforms/ROSTER_TYPE_COMBAT", "fetch s1 uniforms roster ROSTER_TYPE_COMBAT: unexpected EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			rr := rosterGet(t, h, tc.path)
			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// A nil roster with a nil error violates the datastore invariant (possible
// only from a future datastore bug): a clean 500 on every shape, not a panic
// or a fabricated empty 200.
func TestNewStack_NilRosterWithNilErrorIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findRosterByType:           func(proto.RosterType) (*proto.Roster, error) { return nil, nil },
		findLiteRosterByType:       func(proto.RosterType) (*proto.LiteRoster, error) { return nil, nil },
		findS1UniformsRosterByType: func(proto.RosterType) (*proto.S1UniformsRoster, error) { return nil, nil },
	}, &stubReferenceCache{})

	for _, path := range rosterRoutePaths {
		t.Run(path, func(t *testing.T) {
			rr := rosterGet(t, h, path)
			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"datastore returned no roster","details":[]}`, rr.Body.String())
		})
	}
}

// A roster message whose profiles map is nil (distinct from empty) still
// serves {"profiles":{}} — the old stack's EmitUnpopulated marshaler emitted
// {} for a nil proto map, and the mapper's unconditional allocation preserves
// that.
func TestNewStack_NilProfilesMapServesEmptyObject(t *testing.T) {
	h := rest.New(&fakeDatastore{
		findRosterByType: func(proto.RosterType) (*proto.Roster, error) { return &proto.Roster{}, nil },
	}, &stubReferenceCache{})

	rr := rosterGet(t, h, "/api/v1/roster/ROSTER_TYPE_COMBAT")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"profiles":{}}`, rr.Body.String())
}

// Wrong-method on a roster route keeps the ruled 405 + Allow (the fallback's
// GET probe is pattern-based, so the parameterized roster patterns are
// covered — witnessed once per fan-out surface).
func TestNewStack_WrongMethodOnRosterRouteIs405WithAllow(t *testing.T) {
	h := newStack(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/roster/ROSTER_TYPE_COMBAT", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, "GET, HEAD", rr.Header().Get("Allow"))
	assert.JSONEq(t, `{"code":12,"message":"Method Not Allowed","details":[]}`, rr.Body.String())
}

// The large-roster criterion (#127): the combat roster is 15.7MB of JSON in
// production — the shape must hold at full size, not just on the two-member
// golden. The fake serves 4000 members cloned from the SAME seeds the golden
// freezes; the response (through the real gzip layer) must compare clean
// member-by-member against the golden's own profile objects — semantic
// equality at full size, with the gzip layer earning its keep on the way.
func TestNewStack_LargeRosterComparesCleanAtFullSize(t *testing.T) {
	const members = 4000

	h := rest.New(&fakeDatastore{
		findRosterByType: func(proto.RosterType) (*proto.Roster, error) {
			profiles := make(map[uint64]*proto.Profile, members)
			for i := uint64(1); i <= members; i++ {
				if i%2 == 1 {
					profiles[i] = seedJarvis()
				} else {
					profiles[i] = seedDoe()
				}
			}
			return &proto.Roster{Profiles: profiles}, nil
		},
	}, &stubReferenceCache{})

	// The expectation comes from the committed golden, not from re-running
	// the mappers: each cloned member must equal the golden's recording of
	// the same seed.
	g, err := contract.LoadGolden(goldensDir, "roster/combat_by_name")
	require.NoError(t, err)
	var golden struct {
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(g.Body, &golden))
	var wantJarvis, wantDoe any
	require.NoError(t, json.Unmarshal(golden.Profiles["1"], &wantJarvis))
	require.NoError(t, json.Unmarshal(golden.Profiles["2"], &wantDoe))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/roster/ROSTER_TYPE_COMBAT", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	req.Header.Set("Accept-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "gzip", rr.Result().Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(rr.Body)
	require.NoError(t, err)
	raw, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NoError(t, zr.Close(), "gzip trailer must be intact at full size")

	var got struct {
		Profiles map[string]json.RawMessage `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got.Profiles, members)

	for i := uint64(1); i <= members; i++ {
		key := fmt.Sprintf("%d", i)
		var member any
		require.NoError(t, json.Unmarshal(got.Profiles[key], &member))
		want := wantJarvis
		if i%2 == 0 {
			want = wantDoe
		}
		if !assert.ObjectsAreEqual(want, member) {
			require.Equal(t, want, member, "member %s diverges from the golden seed shape", key)
		}
	}
}
