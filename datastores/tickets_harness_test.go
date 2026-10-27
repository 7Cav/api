package datastores_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// ticketIDs flattens a ticket slice to ids, preserving order, for
// readable ordering assertions.
func ticketIDs(tickets []*types.Ticket) []uint32 {
	ids := make([]uint32, len(tickets))
	for i, t := range tickets {
		ids[i] = t.TicketId
	}
	return ids
}

func assertTicketOrder(t *testing.T, tickets []*types.Ticket, want []uint32) {
	t.Helper()
	got := ticketIDs(tickets)
	if len(got) != len(want) {
		t.Fatalf("ticket ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ticket ids = %v, want %v", got, want)
		}
	}
}

// The default listing serves visible tickets only, latest last activity
// first, with a tie broken by higher id first. Tickets 7 and 6 share a
// last activity in the fixtures.
func TestListTickets_DefaultListsVisibleNewestFirst(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	assertTicketOrder(t, tickets, []uint32{8, 9, 11, 10, 1, 2, 3, 4, 7, 6})
	if more || next != "" {
		t.Errorf("single-page result: hasMore=%v next=%q, want false/empty", more, next)
	}
}

// One ticket, fully hydrated: reference-cached names resolved from the
// real phrase and category tables, participants and custom fields
// preloaded, counts derived.
func TestListTickets_HydratesTicketShape(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	// Admin Office and its subtree list tickets 1 and 2 first.
	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{CategoryIDs: []uint32{1}})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	got := tickets[0]

	if got.TicketId != 1 || got.TicketRef != "AA-0001" || got.Title != "Cannot access milpacs" {
		t.Errorf("identity = %d %q %q", got.TicketId, got.TicketRef, got.Title)
	}
	if got.StatusId != 1 || got.StatusName != "Awaiting Support" {
		t.Errorf("status = %d %q, want 1 Awaiting Support (resolved from xf_phrase)", got.StatusId, got.StatusName)
	}
	if got.PriorityId != 2 || got.PriorityName != "Normal" {
		t.Errorf("priority = %d %q, want 2 Normal", got.PriorityId, got.PriorityName)
	}
	if got.CategoryId != 1 || got.CategoryTitle != "Admin Office" {
		t.Errorf("category = %d %q, want 1 Admin Office", got.CategoryId, got.CategoryTitle)
	}
	if len(got.CategoryAncestorIds) != 0 {
		t.Errorf("root category has no ancestors, got %v", got.CategoryAncestorIds)
	}
	if got.TicketState != "open" || got.DiscussionState != "visible" || got.TicketLocked {
		t.Errorf("state = %q/%q locked=%v", got.TicketState, got.DiscussionState, got.TicketLocked)
	}
	if got.StarterUserId != 400 || got.StarterUsername != "TicketGuy.G" {
		t.Errorf("starter = %d %q", got.StarterUserId, got.StarterUsername)
	}
	if got.AssignedUserId != 401 || got.AssignedUsername != "Helpdesk.H" {
		t.Errorf("assigned = %d %q", got.AssignedUserId, got.AssignedUsername)
	}
	if got.ReplyCount != 2 || got.TotalMessageCount != 3 {
		t.Errorf("counts = %d replies, %d total; want 2/3 (starter + replies)", got.ReplyCount, got.TotalMessageCount)
	}
	if got.LastMessageUserId != 401 || got.LastMessageUsername != "Helpdesk.H" {
		t.Errorf("last message by = %d %q", got.LastMessageUserId, got.LastMessageUsername)
	}
	if got.LastModifiedDate != 1740700000 || got.StartDate != 1740000000 {
		t.Errorf("dates = start %d modified %d", got.StartDate, got.LastModifiedDate)
	}
	if len(got.Participants) != 2 {
		t.Errorf("participants = %v, want both seeded members", got.Participants)
	}
	if got.CustomFields["discordId"] != "111111111111111111" {
		t.Errorf("CustomFields = %v, want the seeded discordId field", got.CustomFields)
	}

	// A child category resolves its ancestor chain (ticket 2 sits in
	// Recruiting, child of Admin Office) and its prefix phrase.
	child := tickets[1]
	if child.CategoryId != 2 || len(child.CategoryAncestorIds) != 1 || child.CategoryAncestorIds[0] != 1 {
		t.Errorf("ticket 2 ancestors = %v, want [1]", child.CategoryAncestorIds)
	}
	if child.PrefixId != 1 || child.PrefixName != "Urgent" {
		t.Errorf("ticket 2 prefix = %d %q, want 1 Urgent", child.PrefixId, child.PrefixName)
	}
}

// include_hidden pulls non-visible tickets into the listing in their
// chronological slot.
func TestListTickets_IncludeHiddenAddsDeletedTickets(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{IncludeHidden: true})
	if err != nil {
		t.Fatalf("ListTickets(include hidden): %v", err)
	}
	assertTicketOrder(t, tickets, []uint32{8, 9, 11, 10, 1, 2, 3, 4, 5, 7, 6})
}

// The category filter expands to subcategories by default (Admin
// Office pulls in Recruiting); ExcludeSubcategories restricts to the
// named categories only.
func TestListTickets_CategoryFilterExpandsSubtree(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
		CategoryIDs: []uint32{1},
	})
	if err != nil {
		t.Fatalf("ListTickets(category 1): %v", err)
	}
	assertTicketOrder(t, tickets, []uint32{1, 2, 4, 7, 6})

	tickets, _, _, err = ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
		CategoryIDs:          []uint32{1},
		ExcludeSubcategories: true,
	})
	if err != nil {
		t.Fatalf("ListTickets(category 1, exclude subcategories): %v", err)
	}
	assertTicketOrder(t, tickets, []uint32{1, 4, 7})
}

// Every remaining filter knob, against the seeded spread.
func TestListTickets_Filters(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	cases := []struct {
		name   string
		filter datastores.ListTicketsFilter
		want   []uint32
	}{
		{"status ids", datastores.ListTicketsFilter{StatusIDs: []uint32{3}}, []uint32{4, 7}},
		{"repeated status ids", datastores.ListTicketsFilter{StatusIDs: []uint32{2, 3}}, []uint32{2, 4, 7, 6}},
		{"ticket states", datastores.ListTicketsFilter{TicketStates: []string{"resolved"}}, []uint32{4, 7}},
		{"prefix ids", datastores.ListTicketsFilter{PrefixIDs: []uint32{1}}, []uint32{2, 7}},
		{"assigned user", datastores.ListTicketsFilter{AssignedUserIDs: []uint32{401}}, []uint32{9, 1, 2, 4, 7, 6}},
		{"starter user", datastores.ListTicketsFilter{StarterUserIDs: []uint32{400}}, []uint32{1}},
		// 1740450000 deliberately sits BETWEEN ticket 4 (1740400000) and
		// ticket 3 (1740500000) so the filter's direction is observable.
		{"modified since", datastores.ListTicketsFilter{ModifiedSince: 1740450000}, []uint32{8, 9, 11, 10, 1, 2, 3}},
		// Exactly ticket 3's last activity: the boundary is INCLUSIVE, so
		// ticket 3 stays in.
		{"modified since boundary inclusive", datastores.ListTicketsFilter{ModifiedSince: 1740500000}, []uint32{8, 9, 11, 10, 1, 2, 3}},
		{"conjunction", datastores.ListTicketsFilter{StatusIDs: []uint32{3}, PrefixIDs: []uint32{1}}, []uint32{7}},
		{"no match", datastores.ListTicketsFilter{StarterUserIDs: []uint32{9999}}, []uint32{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tickets, _, _, err := ds.ListTickets(context.Background(), rc, &c.filter)
			if err != nil {
				t.Fatalf("ListTickets(%s): %v", c.name, err)
			}
			assertTicketOrder(t, tickets, c.want)
		})
	}
}

// listModifiedSince lists the visible tickets whose last activity is at or
// after since.
func listModifiedSince(t *testing.T, ds *datastores.Mysql, rc datastores.TicketReferenceCache, since uint32) []*types.Ticket {
	t.Helper()
	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{ModifiedSince: since})
	if err != nil {
		t.Fatalf("ListTickets(modifiedSince %d): %v", since, err)
	}
	return tickets
}

// Ticket 8 opened after the forum stopped maintaining
// last_modified_date, so the column reads 0. Its public reply at
// 1793100790 is its last activity, and modifiedSince filters on that.
func TestListTickets_ModifiedSinceFindsReplyOnUnmaintainedColumn(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	const reply = 1793100790
	cases := []struct {
		name  string
		since uint32
		want  []uint32
	}{
		{"at the reply", reply, []uint32{8}},
		{"one second before", reply - 1, []uint32{8}},
		{"one second after", reply + 1, []uint32{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertTicketOrder(t, listModifiedSince(t, ds, rc, c.since), c.want)
		})
	}
}

// Ticket 9's newest event is its assignment at 1793050000, logged long
// after its opening post. A counted log action is last activity.
func TestListTickets_CountedLogActionIsLastActivity(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	const assigned = 1793050000
	tickets := listModifiedSince(t, ds, rc, assigned)
	assertTicketOrder(t, tickets, []uint32{8, 9})
	if got := tickets[1].LastModifiedDate; got != assigned {
		t.Errorf("ticket 9 lastModifiedDate = %d, want its assignment at %d", got, assigned)
	}
}

// Ticket 10's only log rows after its last public reply are user_banned,
// ticket_mention and tags_changed at 1793080000. None of them is ticket
// activity, so modifiedSince at that time leaves ticket 10 out.
func TestListTickets_UncountedLogActionsAreNotActivity(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	const uncounted = 1793080000
	assertTicketOrder(t, listModifiedSince(t, ds, rc, uncounted), []uint32{8})
}

// Ticket 11's last public reply is at 1792900000 and an internal staff
// note follows at 1793070000. The note isn't ticket activity.
func TestListTickets_InternalNoteIsNotActivity(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	const note, reply = 1793070000, 1792900000
	assertTicketOrder(t, listModifiedSince(t, ds, rc, note), []uint32{8})

	tickets := listModifiedSince(t, ds, rc, reply)
	assertTicketOrder(t, tickets, []uint32{8, 9, 11})
	if got := tickets[2].LastModifiedDate; got != reply {
		t.Errorf("ticket 11 lastModifiedDate = %d, want its last public reply at %d", got, reply)
	}
}

// Each counted activity-log action moves a ticket's last activity, and an
// action outside the list doesn't. Each step logs one action on ticket 6,
// newer than every earlier step, so an action missing from the counted
// list fails its own step.
func TestListTickets_OnlyCountedLogActionsAreActivity(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	at := uint32(1800000000)
	logOnTicket6 := func(t *testing.T, action string) {
		t.Helper()
		at += 10
		if err := ds.Db.Exec(`INSERT INTO xf_nf_tickets_ticket_activity_log
			(ticket_id, after_message_id, actor_type, actor_id, action, created_at)
			VALUES (6, 3009, 'user', 401, ?, ?)`, action, at).Error; err != nil {
			t.Fatalf("logging %s: %v", action, err)
		}
	}
	counted := []string{
		"status_changed", "assigned", "priority_changed", "category_moved",
		"title_changed", "prefix_changed", "locked", "unlocked", "made_visible",
		"unapproved", "soft_deleted", "restored", "merged", "ticket_opened",
		"warning_created",
	}
	for _, action := range counted {
		t.Run(action, func(t *testing.T) {
			logOnTicket6(t, action)
			tickets := listModifiedSince(t, ds, rc, at)
			assertTicketOrder(t, tickets, []uint32{6})
			if got := tickets[0].LastModifiedDate; got != at {
				t.Errorf("lastModifiedDate = %d, want the %s row at %d", got, action, at)
			}
		})
	}

	t.Run("an action outside the list", func(t *testing.T) {
		logOnTicket6(t, "some_future_action")
		assertTicketOrder(t, listModifiedSince(t, ds, rc, at), []uint32{})
	})
}

// Cursor pagination walks the full visible set without skips or
// duplicates, one per page, so every ticket is a page boundary. The
// first boundary is ticket 8, whose last_modified_date column reads 0.
func TestListTickets_CursorWalksAllPages(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	var seen []uint32
	cursor := ""
	pages := 0
	for {
		tickets, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
			PerPage:     1,
			AfterCursor: cursor,
		})
		if err != nil {
			t.Fatalf("ListTickets(page %d): %v", pages+1, err)
		}
		seen = append(seen, ticketIDs(tickets)...)
		pages++
		if !more {
			if next != "" {
				t.Errorf("final page must not return a cursor, got %q", next)
			}
			break
		}
		if next == "" {
			t.Fatal("hasMore without a cursor")
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}

	want := []uint32{8, 9, 11, 10, 1, 2, 3, 4, 7, 6}
	if pages != len(want) {
		t.Errorf("%d visible tickets at 1 per page = %d pages, got %d", len(want), len(want), pages)
	}
	if len(seen) != len(want) {
		t.Fatalf("walked ids = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("walked ids = %v, want %v", seen, want)
		}
	}
}

// A page boundary landing exactly on the shared last activity of
// tickets 7 and 6 must resume INSIDE the tie, returning 6, not skipping
// it and not repeating 7.
func TestListTickets_CursorResumesInsideTimestampTie(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	first, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{PerPage: 9})
	if err != nil {
		t.Fatalf("ListTickets(page 1): %v", err)
	}
	assertTicketOrder(t, first, []uint32{8, 9, 11, 10, 1, 2, 3, 4, 7})
	if !more || next == "" {
		t.Fatalf("expected another page, got hasMore=%v next=%q", more, next)
	}

	second, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
		PerPage:     9,
		AfterCursor: next,
	})
	if err != nil {
		t.Fatalf("ListTickets(page 2): %v", err)
	}
	assertTicketOrder(t, second, []uint32{6})
	if seven, six := first[len(first)-1], second[0]; seven.LastModifiedDate != six.LastModifiedDate {
		t.Fatalf("fixtures no longer tie: ticket 7 at %d, ticket 6 at %d, so the tie-break goes untested",
			seven.LastModifiedDate, six.LastModifiedDate)
	}
	if more || next != "" {
		t.Errorf("exhausted listing: hasMore=%v next=%q, want false/empty", more, next)
	}
}

func TestListTickets_MalformedCursorIsInvalidCursorError(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	_, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
		AfterCursor: "garbage-not-base64",
	})
	if !errors.Is(err, datastores.ErrInvalidCursor) {
		t.Errorf("want ErrInvalidCursor (handlers map it to 400), got %v", err)
	}
}

// forumUrl is assembled from the configured FORUM_BASE_URL, trailing
// slashes trimmed; without configuration it stays empty.
func TestListTickets_ForumUrlFromConfiguredBase(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
	if err != nil {
		t.Fatalf("ListTickets(unconfigured): %v", err)
	}
	if tickets[0].ForumUrl != "" {
		t.Errorf("without FORUM_BASE_URL forumUrl must be empty, got %q", tickets[0].ForumUrl)
	}

	prior := viper.GetString("FORUM_BASE_URL")
	viper.Set("FORUM_BASE_URL", "https://forum.example.test/")
	t.Cleanup(func() { viper.Set("FORUM_BASE_URL", prior) })

	tickets, _, _, err = ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
	if err != nil {
		t.Fatalf("ListTickets(configured): %v", err)
	}
	if want := "https://forum.example.test/tickets/8/"; tickets[0].ForumUrl != want {
		t.Errorf("ForumUrl = %q, want %q", tickets[0].ForumUrl, want)
	}
}

func TestGetTicket_ByIdHappyPath(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	ticket, err := ds.GetTicket(context.Background(), rc, 1, "https://7cav.us")
	if err != nil {
		t.Fatalf("GetTicket(1): %v", err)
	}
	if ticket.TicketRef != "AA-0001" || ticket.Title != "Cannot access milpacs" {
		t.Errorf("got %q %q", ticket.TicketRef, ticket.Title)
	}
	if ticket.StatusName != "Awaiting Support" {
		t.Errorf("StatusName = %q, want Awaiting Support", ticket.StatusName)
	}
	if ticket.ForumUrl != "https://7cav.us/tickets/1/" {
		t.Errorf("ForumUrl = %q", ticket.ForumUrl)
	}
	if len(ticket.Participants) != 2 {
		t.Errorf("participants = %v, want both seeded members", ticket.Participants)
	}
}

// Get by id and get by ref serve last activity as lastModifiedDate, the
// same as the list: ticket 8's public reply and ticket 9's assignment.
func TestGetTicket_LastModifiedDateIsLastActivity(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	byID, err := ds.GetTicket(context.Background(), rc, 8, "")
	if err != nil {
		t.Fatalf("GetTicket(8): %v", err)
	}
	if want := uint32(1793100790); byID.LastModifiedDate != want {
		t.Errorf("by id: ticket 8 lastModifiedDate = %d, want its public reply at %d", byID.LastModifiedDate, want)
	}

	byRef, err := ds.GetTicketByRef(context.Background(), rc, "SE-0009", "")
	if err != nil {
		t.Fatalf("GetTicketByRef(SE-0009): %v", err)
	}
	if want := uint32(1793050000); byRef.LastModifiedDate != want {
		t.Errorf("by ref: ticket 9 lastModifiedDate = %d, want its assignment at %d", byRef.LastModifiedDate, want)
	}
}

// OBSERVED behavior: GetTicket/GetTicketByRef apply NO discussion_state
// filter — the visibility filtering of ListTickets is list-only, and a
// deleted ticket stays directly retrievable by id or ref.
func TestGetTicket_DeletedTicketIsRetrievableDirectly(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	ticket, err := ds.GetTicket(context.Background(), rc, 5, "")
	if err != nil {
		t.Fatalf("GetTicket(5, deleted): %v", err)
	}
	if ticket.TicketId != 5 || ticket.DiscussionState != "deleted" {
		t.Errorf("got ticket %d state %q, want 5/deleted", ticket.TicketId, ticket.DiscussionState)
	}

	byRef, err := ds.GetTicketByRef(context.Background(), rc, "TS-0005", "")
	if err != nil {
		t.Fatalf("GetTicketByRef(TS-0005, deleted): %v", err)
	}
	if byRef.TicketId != 5 || byRef.DiscussionState != "deleted" {
		t.Errorf("got ticket %d state %q, want 5/deleted", byRef.TicketId, byRef.DiscussionState)
	}
}

func TestGetTicket_UnknownIdIsRecordNotFound(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	_, err := ds.GetTicket(context.Background(), rc, 9999, "")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("want gorm.ErrRecordNotFound (handlers map it to 404), got %v", err)
	}
}

func TestGetTicketByRef(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	ticket, err := ds.GetTicketByRef(context.Background(), rc, "AA-0001", "https://7cav.us")
	if err != nil {
		t.Fatalf("GetTicketByRef(AA-0001): %v", err)
	}
	if ticket.TicketId != 1 {
		t.Errorf("TicketId = %d, want 1", ticket.TicketId)
	}
	if ticket.ForumUrl != "https://7cav.us/tickets/1/" {
		t.Errorf("ForumUrl = %q", ticket.ForumUrl)
	}

	_, err = ds.GetTicketByRef(context.Background(), rc, "ZZ-9999", "")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("unknown ref: want gorm.ErrRecordNotFound, got %v", err)
	}
}

// First-messages preview: hidden replies are excluded from both the
// page and the total unless requested; the page is position-ordered
// and capped at n while the total counts the whole ticket.
func TestGetTicketFirstMessages(t *testing.T) {
	ds, _ := openTicketsHarness(t)

	msgs, total, err := ds.GetTicketFirstMessages(context.Background(), 1, 10, false)
	if err != nil {
		t.Fatalf("GetTicketFirstMessages(visible): %v", err)
	}
	if total != 2 {
		t.Errorf("visible total = %d, want 2 (hidden internal note excluded)", total)
	}
	if len(msgs) != 2 || msgs[0].Position != 0 || msgs[1].Position != 2 {
		t.Fatalf("visible positions = %v, want [0 2]", messagePositions(msgs))
	}
	if msgs[0].Message != "I cannot see my milpacs page." || msgs[0].Username != "TicketGuy.G" {
		t.Errorf("starter message = %q by %q", msgs[0].Message, msgs[0].Username)
	}

	msgs, total, err = ds.GetTicketFirstMessages(context.Background(), 1, 10, true)
	if err != nil {
		t.Fatalf("GetTicketFirstMessages(include hidden): %v", err)
	}
	if total != 3 || len(msgs) != 3 || msgs[1].MessageState != "hidden" {
		t.Errorf("with hidden: total=%d positions=%v state[1]=%q, want 3/[0 1 2]/hidden",
			total, messagePositions(msgs), msgs[1].MessageState)
	}

	// n caps the page, not the total.
	msgs, total, err = ds.GetTicketFirstMessages(context.Background(), 1, 1, false)
	if err != nil {
		t.Fatalf("GetTicketFirstMessages(n=1): %v", err)
	}
	if len(msgs) != 1 || total != 2 {
		t.Errorf("n=1: got %d messages, total %d; want 1 message, total 2", len(msgs), total)
	}
}

// The message cursor means "next position to include": page one with no
// cursor starts at the starter post (position 0 — regression: smoke
// ticket 6899, where the empty cursor was conflated with position 0 and
// the starter post dropped), and each cursor resumes exactly after the
// last returned message.
func TestListTicketMessages_CursorPagination(t *testing.T) {
	ds, _ := openTicketsHarness(t)

	// Page 1, no cursor: position 0 must be present.
	msgs, next, more, err := ds.ListTicketMessages(context.Background(), 1, "", 1, true)
	if err != nil {
		t.Fatalf("ListTicketMessages(page 1): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Position != 0 {
		t.Fatalf("page 1 positions = %v, want [0]", messagePositions(msgs))
	}
	if !more || next == "" {
		t.Fatalf("expected more pages, got hasMore=%v next=%q", more, next)
	}

	// Page 2 resumes at position 1 (the hidden internal note).
	msgs, next, more, err = ds.ListTicketMessages(context.Background(), 1, next, 1, true)
	if err != nil {
		t.Fatalf("ListTicketMessages(page 2): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Position != 1 || msgs[0].MessageState != "hidden" {
		t.Fatalf("page 2 = positions %v state %q, want [1] hidden", messagePositions(msgs), msgs[0].MessageState)
	}
	if !more {
		t.Fatal("expected a third page")
	}

	// Page 3 is the last.
	msgs, next, more, err = ds.ListTicketMessages(context.Background(), 1, next, 1, true)
	if err != nil {
		t.Fatalf("ListTicketMessages(page 3): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Position != 2 {
		t.Fatalf("page 3 positions = %v, want [2]", messagePositions(msgs))
	}
	if more || next != "" {
		t.Errorf("exhausted thread: hasMore=%v next=%q, want false/empty", more, next)
	}
}

// Hidden messages are filtered out of the default listing.
func TestListTicketMessages_HiddenFilteredByDefault(t *testing.T) {
	ds, _ := openTicketsHarness(t)

	msgs, _, more, err := ds.ListTicketMessages(context.Background(), 1, "", 0, false)
	if err != nil {
		t.Fatalf("ListTicketMessages(visible): %v", err)
	}
	if more {
		t.Error("two visible messages fit one default page")
	}
	if len(msgs) != 2 || msgs[0].Position != 0 || msgs[1].Position != 2 {
		t.Errorf("visible positions = %v, want [0 2]", messagePositions(msgs))
	}
}

// A perPage=1 cursored walk crosses the hidden gap: the cursor handed
// back after position 0 points at position 1 (the hidden note), and the
// next visible-only page must advance past the hidden row and serve
// position 2 — without stalling on the hidden row and without skipping
// the next visible message.
func TestListTicketMessages_CursorWalksAcrossHiddenGap(t *testing.T) {
	ds, _ := openTicketsHarness(t)

	msgs, next, more, err := ds.ListTicketMessages(context.Background(), 1, "", 1, false)
	if err != nil {
		t.Fatalf("ListTicketMessages(page 1, visible): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Position != 0 {
		t.Fatalf("page 1 positions = %v, want [0]", messagePositions(msgs))
	}
	if !more || next == "" {
		t.Fatalf("expected another visible page, got hasMore=%v next=%q", more, next)
	}

	msgs, next, more, err = ds.ListTicketMessages(context.Background(), 1, next, 1, false)
	if err != nil {
		t.Fatalf("ListTicketMessages(page 2, visible): %v", err)
	}
	if len(msgs) != 1 || msgs[0].Position != 2 {
		t.Fatalf("page 2 positions = %v, want [2] (hidden position 1 crossed, not served)", messagePositions(msgs))
	}
	if more || next != "" {
		t.Errorf("exhausted visible thread: hasMore=%v next=%q, want false/empty", more, next)
	}
}

// Message listings for a ticket that does not exist are empty results,
// not errors — neither method probes ticket existence.
func TestTicketMessages_NonexistentTicketIsEmptyNotError(t *testing.T) {
	ds, _ := openTicketsHarness(t)

	msgs, total, err := ds.GetTicketFirstMessages(context.Background(), 9999, 10, true)
	if err != nil {
		t.Fatalf("GetTicketFirstMessages(9999): %v", err)
	}
	if len(msgs) != 0 || total != 0 {
		t.Errorf("nonexistent ticket: got %d messages, total %d; want 0/0", len(msgs), total)
	}

	listed, next, more, err := ds.ListTicketMessages(context.Background(), 9999, "", 10, true)
	if err != nil {
		t.Fatalf("ListTicketMessages(9999): %v", err)
	}
	if len(listed) != 0 || next != "" || more {
		t.Errorf("nonexistent ticket: got %d messages, next %q, hasMore %v; want 0/empty/false", len(listed), next, more)
	}
}

func TestListTicketMessages_MalformedCursorIsInvalidCursorError(t *testing.T) {
	ds, _ := openTicketsHarness(t)

	_, _, _, err := ds.ListTicketMessages(context.Background(), 1, "garbage-not-base64", 10, false)
	if !errors.Is(err, datastores.ErrInvalidCursor) {
		t.Errorf("want ErrInvalidCursor (handlers map it to 400), got %v", err)
	}
}

// Categories come from the reference cache's nested-set tree, in lft
// (tree) order, with hierarchy fields intact.
func TestListCategories_ServesCategoryTree(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	cats, err := ds.ListCategories(context.Background(), rc)
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(cats) != 3 {
		t.Fatalf("want 3 seeded categories, got %d", len(cats))
	}

	wantTitles := []string{"Admin Office", "Recruiting", "Tech Support"}
	for i, want := range wantTitles {
		if cats[i].Title != want {
			t.Errorf("cats[%d] = %q, want %q (lft order)", i, cats[i].Title, want)
		}
	}
	child := cats[1]
	if child.CategoryId != 2 || child.ParentCategoryId != 1 || child.Depth != 1 {
		t.Errorf("Recruiting = id %d parent %d depth %d, want 2/1/1", child.CategoryId, child.ParentCategoryId, child.Depth)
	}
	if child.Description != "Enlistment paperwork" || child.TicketCount != 2 {
		t.Errorf("Recruiting = %q count %d", child.Description, child.TicketCount)
	}
}

func messagePositions(msgs []*types.Message) []uint32 {
	out := make([]uint32, len(msgs))
	for i, m := range msgs {
		out[i] = m.Position
	}
	return out
}

// A must-be-populated phrase family (status) that reads zero rows from
// xf_phrase must surface a Warn naming the family and still return an empty
// map with a nil error — the refresh must NOT hard-fail on one drifted
// family (#195). This drives the real loader against the MariaDB harness
// with the status phrases deleted to reproduce the production drift mode.
func TestLoadStatusNames_ZeroRowsWarnsButDoesNotFail(t *testing.T) {
	ds := openHarnessDatastore(t)

	// Remove the seeded status phrases so the (correct) LIKE prefix now
	// matches zero rows — the exact silent-blank mode #189 fixed at the
	// prefix level and #195 guards one layer down.
	if err := ds.Db.Exec(`DELETE FROM xf_phrase WHERE title LIKE 'nf_tickets_status.%'`).Error; err != nil {
		t.Fatalf("clearing status phrases: %v", err)
	}

	var buf bytes.Buffer
	prev := datastores.Warn.Writer()
	datastores.Warn.SetOutput(&buf)
	t.Cleanup(func() { datastores.Warn.SetOutput(prev) })

	names, err := ds.LoadStatusNames(context.Background())
	if err != nil {
		t.Fatalf("LoadStatusNames must not hard-fail on a zero-row read, got %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("status phrases were deleted, want empty map, got %v", names)
	}
	logged := buf.String()
	if logged == "" {
		t.Fatal("an empty status family must surface a Warn, got silence (the #195 silent-blank mode)")
	}
	if !strings.Contains(strings.ToLower(logged), "status") {
		t.Errorf("the Warn must name the status family, got %q", logged)
	}
}
