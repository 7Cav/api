package rest_test

// New-stack-only tickets tests: behavior the golden corpus cannot witness
// (datastore outages, allocation discipline on empty pages) plus the
// enumerated cutover breaks from PRD #112 — deliberately plain tests, not
// battery cases, because the corpus replays against the old stack too.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ticketsGet drives one authenticated GET against the new stack with the
// read:tickets key.
func ticketsGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer cav7_ticketskey")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// A datastore outage on the ticket fetch must surface as the frozen Internal
// shape (message text mirrors the old handler verbatim: "fetch ticket: %v").
func TestNewStack_GetTicketDatastoreOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{getTicket: func(uint32) (*proto.Ticket, error) {
		return nil, io.ErrUnexpectedEOF
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets/42")
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"fetch ticket: unexpected EOF","details":[]}`, rr.Body.String())
}

// An outage on the first-messages fetch (after the ticket resolved) keeps its
// own frozen message string: "fetch ticket messages: %v".
func TestNewStack_GetTicketFirstMessagesOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{getTicketFirstMessages: func(uint32, int) ([]*proto.Message, uint32, error) {
		return nil, 0, io.ErrUnexpectedEOF
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets/42")
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"fetch ticket messages: unexpected EOF","details":[]}`, rr.Body.String())
}

// An outage on the categories list: "list ticket categories: %v".
func TestNewStack_ListCategoriesOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{listCategories: func() ([]*proto.Category, error) {
		return nil, io.ErrUnexpectedEOF
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets/categories")
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"list ticket categories: unexpected EOF","details":[]}`, rr.Body.String())
}

// An outage on the tickets list: "list tickets: %v".
func TestNewStack_ListTicketsOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{listTickets: func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
		return nil, "", false, io.ErrUnexpectedEOF
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets")
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"list tickets: unexpected EOF","details":[]}`, rr.Body.String())
}

// An empty tickets page must serialize with the collections allocated:
// {"tickets":[],...} — never null.
func TestNewStack_EmptyTicketsPageIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{listTickets: func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
		return nil, "", false, nil
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"tickets":[],"nextCursor":"","hasMore":false}`, rr.Body.String())
}

// An outage on the messages list: "list ticket messages: %v".
func TestNewStack_ListTicketMessagesOutageIsInternalJSON(t *testing.T) {
	h := rest.New(&fakeDatastore{listTicketMessages: func(uint32, string, uint32) ([]*proto.Message, string, bool, error) {
		return nil, "", false, io.ErrUnexpectedEOF
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets/42/messages")
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.JSONEq(t, `{"code":13,"message":"list ticket messages: unexpected EOF","details":[]}`, rr.Body.String())
}

// --- /tickets subtree routing edges (unpinned by goldens, frozen here) -----

// /tickets/ref/messages reproduces the old gateway's route order verbatim.
// The gateway's ServeMux.Handle PREPENDED patterns, so match order was the
// REVERSE of registration: ListCategories → ListTicketMessages →
// GetTicketByRef → GetTicket → ListTickets. /tickets/ref/messages therefore
// hit {ticket_id}/messages FIRST, bound ticket_id="ref", and leaked the
// frozen strconv 400 — it was never a by-ref lookup of "messages". The 400
// fired inside the gateway before the RPC body ran, and RequireScope lived
// inside the RPC body it never reached — so any AUTHENTICATED key sees the
// 400, scope notwithstanding.
func TestNewStack_TicketsRefMessagesIsFrozenParse400(t *testing.T) {
	h := newStack(t)

	for _, key := range []string{"cav7_ticketskey", "cav7_readkey", "cav7_noscopekey"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tickets/ref/messages", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code, key)
		assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: ticket_id, error: strconv.ParseUint: parsing \"ref\": invalid syntax","details":[]}`, rr.Body.String(), key)
	}
}

// /tickets/ref with no tail: the old gateway's match order reached GetTicket
// ({ticket_id}) with ticket_id="ref" — the same frozen parse 400. The new
// stack's {ticket_id} route produces it naturally (verified parity); pinned
// so it cannot rot.
func TestNewStack_TicketsRefNoTailIsFrozenParse400(t *testing.T) {
	h := newStack(t)

	rr := ticketsGet(t, h, "/api/v1/tickets/ref")
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"code":3,"message":"type mismatch, parameter: ticket_id, error: strconv.ParseUint: parsing \"ref\": invalid syntax","details":[]}`, rr.Body.String())
}

// An unknown sub-resource under a ticket id is the JSON 404 — and it stays a
// 404 (not a 403) for callers without read:tickets, like every other unknown
// path: route-shape dispatch precedes the scope gate.
func TestNewStack_UnknownTicketSubResourceIsJSON404(t *testing.T) {
	h := newStack(t)

	for _, key := range []string{"cav7_ticketskey", "cav7_readkey"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tickets/42/bogus", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code, key)
		assert.JSONEq(t, `{"code":5,"message":"Not Found","details":[]}`, rr.Body.String(), key)
	}
}

// --- Query-parse 400 parity (old-gateway behavior, frozen wire text) -------
//
// The old gateway did NOT silently drop unparseable uint32/bool query values
// — runtime.PopulateQueryParameters 400'd them (verified against
// grpc-gateway v2.29.0): "parsing field" for scalars, "parsing list" for
// repeated fields, the snake_case proto field name in both. These are parity
// pins, not enumerated breaks. The breaks-list entry about invalid ENUM query
// values being silently dropped is real but does not apply to this surface:
// the tickets routes declare no enum-typed query parameter.
func TestNewStack_InvalidQueryValuesReturn400(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/tickets?status_id=abc",
			`parsing list "status_id": strconv.ParseUint: parsing "abc": invalid syntax`},
		{"/api/v1/tickets?excludeSubcategories=bogus",
			`parsing field "exclude_subcategories": strconv.ParseBool: parsing "bogus": invalid syntax`},
		{"/api/v1/tickets?perPage=-1",
			`parsing field "per_page": strconv.ParseUint: parsing "-1": invalid syntax`},
		{"/api/v1/tickets/42/messages?per_page=abc",
			`parsing field "per_page": strconv.ParseUint: parsing "abc": invalid syntax`},
		{"/api/v1/tickets/42/messages?include_hidden=banana",
			`parsing field "include_hidden": strconv.ParseBool: parsing "banana": invalid syntax`},
	}
	for _, tc := range cases {
		rr := ticketsGet(t, h, tc.path)
		require.Equal(t, http.StatusBadRequest, rr.Code, tc.path)
		body, err := json.Marshal(map[string]any{"code": 3, "message": tc.want, "details": []any{}})
		require.NoError(t, err)
		assert.JSONEq(t, string(body), rr.Body.String(), tc.path)
	}
}

// Same key repeated on a scalar query field: the old gateway's deterministic
// too-many-values 400 (parity, frozen text) — and a bad value alongside the
// other spelling is the parsing-field 400 even though the camel value alone
// would bind (every value parses; old behavior, deterministic either map
// order).
func TestNewStack_ScalarQueryRepetitionAndDualSpellingErrors(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/tickets?per_page=1&per_page=2",
			`too many values for field "per_page": 1, 2`},
		{"/api/v1/tickets/42/messages?after_cursor=a&after_cursor=b",
			`too many values for field "after_cursor": a, b`},
		{"/api/v1/tickets?per_page=abc&perPage=5",
			`parsing field "per_page": strconv.ParseUint: parsing "abc": invalid syntax`},
	}
	for _, tc := range cases {
		rr := ticketsGet(t, h, tc.path)
		require.Equal(t, http.StatusBadRequest, rr.Code, tc.path)
		body, err := json.Marshal(map[string]any{"code": 3, "message": tc.want, "details": []any{}})
		require.NoError(t, err)
		assert.JSONEq(t, string(body), rr.Body.String(), tc.path)
	}
}

// Malformed query SYNTAX on the two list routes: the old generated handlers
// called req.ParseForm() (verified in proto/tickets.pb.gw.go) and 400'd with
// the parse error verbatim — never the silent drop r.URL.Query() performs.
// Get/GetByRef/Categories never called ParseForm, so only these two routes
// carry the strict tier.
func TestNewStack_MalformedQuerySyntaxOnListRoutesIs400(t *testing.T) {
	h := newStack(t)

	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/tickets?per_page=%zz", `invalid URL escape "%zz"`},
		{"/api/v1/tickets?a=1;b=2", `invalid semicolon separator in query`},
		{"/api/v1/tickets/42/messages?per_page=%zz", `invalid URL escape "%zz"`},
		{"/api/v1/tickets/42/messages?a=1;b=2", `invalid semicolon separator in query`},
	}
	for _, tc := range cases {
		rr := ticketsGet(t, h, tc.path)
		require.Equal(t, http.StatusBadRequest, rr.Code, tc.path)
		body, err := json.Marshal(map[string]any{"code": 3, "message": tc.want, "details": []any{}})
		require.NoError(t, err)
		assert.JSONEq(t, string(body), rr.Body.String(), tc.path)
	}
}

// The Grpc-Metadata-* response headers the old gateway leaks on tickets
// routes are NOT reproduced (breaks list: "Gateway artifacts dropped").
func TestNewStack_NoGrpcMetadataHeadersOnTicketsRoutes(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/tickets",
		"/api/v1/tickets/42",
		"/api/v1/tickets/ref/MF1UI9HE",
		"/api/v1/tickets/42/messages",
		"/api/v1/tickets/categories",
	} {
		rr := ticketsGet(t, h, path)
		require.Equal(t, http.StatusOK, rr.Code, path)
		for name := range rr.Result().Header {
			assert.NotContains(t, strings.ToLower(name), "grpc-metadata", path)
		}
	}
}

// --- Scope separation (read vs read:tickets) -------------------------------

// The battery pins both wrong-scope directions with goldens
// (auth/tickets_wrong_scope: `read` on /api/v1/tickets;
// auth/milpacs_wrong_scope: `read:tickets` on a milpacs route, replayed in
// TestNewStack_AuthTiersOnRanksRoute). This covers the tier the battery
// lacks on the tickets surface — a valid key with NO scopes — and pins the
// per-handler scope name in the 403 body on every tickets route.
func TestNewStack_TicketsScopeGateOnEveryRoute(t *testing.T) {
	h := newStack(t)

	for _, path := range []string{
		"/api/v1/tickets",
		"/api/v1/tickets/42",
		"/api/v1/tickets/ref/MF1UI9HE",
		"/api/v1/tickets/42/messages",
		"/api/v1/tickets/categories",
	} {
		for _, key := range []string{"cav7_readkey", "cav7_noscopekey"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer "+key)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			require.Equal(t, http.StatusForbidden, rr.Code, "%s with %s", path, key)
			assert.JSONEq(t, `{"code":7,"message":"scope required: read:tickets","details":[]}`,
				rr.Body.String(), "%s with %s", path, key)
		}
	}
}

// An empty category tree must serialize as {"categories":[]} — allocation
// discipline the goldens only witness populated.
func TestNewStack_EmptyCategoriesIsEmptyArray(t *testing.T) {
	h := rest.New(&fakeDatastore{listCategories: func() ([]*proto.Category, error) {
		return nil, nil
	}}, nil)

	rr := ticketsGet(t, h, "/api/v1/tickets/categories")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"categories":[]}`, rr.Body.String())
}
