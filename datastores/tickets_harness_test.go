package datastores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

// ticketIDs flattens a ticket slice to ids, preserving order, for
// readable ordering assertions.
func ticketIDs(tickets []*proto.Ticket) []uint32 {
	ids := make([]uint32, len(tickets))
	for i, t := range tickets {
		ids[i] = t.TicketId
	}
	return ids
}

func assertTicketOrder(t *testing.T, tickets []*proto.Ticket, want []uint32) {
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

// The default listing serves visible tickets only, newest activity
// first, with the (last_modified_date, ticket_id) tie broken by higher
// id first — tickets 7 and 6 share a timestamp in the fixtures.
func TestListTickets_DefaultListsVisibleNewestFirst(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
	if err != nil {
		t.Fatalf("ListTickets: %v", err)
	}
	assertTicketOrder(t, tickets, []uint32{1, 2, 3, 4, 7, 6})
	if more || next != "" {
		t.Errorf("single-page result: hasMore=%v next=%q, want false/empty", more, next)
	}
}

// One ticket, fully hydrated: reference-cached names resolved from the
// real phrase and category tables, participants and custom fields
// preloaded, counts derived.
func TestListTickets_HydratesTicketShape(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
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
	assertTicketOrder(t, tickets, []uint32{1, 2, 3, 4, 5, 7, 6})
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
		{"assigned user", datastores.ListTicketsFilter{AssignedUserIDs: []uint32{401}}, []uint32{1, 2, 4, 7, 6}},
		{"starter user", datastores.ListTicketsFilter{StarterUserIDs: []uint32{400}}, []uint32{1}},
		// 1740450000 deliberately sits BETWEEN ticket 4 (1740400000) and
		// ticket 3 (1740500000) so the filter's direction is observable.
		{"modified since", datastores.ListTicketsFilter{ModifiedSince: 1740450000}, []uint32{1, 2, 3}},
		// Exactly ticket 3's last_modified_date: the boundary is
		// INCLUSIVE (tickets.go uses >=, not >) — ticket 3 stays in.
		{"modified since boundary inclusive", datastores.ListTicketsFilter{ModifiedSince: 1740500000}, []uint32{1, 2, 3}},
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

// Cursor pagination walks the full visible set without skips or
// duplicates, two per page.
func TestListTickets_CursorWalksAllPages(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	var seen []uint32
	cursor := ""
	pages := 0
	for {
		tickets, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
			PerPage:     2,
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

	if pages != 3 {
		t.Errorf("6 visible tickets at 2 per page = 3 pages, got %d", pages)
	}
	want := []uint32{1, 2, 3, 4, 7, 6}
	if len(seen) != len(want) {
		t.Fatalf("walked ids = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("walked ids = %v, want %v", seen, want)
		}
	}
}

// A page boundary landing exactly on the shared last_modified_date of
// tickets 7 and 6 must resume INSIDE the tie via the tuple comparison —
// returning 6, not skipping it and not repeating 7.
func TestListTickets_CursorResumesInsideTimestampTie(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	first, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{PerPage: 5})
	if err != nil {
		t.Fatalf("ListTickets(page 1): %v", err)
	}
	assertTicketOrder(t, first, []uint32{1, 2, 3, 4, 7})
	if !more || next == "" {
		t.Fatalf("expected another page, got hasMore=%v next=%q", more, next)
	}

	second, next, more, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{
		PerPage:     5,
		AfterCursor: next,
	})
	if err != nil {
		t.Fatalf("ListTickets(page 2): %v", err)
	}
	assertTicketOrder(t, second, []uint32{6})
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

// forum_url is assembled from the configured FORUM_BASE_URL, trailing
// slashes trimmed; without configuration it stays empty.
func TestListTickets_ForumUrlFromConfiguredBase(t *testing.T) {
	ds, rc := openTicketsHarness(t)

	tickets, _, _, err := ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
	if err != nil {
		t.Fatalf("ListTickets(unconfigured): %v", err)
	}
	if tickets[0].ForumUrl != "" {
		t.Errorf("without FORUM_BASE_URL forum_url must be empty, got %q", tickets[0].ForumUrl)
	}

	prior := viper.GetString("FORUM_BASE_URL")
	viper.Set("FORUM_BASE_URL", "https://forum.example.test/")
	t.Cleanup(func() { viper.Set("FORUM_BASE_URL", prior) })

	tickets, _, _, err = ds.ListTickets(context.Background(), rc, &datastores.ListTicketsFilter{})
	if err != nil {
		t.Fatalf("ListTickets(configured): %v", err)
	}
	if want := "https://forum.example.test/tickets/AA-0001/"; tickets[0].ForumUrl != want {
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
	if ticket.ForumUrl != "https://7cav.us/tickets/AA-0001/" {
		t.Errorf("ForumUrl = %q", ticket.ForumUrl)
	}
	if len(ticket.Participants) != 2 {
		t.Errorf("participants = %v, want both seeded members", ticket.Participants)
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
	if ticket.ForumUrl != "https://7cav.us/tickets/AA-0001/" {
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
// ticket 6899), and each cursor resumes exactly after the last
// returned message.
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
// next visible-only page must serve position 2 — neither stalling on
// the hidden row nor skipping past it.
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

func messagePositions(msgs []*proto.Message) []uint32 {
	out := make([]uint32, len(msgs))
	for i, m := range msgs {
		out[i] = m.Position
	}
	return out
}
