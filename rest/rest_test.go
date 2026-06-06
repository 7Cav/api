package rest_test

import (
	"compress/gzip"
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
// implemented routes serve: the battery's bearer tokens and the two-rank
// catalog. Unimplemented methods panic via the embedded nil interface — a
// loud failure if a test reaches further than the routes it mounts.
type fakeDatastore struct {
	datastores.Datastore
	findAllRanks func() ([]*proto.RankExpanded, error)
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
	"auth/milpacs_missing_header",
	"auth/milpacs_raw_key",
	"auth/milpacs_invalid_key",
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
