package rest

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/7cav/api/datastores"
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
		b, ok := bindListQuery(w, r)
		if !ok {
			return
		}
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
		if err := b.Err(); err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		tickets, next, more, err := ds.ListTickets(r.Context(), rc, filter)
		if err != nil {
			if errors.Is(err, datastores.ErrInvalidCursor) {
				// Frozen wire text stays opaque; keep the wrapped decode
				// detail server-side as a debug trail.
				Info.Printf("%s %s: %v", r.Method, r.URL.Path, err)
				writeError(w, r, codeInvalidArgument, "invalid after_cursor")
				return
			}
			writeError(w, r, codeInternal, "list tickets: %v", err)
			return
		}
		out := tickets
		if out == nil {
			out = []*types.Ticket{}
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
// messages, and the deprecated top-level total that mirrors
// ticket.totalMessageCount but is computed independently (visible-message
// COUNT vs replyCount+1 — they diverge on hidden messages; see
// types.GetTicketResponse).
func writeTicketResponse(w http.ResponseWriter, r *http.Request, ds datastores.Datastore, ticket *types.Ticket) {
	if ticket == nil {
		// A (nil, nil) datastore return is a bug — fail loudly rather than
		// serve a zeroed-garbage 200.
		writeError(w, r, codeInternal, "fetch ticket: nil ticket")
		return
	}
	msgs, total, err := ds.GetTicketFirstMessages(r.Context(), ticket.TicketId, firstMessagesCount, false)
	if err != nil {
		writeError(w, r, codeInternal, "fetch ticket messages: %v", err)
		return
	}
	if msgs == nil {
		msgs = []*types.Message{}
	}
	writeJSON(w, r, types.GetTicketResponse{
		Ticket:            ticket,
		FirstMessages:     msgs,
		TotalMessageCount: total,
	})
}

// bindListQuery parses the query string STRICTLY for the two list routes and
// returns their binder. The old generated handlers for ListTickets and
// ListTicketMessages called req.ParseForm() (proto/tickets.pb.gw.go) and
// 400'd malformed query syntax with the parse error verbatim ("%v", frozen)
// — r.URL.Query()'s silent drop would diverge. The other tickets routes
// never called ParseForm, so they deliberately stay lenient.
func bindListQuery(w http.ResponseWriter, r *http.Request) (*queryBinder, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, codeInvalidArgument, "%v", err)
		return nil, false
	}
	return newQueryBinder(q), true
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
// skips the scope gate). That is a deliberate ASYMMETRY against the
// scope-precedes-binding ruling (requireScope doc): this shim is an
// exact-path parity reproduction of a gateway-level 400 that predated scope
// in the old stack, and the auth tiers still 401 ahead of it (auth runs
// before routing) — so its layering is 401 → frozen 400, never a 403.
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
		b, ok := bindListQuery(w, r)
		if !ok {
			return
		}
		perPage := b.uint32Field("per_page")
		afterCursor := b.stringField("after_cursor")
		includeHidden := b.boolField("include_hidden")
		if err := b.Err(); err != nil {
			writeError(w, r, codeInvalidArgument, "%v", err)
			return
		}
		msgs, next, more, err := ds.ListTicketMessages(r.Context(), ticketID, afterCursor, perPage, includeHidden)
		if err != nil {
			if errors.Is(err, datastores.ErrInvalidCursor) {
				// Frozen wire text stays opaque; keep the wrapped decode
				// detail server-side as a debug trail.
				Info.Printf("%s %s: %v", r.Method, r.URL.Path, err)
				writeError(w, r, codeInvalidArgument, "invalid after_cursor")
				return
			}
			writeError(w, r, codeInternal, "list ticket messages: %v", err)
			return
		}
		if msgs == nil {
			msgs = []*types.Message{}
		}
		writeJSON(w, r, types.ListTicketMessagesResponse{
			Messages:   msgs,
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
		if cats == nil {
			cats = []*types.Category{}
		}
		writeJSON(w, r, types.ListCategoriesResponse{Categories: cats})
	})
}
