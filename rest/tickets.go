package rest

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/types"
	"gorm.io/gorm"
)

// firstMessagesCount is how many thread-opening messages ride along on the
// single-ticket responses — frozen from the old handler.
const firstMessagesCount = 10

// listTickets serves GET /api/v1/tickets: cursor-paginated tickets with the
// conjunctive filter set, golden-pinned by tickets/list_*. All the
// request-side leniency lives in the query binder; the filter field names
// here are the binder's snake_case declarations (camel spellings derived).
// Error message strings frozen from the old stack (servers/grpc ListTickets).
func listTickets(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := newQueryBinder(r.URL.Query())
		filter := &datastores.ListTicketsFilter{
			CategoryIDs:          b.uint32SliceField("category_id"),
			ExcludeSubcategories: b.boolField("exclude_subcategories"),
			TicketStates:         b.stringSliceField("ticket_state"),
			StatusIDs:            b.uint32SliceField("status_id"),
			PrefixIDs:            b.uint32SliceField("prefix_id"),
			AssignedUserIDs:      b.uint32SliceField("assigned_user_id"),
			StarterUserIDs:       b.uint32SliceField("starter_user_id"),
			ModifiedSince:        b.uint32Field("modified_since"),
			IncludeHidden:        b.boolField("include_hidden"),
			PerPage:              b.uint32Field("per_page"),
			AfterCursor:          b.stringField("after_cursor"),
		}
		if b.err != nil {
			writeError(w, r, codeInvalidArgument, "%v", b.err)
			return
		}
		tickets, next, more, err := ds.ListTickets(r.Context(), rc, filter)
		if err != nil {
			if errors.Is(err, datastores.ErrInvalidCursor) {
				writeError(w, r, codeInvalidArgument, "invalid after_cursor")
				return
			}
			writeError(w, r, codeInternal, "list tickets: %v", err)
			return
		}
		out := make([]*types.Ticket, 0, len(tickets))
		for _, t := range tickets {
			out = append(out, ticketFromProto(t))
		}
		writeJSON(w, r, types.ListTicketsResponse{
			Tickets:    out,
			NextCursor: next,
			HasMore:    more,
		})
	})
}

// getTicketById serves GET /api/v1/tickets/{ticket_id}: one ticket plus its
// first messages, golden-pinned by tickets/get_by_id_*. Error message strings
// frozen from the old stack (servers/grpc GetTicket; the parse-error text is
// the gateway's leaked strconv error, pinned by get_by_id_parse_error).
func getTicketById(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticketID, ok := bindTicketID(w, r)
		if !ok {
			return
		}
		ticket, err := ds.GetTicket(r.Context(), rc, ticketID, "")
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, r, codeNotFound, "ticket %d not found", ticketID)
				return
			}
			writeError(w, r, codeInternal, "fetch ticket: %v", err)
			return
		}
		writeTicketResponse(w, r, ds, ticket)
	})
}

// getTicketByRef serves GET /api/v1/tickets/ref/{ticket_ref}: the same
// GetTicketResponse envelope as the by-id binding, addressed by the
// user-facing alphanumeric reference. Golden-pinned by tickets/get_by_ref_*;
// the not-found message quotes the ref (frozen from servers/grpc
// GetTicketByRef).
func getTicketByRef(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := r.PathValue("ticket_ref")
		ticket, err := ds.GetTicketByRef(r.Context(), rc, ref, "")
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				writeError(w, r, codeNotFound, "ticket %q not found", ref)
				return
			}
			writeError(w, r, codeInternal, "fetch ticket: %v", err)
			return
		}
		writeTicketResponse(w, r, ds, ticket)
	})
}

// writeTicketResponse assembles the shared GetTicketResponse envelope (the
// by-id and by-ref bindings return the same shape): the ticket, its first
// messages, and the deprecated top-level total that duplicates
// ticket.totalMessageCount.
func writeTicketResponse(w http.ResponseWriter, r *http.Request, ds datastores.Datastore, ticket *proto.Ticket) {
	msgs, total, err := ds.GetTicketFirstMessages(r.Context(), ticket.GetTicketId(), firstMessagesCount, false)
	if err != nil {
		writeError(w, r, codeInternal, "fetch ticket messages: %v", err)
		return
	}
	writeJSON(w, r, types.GetTicketResponse{
		Ticket:            ticketFromProto(ticket),
		FirstMessages:     messagesFromProto(msgs),
		TotalMessageCount: total,
	})
}

// bindTicketID binds the {ticket_id} path value for the two bindings that
// carry it (by-id, messages). On failure it writes the frozen wire error —
// the old gateway's leaked strconv text, golden-pinned by
// tickets/get_by_id_parse_error and tickets/messages_parse_error — and
// returns ok=false.
func bindTicketID(w http.ResponseWriter, r *http.Request) (uint32, bool) {
	id, err := strconv.ParseUint(r.PathValue("ticket_id"), 10, 32)
	if err != nil {
		writeError(w, r, codeInvalidArgument, "type mismatch, parameter: ticket_id, error: %v", err)
		return 0, false
	}
	return uint32(id), true
}

// refMessagesParity serves GET /api/v1/tickets/ref/messages — old-gateway
// parity for a route-order collision. The gateway's ServeMux.Handle PREPENDED
// patterns, so its match order was the reverse of registration:
// ListCategories → ListTicketMessages → GetTicketByRef → GetTicket →
// ListTickets. /tickets/ref/messages therefore matched
// {ticket_id}/messages FIRST, bound ticket_id="ref", and leaked the strconv
// error below — frozen wire text. The 400 fired inside the gateway before
// the RPC body, where RequireScope lived — so it is deliberately
// scope-INDEPENDENT (any authenticated key sees it; registration in routes()
// skips the scope gate).
func refMessagesParity() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, codeInvalidArgument,
			`type mismatch, parameter: ticket_id, error: strconv.ParseUint: parsing "ref": invalid syntax`)
	})
}

// listTicketMessages serves GET /api/v1/tickets/{ticket_id}/messages: one
// page of the thread, position ascending, golden-pinned by
// tickets/messages_*. The cursor is opaque, meaning "next position to
// include" (inclusive lower bound — position 0 reachable); an unknown ticket
// id yields an empty page, not 404 (frozen). Error message strings frozen
// from the old stack (servers/grpc ListTicketMessages).
func listTicketMessages(ds datastores.Datastore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticketID, ok := bindTicketID(w, r)
		if !ok {
			return
		}
		b := newQueryBinder(r.URL.Query())
		perPage := b.uint32Field("per_page")
		afterCursor := b.stringField("after_cursor")
		includeHidden := b.boolField("include_hidden")
		if b.err != nil {
			writeError(w, r, codeInvalidArgument, "%v", b.err)
			return
		}
		msgs, next, more, err := ds.ListTicketMessages(r.Context(), ticketID, afterCursor, perPage, includeHidden)
		if err != nil {
			if errors.Is(err, datastores.ErrInvalidCursor) {
				writeError(w, r, codeInvalidArgument, "invalid after_cursor")
				return
			}
			writeError(w, r, codeInternal, "list ticket messages: %v", err)
			return
		}
		writeJSON(w, r, types.ListTicketMessagesResponse{
			Messages:   messagesFromProto(msgs),
			NextCursor: next,
			HasMore:    more,
		})
	})
}

// listCategories serves GET /api/v1/tickets/categories: the category
// reference tree, golden-pinned by tickets/categories. Error message string
// frozen from the old handler (servers/grpc ListCategories).
func listCategories(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cats, err := ds.ListCategories(r.Context(), rc)
		if err != nil {
			writeError(w, r, codeInternal, "list ticket categories: %v", err)
			return
		}
		writeJSON(w, r, types.ListCategoriesResponse{Categories: categoriesFromProto(cats)})
	})
}

// ticketFromProto maps one datastore ticket to the wire type (see
// ranksFromProto for the mapping-layer rationale). Allocation discipline:
// participants/ancestors/customFields are always allocated — []/{} on the
// wire, never null.
func ticketFromProto(t *proto.Ticket) *types.Ticket {
	participants := make([]*types.TicketParticipant, 0, len(t.GetParticipants()))
	for _, p := range t.GetParticipants() {
		participants = append(participants, &types.TicketParticipant{
			UserId:       p.GetUserId(),
			LastReadDate: p.GetLastReadDate(),
		})
	}
	ancestors := t.GetCategoryAncestorIds()
	if ancestors == nil {
		ancestors = []uint32{}
	}
	customFields := t.GetCustomFields()
	if customFields == nil {
		customFields = map[string]string{}
	}
	return &types.Ticket{
		TicketId:            t.GetTicketId(),
		TicketRef:           t.GetTicketRef(),
		Title:               t.GetTitle(),
		CategoryId:          t.GetCategoryId(),
		CategoryTitle:       t.GetCategoryTitle(),
		CategoryAncestorIds: ancestors,
		TicketState:         t.GetTicketState(),
		StatusId:            t.GetStatusId(),
		StatusName:          t.GetStatusName(),
		PriorityId:          t.GetPriorityId(),
		PriorityName:        t.GetPriorityName(),
		PrefixId:            t.GetPrefixId(),
		PrefixName:          t.GetPrefixName(),
		DiscussionState:     t.GetDiscussionState(),
		TicketLocked:        t.GetTicketLocked(),
		StarterUserId:       t.GetStarterUserId(),
		StarterUsername:     t.GetStarterUsername(),
		AssignedUserId:      t.GetAssignedUserId(),
		AssignedUsername:    t.GetAssignedUsername(),
		Participants:        participants,
		StartDate:           t.GetStartDate(),
		LastMessageDate:     t.GetLastMessageDate(),
		LastMessageUserId:   t.GetLastMessageUserId(),
		LastMessageUsername: t.GetLastMessageUsername(),
		LastModifiedDate:    t.GetLastModifiedDate(),
		ReplyCount:          t.GetReplyCount(),
		TotalMessageCount:   t.GetTotalMessageCount(),
		CustomFields:        customFields,
		ForumUrl:            t.GetForumUrl(),
	}
}

// messagesFromProto maps datastore messages to the wire types (allocation
// discipline: [] on the wire, never null).
func messagesFromProto(in []*proto.Message) []*types.Message {
	out := make([]*types.Message, 0, len(in))
	for _, m := range in {
		out = append(out, &types.Message{
			MessageId:    m.GetMessageId(),
			TicketId:     m.GetTicketId(),
			UserId:       m.GetUserId(),
			Username:     m.GetUsername(),
			MessageDate:  m.GetMessageDate(),
			Message:      m.GetMessage(),
			MessageState: m.GetMessageState(),
			Position:     m.GetPosition(),
			AttachCount:  m.GetAttachCount(),
			LastEditDate: m.GetLastEditDate(),
			EditCount:    m.GetEditCount(),
		})
	}
	return out
}

// categoriesFromProto maps the datastore's proto-typed rows to the wire
// types (see ranksFromProto for the mapping-layer rationale and the
// allocation discipline).
func categoriesFromProto(in []*proto.Category) []*types.Category {
	out := make([]*types.Category, 0, len(in))
	for _, c := range in {
		out = append(out, &types.Category{
			CategoryId:       c.GetCategoryId(),
			Title:            c.GetTitle(),
			Description:      c.GetDescription(),
			ParentCategoryId: c.GetParentCategoryId(),
			Depth:            c.GetDepth(),
			DisplayOrder:     c.GetDisplayOrder(),
			TicketCount:      c.GetTicketCount(),
		})
	}
	return out
}
