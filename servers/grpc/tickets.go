package grpc

import (
	"context"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
)

// TicketsService implements proto.TicketsServiceServer.
type TicketsService struct {
	proto.UnimplementedTicketsServiceServer

	Datastore      datastores.Datastore
	ReferenceCache referencecache.ReferenceCache
}

func (s *TicketsService) ListTickets(ctx context.Context, req *proto.ListTicketsRequest) (*proto.ListTicketsResponse, error) {
	if err := RequireScope(ctx, "read:tickets"); err != nil {
		return nil, err
	}
	filter := &datastores.ListTicketsFilter{
		CategoryIDs:          req.CategoryId,
		ExcludeSubcategories: req.ExcludeSubcategories,
		TicketStates:         req.TicketState,
		StatusIDs:            req.StatusId,
		PrefixIDs:            req.PrefixId,
		AssignedUserIDs:      req.AssignedUserId,
		StarterUserIDs:       req.StarterUserId,
		ModifiedSince:        req.ModifiedSince,
		IncludeHidden:        req.IncludeHidden,
		PerPage:              req.PerPage,
		AfterCursor:          req.AfterCursor,
	}
	tickets, next, more, err := s.Datastore.ListTickets(ctx, s.ReferenceCache, filter)
	if err != nil {
		return nil, err
	}
	return &proto.ListTicketsResponse{
		Tickets:    tickets,
		NextCursor: next,
		HasMore:    more,
	}, nil
}

const firstMessagesCount = 10

func (s *TicketsService) GetTicket(ctx context.Context, req *proto.GetTicketRequest) (*proto.GetTicketResponse, error) {
	if err := RequireScope(ctx, "read:tickets"); err != nil {
		return nil, err
	}
	ticket, err := s.Datastore.GetTicket(ctx, s.ReferenceCache, req.TicketId, "")
	if err != nil {
		return nil, err
	}
	msgs, total, err := s.Datastore.GetTicketFirstMessages(ctx, ticket.TicketId, firstMessagesCount, false)
	if err != nil {
		return nil, err
	}
	return &proto.GetTicketResponse{
		Ticket:            ticket,
		FirstMessages:     msgs,
		TotalMessageCount: total,
	}, nil
}

func (s *TicketsService) GetTicketByRef(ctx context.Context, req *proto.GetTicketByRefRequest) (*proto.GetTicketResponse, error) {
	if err := RequireScope(ctx, "read:tickets"); err != nil {
		return nil, err
	}
	ticket, err := s.Datastore.GetTicketByRef(ctx, s.ReferenceCache, req.TicketRef, "")
	if err != nil {
		return nil, err
	}
	msgs, total, err := s.Datastore.GetTicketFirstMessages(ctx, ticket.TicketId, firstMessagesCount, false)
	if err != nil {
		return nil, err
	}
	return &proto.GetTicketResponse{
		Ticket:            ticket,
		FirstMessages:     msgs,
		TotalMessageCount: total,
	}, nil
}

func (s *TicketsService) ListTicketMessages(ctx context.Context, req *proto.ListTicketMessagesRequest) (*proto.ListTicketMessagesResponse, error) {
	if err := RequireScope(ctx, "read:tickets"); err != nil {
		return nil, err
	}
	msgs, next, more, err := s.Datastore.ListTicketMessages(ctx, req.TicketId, req.AfterPosition, req.PerPage, req.IncludeHidden)
	if err != nil {
		return nil, err
	}
	return &proto.ListTicketMessagesResponse{
		Messages:   msgs,
		NextCursor: next,
		HasMore:    more,
	}, nil
}
