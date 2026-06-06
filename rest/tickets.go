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

// getTicketById serves GET /api/v1/tickets/{ticket_id}: one ticket plus its
// first messages, golden-pinned by tickets/get_by_id_*. Error message strings
// frozen from the old stack (servers/grpc GetTicket; the parse-error text is
// the gateway's leaked strconv error, pinned by get_by_id_parse_error).
func getTicketById(ds datastores.Datastore, rc datastores.TicketReferenceCache) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticketID, err := parseTicketID(r.PathValue("ticket_id"))
		if err != nil {
			writeError(w, r, codeInvalidArgument, "type mismatch, parameter: ticket_id, error: %v", err)
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

// parseTicketID binds the {ticket_id} path value. The error text reaching
// the wire mirrors the old gateway's leaked strconv error verbatim
// (golden-pinned), so this returns the bare strconv error for the caller to
// wrap in the frozen "type mismatch" message.
func parseTicketID(raw string) (uint32, error) {
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(id), nil
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
