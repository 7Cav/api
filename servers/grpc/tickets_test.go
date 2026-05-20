package grpc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"gorm.io/gorm"
)

func TestListTickets_RequiresScope(t *testing.T) {
	svc := &TicketsService{Datastore: &fakeDatastore{}, ReferenceCache: &referencecache.Cache{}}
	ctx := withTicketsKey() // no scopes
	_, err := svc.ListTickets(ctx, &proto.ListTicketsRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestListTickets_HappyPath(t *testing.T) {
	var got *datastores.ListTicketsFilter
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listTickets: func(f *datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
				got = f
				return []*proto.Ticket{{TicketId: 1}}, "next123", true, nil
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	ctx := withTicketsKey("read:tickets")
	req := &proto.ListTicketsRequest{
		CategoryId:           []uint32{5},
		ExcludeSubcategories: true,
		TicketState:          []string{"open"},
		StatusId:             []uint32{1, 3},
		PrefixId:             []uint32{2},
		AssignedUserId:       []uint32{100},
		StarterUserId:        []uint32{200},
		ModifiedSince:        1736000000,
		IncludeHidden:        true,
		PerPage:              25,
		AfterCursor:          "cursor-abc",
	}
	resp, err := svc.ListTickets(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, got)

	wantFilter := &datastores.ListTicketsFilter{
		CategoryIDs:          []uint32{5},
		ExcludeSubcategories: true,
		TicketStates:         []string{"open"},
		StatusIDs:            []uint32{1, 3},
		PrefixIDs:            []uint32{2},
		AssignedUserIDs:      []uint32{100},
		StarterUserIDs:       []uint32{200},
		ModifiedSince:        1736000000,
		IncludeHidden:        true,
		PerPage:              25,
		AfterCursor:          "cursor-abc",
	}
	assert.Equal(t, wantFilter, got)

	require.Len(t, resp.Tickets, 1)
	assert.Equal(t, "next123", resp.NextCursor)
	assert.True(t, resp.HasMore)
}

func TestGetTicket_NotFoundReturns404Code(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			getTicket: func(id uint32) (*proto.Ticket, error) {
				return nil, gorm.ErrRecordNotFound
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	_, err := svc.GetTicket(withTicketsKey("read:tickets"), &proto.GetTicketRequest{TicketId: 99999})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestListTickets_DatastoreError(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listTickets: func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
				return nil, "", false, errors.New("boom")
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	_, err := svc.ListTickets(withTicketsKey("read:tickets"), &proto.ListTicketsRequest{})
	require.Error(t, err)
}

func TestGetTicket_PopulatesFirstMessages(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			getTicket: func(id uint32) (*proto.Ticket, error) {
				assert.Equal(t, uint32(7499), id)
				return &proto.Ticket{TicketId: 7499, TicketRef: "MF1UI9HE"}, nil
			},
			firstMsgs: func(id uint32, n int) ([]*proto.Message, uint32, error) {
				assert.Equal(t, uint32(7499), id)
				assert.Equal(t, 10, n)
				return []*proto.Message{{MessageId: 1}, {MessageId: 2}}, 15, nil
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	resp, err := svc.GetTicket(withTicketsKey("read:tickets"), &proto.GetTicketRequest{TicketId: 7499})
	require.NoError(t, err)
	assert.Equal(t, "MF1UI9HE", resp.Ticket.TicketRef)
	assert.Len(t, resp.FirstMessages, 2)
	assert.Equal(t, uint32(15), resp.TotalMessageCount)
}

func TestGetTicket_RequiresScope(t *testing.T) {
	svc := &TicketsService{Datastore: &fakeDatastore{}, ReferenceCache: &referencecache.Cache{}}
	_, err := svc.GetTicket(withTicketsKey(), &proto.GetTicketRequest{TicketId: 1})
	require.Error(t, err)
}

func TestGetTicketByRef_HappyPath(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			getByRef: func(ref string) (*proto.Ticket, error) {
				assert.Equal(t, "MF1UI9HE", ref)
				return &proto.Ticket{TicketId: 1}, nil
			},
			firstMsgs: func(uint32, int) ([]*proto.Message, uint32, error) { return nil, 0, nil },
		},
		ReferenceCache: &referencecache.Cache{},
	}
	resp, err := svc.GetTicketByRef(withTicketsKey("read:tickets"), &proto.GetTicketByRefRequest{TicketRef: "MF1UI9HE"})
	require.NoError(t, err)
	assert.Equal(t, uint32(1), resp.Ticket.TicketId)
}

func TestListTicketMessages_HappyPath(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listMsgs: func(id uint32, after string, per uint32, hidden bool) ([]*proto.Message, string, bool, error) {
				assert.Equal(t, uint32(7499), id)
				assert.Equal(t, "Y3Vyc29yOjEw", after, "handler must pass cursor through unchanged")
				assert.Equal(t, uint32(50), per)
				assert.False(t, hidden)
				return []*proto.Message{{MessageId: 11}}, "Y3Vyc29yOjEx", true, nil
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	resp, err := svc.ListTicketMessages(withTicketsKey("read:tickets"), &proto.ListTicketMessagesRequest{
		TicketId: 7499, AfterCursor: "Y3Vyc29yOjEw", PerPage: 50,
	})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 1)
	assert.Equal(t, "Y3Vyc29yOjEx", resp.NextCursor)
	assert.True(t, resp.HasMore)
}

func TestListCategories_HappyPath(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listCats: func() ([]*proto.Category, error) {
				return []*proto.Category{{CategoryId: 5, Title: "S1"}}, nil
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	resp, err := svc.ListCategories(withTicketsKey("read:tickets"), &emptypb.Empty{})
	require.NoError(t, err)
	require.Len(t, resp.Categories, 1)
	assert.Equal(t, "S1", resp.Categories[0].Title)
}

func TestListTickets_InvalidCursorMapsTo400(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listTickets: func(_ *datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
				// Simulate datastore returning a wrapped ErrInvalidCursor.
				return nil, "", false, fmt.Errorf("decode: %w", datastores.ErrInvalidCursor)
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	_, err := svc.ListTickets(withTicketsKey("read:tickets"), &proto.ListTicketsRequest{
		AfterCursor: "garbage",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestListTicketMessages_InvalidCursorMapsTo400(t *testing.T) {
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listMsgs: func(_ uint32, _ string, _ uint32, _ bool) ([]*proto.Message, string, bool, error) {
				return nil, "", false, fmt.Errorf("decode: %w", datastores.ErrInvalidCursor)
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	_, err := svc.ListTicketMessages(withTicketsKey("read:tickets"), &proto.ListTicketMessagesRequest{
		TicketId:    7499,
		AfterCursor: "garbage",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
