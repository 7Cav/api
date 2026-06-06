package rest_test

// The tickets half of fakeDatastore: a copy of the recording seed
// (contract/fake_datastore_test.go) for everything the tickets routes serve
// — the two seeds MUST agree or golden replay against the new stack proves
// nothing. The recording seed lives in package contract's test files, so it
// cannot be imported; this file mirrors it verbatim (same world: tickets
// 44/43/42 sorted last_modified DESC, the ticket-42 message thread, the
// three-node category tree, the exact cursor formats from
// datastores/tickets.go).

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"gorm.io/gorm"
)

// seedTickets returns the ticket world sorted by last_modified_date DESC,
// ticket_id DESC — the order the real datastore queries in.
func seedTickets() []*proto.Ticket {
	return []*proto.Ticket{
		{
			TicketId:            44,
			TicketRef:           "ZZTOP44Q",
			Title:               "Transfer request: Bravo to Alpha",
			CategoryId:          5,
			CategoryTitle:       "S1 Personnel",
			CategoryAncestorIds: []uint32{1},
			TicketState:         "open",
			StatusId:            2,
			StatusName:          "In Progress",
			PriorityId:          2,
			PriorityName:        "Normal",
			PrefixId:            3,
			PrefixName:          "Transfer",
			DiscussionState:     "visible",
			TicketLocked:        false,
			StarterUserId:       8,
			StarterUsername:     "John.Doe",
			AssignedUserId:      3,
			AssignedUsername:    "Jarvis.A",
			Participants: []*proto.TicketParticipant{
				{UserId: 8, LastReadDate: 1748690000},
				{UserId: 3, LastReadDate: 1748695000},
			},
			StartDate:           1748600000,
			LastMessageDate:     1748699000,
			LastMessageUserId:   3,
			LastMessageUsername: "Jarvis.A",
			LastModifiedDate:    1748700000,
			ReplyCount:          1,
			TotalMessageCount:   2,
			CustomFields:        map[string]string{"department": "S1", "billet": "Rifleman"},
			ForumUrl:            "https://7cav.us/tickets/ZZTOP44Q/",
		},
		{
			TicketId:            43,
			TicketRef:           "QQWW43RR",
			Title:               "TeamSpeak <connection> issues & errors",
			CategoryId:          7,
			CategoryTitle:       "S6 Technical Support",
			CategoryAncestorIds: []uint32{},
			TicketState:         "resolved",
			StatusId:            5,
			StatusName:          "Resolved",
			PriorityId:          1,
			PriorityName:        "Low",
			DiscussionState:     "visible",
			StarterUserId:       3,
			StarterUsername:     "Jarvis.A",
			Participants:        []*proto.TicketParticipant{},
			StartDate:           1748500000,
			LastMessageDate:     1748590000,
			LastMessageUserId:   3,
			LastMessageUsername: "Jarvis.A",
			LastModifiedDate:    1748600000,
			ReplyCount:          0,
			TotalMessageCount:   1,
			CustomFields:        map[string]string{},
			ForumUrl:            "https://7cav.us/tickets/QQWW43RR/",
		},
		{
			TicketId:            42,
			TicketRef:           "MF1UI9HE",
			Title:               "Enlistment — John.Doe",
			CategoryId:          1,
			CategoryTitle:       "Recruiting",
			CategoryAncestorIds: []uint32{},
			TicketState:         "open",
			StatusId:            1,
			StatusName:          "New",
			PriorityId:          3,
			PriorityName:        "High",
			PrefixId:            1,
			PrefixName:          "Enlistment",
			DiscussionState:     "visible",
			StarterUserId:       8,
			StarterUsername:     "John.Doe",
			Participants:        []*proto.TicketParticipant{{UserId: 8, LastReadDate: 1748450000}},
			StartDate:           1748400000,
			LastMessageDate:     1748480000,
			LastMessageUserId:   8,
			LastMessageUsername: "John.Doe",
			LastModifiedDate:    1748500000,
			ReplyCount:          2,
			TotalMessageCount:   3,
			CustomFields:        map[string]string{"recruiter": "Jarvis.A"},
			ForumUrl:            "https://7cav.us/tickets/MF1UI9HE/",
		},
	}
}

// seedMessages42 is the message thread for ticket 42, position ascending.
func seedMessages42() []*proto.Message {
	return []*proto.Message{
		{
			MessageId:    9001,
			TicketId:     42,
			UserId:       8,
			Username:     "John.Doe",
			MessageDate:  1748400000,
			Message:      "[B]Enlistment[/B] request — I'd like to join the 7th Cavalry & start ASAP <o7>",
			MessageState: "visible",
			Position:     0,
			AttachCount:  0,
		},
		{
			MessageId:    9002,
			TicketId:     42,
			UserId:       3,
			Username:     "Jarvis.A",
			MessageDate:  1748450000,
			Message:      "Welcome aboard — processing now.",
			MessageState: "visible",
			Position:     1,
			AttachCount:  1,
			LastEditDate: 1748451000,
			EditCount:    1,
		},
		{
			MessageId:    9003,
			TicketId:     42,
			UserId:       8,
			Username:     "John.Doe",
			MessageDate:  1748480000,
			Message:      "Thank you!",
			MessageState: "visible",
			Position:     2,
		},
	}
}

// Cursor helpers mirroring datastores/tickets.go exactly.
func encodeTicketCursor(lastModified, ticketID uint32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", lastModified, ticketID)))
}

func decodeTicketCursor(c string) (uint32, uint32, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: expected ts:id", datastores.ErrInvalidCursor)
	}
	ts, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	id, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	return uint32(ts), uint32(id), nil
}

func encodeMessageCursor(position uint32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(position), 10)))
}

func decodeMessageCursor(c string) (uint32, error) {
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	pos, err := strconv.ParseUint(string(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	return uint32(pos), nil
}

func (f *fakeDatastore) ListTickets(_ context.Context, rc datastores.TicketReferenceCache, flt *datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
	f.lastRC = rc
	if f.listTickets != nil {
		return f.listTickets(flt)
	}
	perPage := flt.PerPage
	if perPage == 0 {
		perPage = 50
	} else if perPage > 100 {
		perPage = 100
	}

	var afterTs, afterId uint32
	hasCursor := flt.AfterCursor != ""
	if hasCursor {
		var err error
		afterTs, afterId, err = decodeTicketCursor(flt.AfterCursor)
		if err != nil {
			return nil, "", false, err
		}
	}

	var rows []*proto.Ticket
	for _, t := range seedTickets() {
		if hasCursor && !(t.LastModifiedDate < afterTs || (t.LastModifiedDate == afterTs && t.TicketId < afterId)) {
			continue
		}
		if !matchTicket(t, flt) {
			continue
		}
		rows = append(rows, t)
	}

	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}
	next := ""
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = encodeTicketCursor(last.LastModifiedDate, last.TicketId)
	}
	if rows == nil {
		rows = []*proto.Ticket{}
	}
	return rows, next, hasMore, nil
}

func matchTicket(t *proto.Ticket, f *datastores.ListTicketsFilter) bool {
	if len(f.CategoryIDs) > 0 {
		match := containsU32(f.CategoryIDs, t.CategoryId)
		if !match && !f.ExcludeSubcategories {
			for _, anc := range t.CategoryAncestorIds {
				if containsU32(f.CategoryIDs, anc) {
					match = true
					break
				}
			}
		}
		if !match {
			return false
		}
	}
	if len(f.TicketStates) > 0 && !containsStr(f.TicketStates, t.TicketState) {
		return false
	}
	if len(f.StatusIDs) > 0 && !containsU32(f.StatusIDs, t.StatusId) {
		return false
	}
	if len(f.PrefixIDs) > 0 && !containsU32(f.PrefixIDs, t.PrefixId) {
		return false
	}
	if len(f.AssignedUserIDs) > 0 && !containsU32(f.AssignedUserIDs, t.AssignedUserId) {
		return false
	}
	if len(f.StarterUserIDs) > 0 && !containsU32(f.StarterUserIDs, t.StarterUserId) {
		return false
	}
	if f.ModifiedSince > 0 && t.LastModifiedDate < f.ModifiedSince {
		return false
	}
	return true
}

func containsU32(haystack []uint32, needle uint32) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func containsStr(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func (f *fakeDatastore) GetTicket(_ context.Context, rc datastores.TicketReferenceCache, ticketID uint32, _ string) (*proto.Ticket, error) {
	f.lastRC = rc
	if f.getTicket != nil {
		return f.getTicket(ticketID)
	}
	for _, t := range seedTickets() {
		if t.TicketId == ticketID {
			return t, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeDatastore) GetTicketByRef(_ context.Context, rc datastores.TicketReferenceCache, ref string, _ string) (*proto.Ticket, error) {
	f.lastRC = rc
	for _, t := range seedTickets() {
		if t.TicketRef == ref {
			return t, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeDatastore) GetTicketFirstMessages(_ context.Context, ticketID uint32, n int, _ bool) ([]*proto.Message, uint32, error) {
	if f.getTicketFirstMessages != nil {
		return f.getTicketFirstMessages(ticketID, n)
	}
	if ticketID != 42 {
		return []*proto.Message{}, 0, nil
	}
	msgs := seedMessages42()
	total := uint32(len(msgs))
	if n < len(msgs) {
		msgs = msgs[:n]
	}
	return msgs, total, nil
}

func (f *fakeDatastore) ListTicketMessages(_ context.Context, ticketID uint32, afterCursor string, perPage uint32, _ bool) ([]*proto.Message, string, bool, error) {
	if f.listTicketMessages != nil {
		return f.listTicketMessages(ticketID, afterCursor, perPage)
	}
	from, err := decodeMessageCursor(afterCursor)
	if err != nil {
		return nil, "", false, err
	}
	if perPage == 0 {
		perPage = 50
	} else if perPage > 100 {
		perPage = 100
	}
	var rows []*proto.Message
	if ticketID == 42 {
		for _, m := range seedMessages42() {
			if m.Position >= from {
				rows = append(rows, m)
			}
		}
	}
	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}
	next := ""
	if hasMore && len(rows) > 0 {
		next = encodeMessageCursor(rows[len(rows)-1].Position + 1)
	}
	if rows == nil {
		rows = []*proto.Message{}
	}
	return rows, next, hasMore, nil
}

func (f *fakeDatastore) ListCategories(_ context.Context, rc datastores.TicketReferenceCache) ([]*proto.Category, error) {
	f.lastRC = rc
	if f.listCategories != nil {
		return f.listCategories()
	}
	return []*proto.Category{
		{CategoryId: 1, Title: "Recruiting", Description: "Enlistment & recruiting questions", ParentCategoryId: 0, Depth: 0, DisplayOrder: 10, TicketCount: 120},
		{CategoryId: 5, Title: "S1 Personnel", Description: "", ParentCategoryId: 1, Depth: 1, DisplayOrder: 20, TicketCount: 34},
		{CategoryId: 7, Title: "S6 Technical Support", Description: "Teamspeak, forum & game-server help", ParentCategoryId: 0, Depth: 0, DisplayOrder: 30, TicketCount: 78},
	}, nil
}
