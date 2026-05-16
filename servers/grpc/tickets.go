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
