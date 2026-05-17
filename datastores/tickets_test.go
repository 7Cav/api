package datastores

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"testing"

	"github.com/7cav/api/referencecache"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRefCache struct {
	statuses   map[uint32]string
	priorities map[uint32]string
	prefixes   map[uint32]string
	cats       map[uint32]*referencecache.CategoryRecord
	subtree    func([]uint32) []uint32
}

func (f *fakeRefCache) StatusName(id uint32) string                       { return f.statuses[id] }
func (f *fakeRefCache) PriorityName(id uint32) string                     { return f.priorities[id] }
func (f *fakeRefCache) PrefixName(id uint32) string                       { return f.prefixes[id] }
func (f *fakeRefCache) Category(id uint32) *referencecache.CategoryRecord { return f.cats[id] }
func (f *fakeRefCache) CategoryAncestors(id uint32) []uint32 {
	cat := f.cats[id]
	if cat == nil || cat.ParentID == 0 {
		return nil
	}
	return []uint32{cat.ParentID}
}
func (f *fakeRefCache) CategoryTree() []*referencecache.CategoryRecord {
	out := make([]*referencecache.CategoryRecord, 0, len(f.cats))
	for _, c := range f.cats {
		out = append(out, c)
	}
	return out
}
func (f *fakeRefCache) ExpandSubtree(ids []uint32) []uint32 {
	if f.subtree != nil {
		return f.subtree(ids)
	}
	return ids
}

func newFakeRefCache() *fakeRefCache {
	return &fakeRefCache{
		statuses:   map[uint32]string{1: "Open", 11: "Closed"},
		priorities: map[uint32]string{1: "Low"},
		prefixes:   map[uint32]string{},
		cats: map[uint32]*referencecache.CategoryRecord{
			5:  {ID: 5, Title: "S1 Personnel Admin", Lft: 1, Rgt: 12},
			17: {ID: 17, ParentID: 5, Title: "S1 Citations", Lft: 2, Rgt: 3},
		},
	}
}

func TestListTickets_NoFiltersHappyPath(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()

	rc := newFakeRefCache()

	rows := sqlmock.NewRows([]string{
		"ticket_id", "ticket_ref", "title",
		"user_id", "username", "start_date",
		"priority", "status_id", "ticket_state",
		"ticket_locked", "discussion_state",
		"assigned_user_id", "assigned_username",
		"ticket_category_id", "last_message_id",
		"last_message_date", "last_message_user_id",
		"last_message_username", "last_modified_date",
		"reply_count", "prefix_id",
		"starter_user_id", "starter_username",
	}).AddRow(
		7499, "MF1UI9HE", "HALO Combat Jump",
		1648, "Hilberg.A", uint32(1736294298),
		uint32(1), uint32(11), "open",
		uint32(0), "visible",
		uint32(0), "",
		uint32(17), uint32(55112),
		uint32(1736557390), uint32(7804),
		"Angels.N", uint32(1736556824),
		uint32(4), uint32(0),
		uint32(1648), "Hilberg.A",
	)

	mock.ExpectQuery(regexp.QuoteMeta("FROM `xf_nf_tickets_ticket`")).
		WillReturnRows(rows)

	// field_values + participants preload queries (GORM orders alphabetically by association name)
	mock.ExpectQuery(regexp.QuoteMeta("xf_nf_tickets_ticket_field_value")).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id", "field_id", "field_value"}))
	mock.ExpectQuery(regexp.QuoteMeta("xf_nf_tickets_ticket_participant")).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id", "user_id", "last_read_date"}))

	tickets, next, more, err := (&ds).ListTickets(context.Background(), rc, &ListTicketsFilter{PerPage: 50})
	require.NoError(t, err)
	assert.Empty(t, next, "no cursor returned for a single-page result set")
	assert.False(t, more)
	require.Len(t, tickets, 1)
	got := tickets[0]
	assert.Equal(t, uint32(7499), got.TicketId)
	assert.Equal(t, "MF1UI9HE", got.TicketRef)
	assert.Equal(t, "Closed", got.StatusName, "status name resolved from reference cache")
	assert.Equal(t, "S1 Citations", got.CategoryTitle)
	assert.Equal(t, []uint32{5}, got.CategoryAncestorIds)
}

func TestListTickets_FilterByCategoryExpandsSubtree(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()

	rc := newFakeRefCache()
	rc.subtree = func(in []uint32) []uint32 {
		assert.Equal(t, []uint32{5}, in)
		return []uint32{5, 17, 16, 14, 15, 24}
	}

	rows := sqlmock.NewRows([]string{"ticket_id"})
	mock.ExpectQuery(regexp.QuoteMeta("ticket_category_id IN")).
		WithArgs("visible", uint32(5), uint32(17), uint32(16), uint32(14), uint32(15), uint32(24), 51).
		WillReturnRows(rows)

	_, _, _, err := (&ds).ListTickets(context.Background(), rc, &ListTicketsFilter{
		CategoryIDs: []uint32{5},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListTickets_ExcludeSubcategoriesSkipsExpansion(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()
	rc := newFakeRefCache()
	rc.subtree = func(in []uint32) []uint32 {
		t.Fatalf("subtree should not have been called")
		return nil
	}
	mock.ExpectQuery(regexp.QuoteMeta("ticket_category_id IN")).
		WithArgs("visible", uint32(5), 51).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id"}))
	_, _, _, err := (&ds).ListTickets(context.Background(), rc, &ListTicketsFilter{
		CategoryIDs:          []uint32{5},
		ExcludeSubcategories: true,
	})
	require.NoError(t, err)
}

func TestListTickets_CursorPagination(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()
	rc := newFakeRefCache()

	// Two rows, perPage=1, so hasMore=true and a cursor comes back.
	header := []string{
		"ticket_id", "ticket_ref", "title", "user_id", "username", "start_date",
		"priority", "status_id", "ticket_state", "ticket_locked", "discussion_state",
		"assigned_user_id", "assigned_username", "ticket_category_id",
		"last_message_id", "last_message_date", "last_message_user_id",
		"last_message_username", "last_modified_date", "reply_count", "prefix_id",
		"starter_user_id", "starter_username",
	}
	rows := sqlmock.NewRows(header).
		AddRow(2, "B", "t2", 0, "", uint32(0), uint32(0), uint32(0), "open", uint32(0), "visible", uint32(0), "", uint32(0), uint32(0), uint32(0), uint32(0), "", uint32(2000), uint32(0), uint32(0), uint32(0), "").
		AddRow(1, "A", "t1", 0, "", uint32(0), uint32(0), uint32(0), "open", uint32(0), "visible", uint32(0), "", uint32(0), uint32(0), uint32(0), uint32(0), "", uint32(1000), uint32(0), uint32(0), uint32(0), "")
	mock.ExpectQuery(regexp.QuoteMeta("FROM `xf_nf_tickets_ticket`")).WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta("xf_nf_tickets_ticket_field_value")).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id", "field_id", "field_value"}))
	mock.ExpectQuery(regexp.QuoteMeta("xf_nf_tickets_ticket_participant")).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id", "user_id", "last_read_date"}))

	tickets, next, more, err := (&ds).ListTickets(context.Background(), rc, &ListTicketsFilter{PerPage: 1})
	require.NoError(t, err)
	assert.True(t, more)
	require.NotEmpty(t, next)
	require.Len(t, tickets, 1)
	assert.Equal(t, uint32(2), tickets[0].TicketId, "newest first by last_modified_date DESC")
}

func TestListTickets_CursorRoundTrip(t *testing.T) {
	c := encodeCursor(2000, 42)
	ts, id, err := decodeCursor(c)
	require.NoError(t, err)
	assert.Equal(t, uint32(2000), ts)
	assert.Equal(t, uint32(42), id)
}

func TestGetTicket_HappyPath(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()
	rc := newFakeRefCache()

	rows := sqlmock.NewRows([]string{
		"ticket_id", "ticket_ref", "title", "user_id", "username", "start_date",
		"priority", "status_id", "ticket_state", "ticket_locked", "discussion_state",
		"assigned_user_id", "assigned_username", "ticket_category_id",
		"last_message_id", "last_message_date", "last_message_user_id",
		"last_message_username", "last_modified_date", "reply_count", "prefix_id",
		"starter_user_id", "starter_username",
	}).AddRow(7499, "MF1UI9HE", "T", 0, "", uint32(0), uint32(0), uint32(11), "open", uint32(0), "visible", uint32(0), "", uint32(17), uint32(0), uint32(0), uint32(0), "", uint32(0), uint32(0), uint32(0), uint32(0), "")
	mock.ExpectQuery(regexp.QuoteMeta("FROM `xf_nf_tickets_ticket`")).
		WithArgs(uint32(7499), 1).
		WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta("xf_nf_tickets_ticket_field_value")).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id", "field_id", "field_value"}))
	mock.ExpectQuery(regexp.QuoteMeta("xf_nf_tickets_ticket_participant")).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id", "user_id", "last_read_date"}))

	ticket, err := (&ds).GetTicket(context.Background(), rc, 7499, "https://7cav.us")
	require.NoError(t, err)
	require.NotNil(t, ticket)
	assert.Equal(t, "MF1UI9HE", ticket.TicketRef)
	assert.Equal(t, "https://7cav.us/tickets/MF1UI9HE/", ticket.ForumUrl)
}

func TestGetTicket_NotFound(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()
	rc := newFakeRefCache()
	mock.ExpectQuery(regexp.QuoteMeta("FROM `xf_nf_tickets_ticket`")).
		WithArgs(uint32(9999), 1).
		WillReturnRows(sqlmock.NewRows([]string{"ticket_id"}))
	_, err := (&ds).GetTicket(context.Background(), rc, 9999, "")
	require.Error(t, err)
}

func TestGetTicketFirstMessages_HappyPath(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()
	// Count query (returns total count).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT count")).
		WithArgs(uint32(7499), "visible").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(15))
	// Page query (up to N rows).
	mrows := sqlmock.NewRows([]string{
		"message_id", "ticket_id", "user_id", "username",
		"message_date", "message", "message_state",
		"position", "attach_count", "last_edit_date", "edit_count",
	})
	for i := 1; i <= 10; i++ {
		mrows = mrows.AddRow(uint32(i), uint32(7499), uint32(0), "", uint32(0), "m", "visible", uint32(i), uint32(0), uint32(0), uint32(0))
	}
	mock.ExpectQuery(regexp.QuoteMeta("FROM `xf_nf_tickets_message`")).
		WithArgs(uint32(7499), "visible", 10).
		WillReturnRows(mrows)

	msgs, total, err := (&ds).GetTicketFirstMessages(context.Background(), 7499, 10, false)
	require.NoError(t, err)
	assert.Equal(t, uint32(15), total)
	assert.Len(t, msgs, 10)
	assert.Equal(t, uint32(1), msgs[0].Position)
}

func TestListTicketMessages_Pagination(t *testing.T) {
	ds, mock, cleanup := newMockDS(t)
	defer cleanup()
	// 3 messages requested (perPage=2 + 1 lookahead), so 1 lookahead means hasMore=true
	rows := sqlmock.NewRows([]string{
		"message_id", "ticket_id", "user_id", "username",
		"message_date", "message", "message_state",
		"position", "attach_count", "last_edit_date", "edit_count",
	}).
		AddRow(uint32(11), uint32(1), uint32(0), "", uint32(0), "", "visible", uint32(11), uint32(0), uint32(0), uint32(0)).
		AddRow(uint32(12), uint32(1), uint32(0), "", uint32(0), "", "visible", uint32(12), uint32(0), uint32(0), uint32(0)).
		AddRow(uint32(13), uint32(1), uint32(0), "", uint32(0), "", "visible", uint32(13), uint32(0), uint32(0), uint32(0))
	mock.ExpectQuery(regexp.QuoteMeta("FROM `xf_nf_tickets_message`")).
		WithArgs(uint32(1), uint32(10), "visible", 3).
		WillReturnRows(rows)

	msgs, next, more, err := (&ds).ListTicketMessages(context.Background(), 1, 10, 2, false)
	require.NoError(t, err)
	assert.True(t, more)
	assert.Equal(t, uint32(12), next, "cursor is position of last returned message")
	assert.Len(t, msgs, 2)
}

func TestListCategories_PassThroughFromCache(t *testing.T) {
	ds := Mysql{}
	rc := newFakeRefCache()
	cats, err := ds.ListCategories(context.Background(), rc)
	require.NoError(t, err)
	require.Len(t, cats, 2)
	// fakeRefCache.cats is a map; sort the result for stable assertion.
	titles := []string{cats[0].Title, cats[1].Title}
	assert.Contains(t, titles, "S1 Personnel Admin")
	assert.Contains(t, titles, "S1 Citations")
}

func TestDecodeCursor_GarbageReturnsErrInvalidCursor(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"not base64", "garbage-not-base64"},
		{"base64 but not ts:id", base64URL("nope")},
		{"only one int", base64URL("1234")},
		{"empty string after base64 decode", base64URL("")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := decodeCursor(c.in)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidCursor),
				"want errors.Is(err, ErrInvalidCursor); got %v", err)
		})
	}
}

func TestDecodeCursor_ValidRoundTrip(t *testing.T) {
	enc := encodeCursor(1736294298, 7499)
	ts, id, err := decodeCursor(enc)
	require.NoError(t, err)
	assert.Equal(t, uint32(1736294298), ts)
	assert.Equal(t, uint32(7499), id)
}

// base64URL returns base64.RawURLEncoding.EncodeToString([]byte(s)).
func base64URL(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
