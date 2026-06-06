package rest_test

// New-stack-only tickets tests: behavior the golden corpus cannot witness
// (datastore outages, allocation discipline on empty pages) plus the
// enumerated cutover breaks from PRD #112 — deliberately plain tests, not
// battery cases, because the corpus replays against the old stack too.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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

// /tickets/ref/messages is a by-ref lookup of the ref "messages" — the ref
// pattern is more specific than the {ticket_id}/{sub} dispatcher and keeps
// winning all of /tickets/ref/*, exactly like the old gateway's route order.
func TestNewStack_TicketsRefMessagesIsByRefLookup(t *testing.T) {
	h := newStack(t)

	rr := ticketsGet(t, h, "/api/v1/tickets/ref/messages")
	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.JSONEq(t, `{"code":5,"message":"ticket \"messages\" not found","details":[]}`, rr.Body.String())
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
