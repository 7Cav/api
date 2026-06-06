package rest_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// positionsGet replays one authenticated GET against the stack — the local
// shorthand for this file's request loop.
func positionsGet(h http.Handler, path, key string) *httptest.ResponseRecorder {
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
		findAllPositionGroups:  func() ([]*proto.PositionGroup, error) { return nil, io.ErrUnexpectedEOF },
		findProfilesByPosition: func(string) (*proto.LiteRoster, error) { return nil, io.ErrUnexpectedEOF },
		findAwol:               func() ([]*proto.Awol, error) { return nil, io.ErrUnexpectedEOF },
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
			rr := positionsGet(h, tc.path, "cav7_readkey")

			require.Equal(t, http.StatusInternalServerError, rr.Code)
			assert.JSONEq(t, `{"code":13,"message":"`+tc.want+`","details":[]}`, rr.Body.String())
		})
	}
}

// An empty position-group catalog must serialize as {"groups":[]} — the
// allocation discipline (empty collections are [], never null) the goldens
// can only witness on populated routes.
func TestNewStack_EmptyPositionGroupsIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{findAllPositionGroups: func() ([]*proto.PositionGroup, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	rr := positionsGet(h, "/api/v1/milpacs/position/groups", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"groups":[]}`, strings.TrimSpace(rr.Body.String()))
}

// A lite roster whose profiles map the datastore left NIL (nil error) still
// emits {"profiles":{}} — the mapper allocates, so the frozen empty-result
// form (#137) cannot regress to null through a lazy datastore.
func TestNewStack_SearchNilProfilesMapIsEmptyObject(t *testing.T) {
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(string) (*proto.LiteRoster, error) {
		return &proto.LiteRoster{}, nil // Profiles map nil, not allocated
	}}, &stubReferenceCache{})

	rr := positionsGet(h, "/api/v1/milpacs/position/search/Rifleman", "cav7_readkey")

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, `{"profiles":{}}`, strings.TrimSpace(rr.Body.String()))
}

// A NIL roster with a nil error violates the datastore invariant
// (datastores.Mysql always allocates): it must surface as the frozen
// Internal shape, not a panic or a fabricated empty 200.
func TestNewStack_SearchNilRosterWithNilErrorIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(string) (*proto.LiteRoster, error) {
		return nil, nil
	}}, &stubReferenceCache{})

	rr := positionsGet(h, "/api/v1/milpacs/position/search/Rifleman", "cav7_readkey")

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"datastore returned no roster","details":[]}`, rr.Body.String())
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
	h := rest.New(&fakeDatastore{findProfilesByPosition: func(q string) (*proto.LiteRoster, error) {
		got = q
		return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{}}, nil
	}}, &stubReferenceCache{})

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/milpacs/position/search/Regimental%20Technical%20Aide", "Regimental Technical Aide"},
		{"/api/v1/milpacs/position/search/Platoon/Leader", "Platoon/Leader"},
		{"/api/v1/milpacs/position/search/Platoon%20Sergeant/Bravo%26Charlie", "Platoon Sergeant/Bravo&Charlie"},
		// %25 is a literal percent: single decoding yields "100%", and the
		// datastore's own LIKE-escaping handles it from there.
		{"/api/v1/milpacs/position/search/100%25", "100%"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got = ""
			rr := positionsGet(h, tc.path, "cav7_readkey")

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

	rr := positionsGet(h, "/api/v1/milpacs/position/search", "cav7_readkey")

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

// The scope gate is witnessed PER route: a ticket-scoped key (read:tickets,
// not read) must 403 with the frozen body on each #128 route — the golden
// tier only witnesses one milpacs path, so this loop proves no route was
// registered without its requireScope gate. The search trailing-slash form
// rides along: 403 answers BEFORE the empty-query 400 (the uniform tier
// order, ruled at #126 — 403-before-binding-400).
func TestNewStack_PositionAndAwolRoutes403UnderTicketScopedKey(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/milpacs/position/groups",
		"/api/v1/milpacs/position/search/Rifleman",
		"/api/v1/milpacs/position/search/",
		"/api/v1/milpacs/awol",
	} {
		t.Run(path, func(t *testing.T) {
			rr := positionsGet(h, path, "cav7_ticketskey")

			require.Equal(t, http.StatusForbidden, rr.Code)
			assert.JSONEq(t, `{"code":7,"message":"scope required: read","details":[]}`, rr.Body.String())
		})
	}
}
