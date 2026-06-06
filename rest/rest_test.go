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

	// Tickets overrides (seeded defaults live in fake_tickets_test.go); a
	// test sets one to inject an outage or observe the bound filter.
	listTickets            func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error)
	getTicket              func(ticketID uint32) (*proto.Ticket, error)
	getTicketFirstMessages func(ticketID uint32, n int) ([]*proto.Message, uint32, error)
	listTicketMessages     func(ticketID uint32, afterCursor string, perPage uint32) ([]*proto.Message, string, bool, error)
	listCategories         func() ([]*proto.Category, error)
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
	return rest.New(&fakeDatastore{}, nil)
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
	"tickets/categories",
	"tickets/get_by_id_happy",
	"tickets/get_by_id_not_found",
	"tickets/get_by_id_parse_error",
	"tickets/get_by_ref_happy",
	"tickets/get_by_ref_not_found",
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
	}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":13,"message":"error fetching ranks: unexpected EOF","details":[]}`, rr.Body.String())
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
	srv := httptest.NewServer(rest.New(&fakeDatastore{}, nil))
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
	}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/milpacs/ranks", nil)
	req.Header.Set("Authorization", "Bearer cav7_readkey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"ranks":[]}`, strings.TrimSpace(rr.Body.String()))
}
