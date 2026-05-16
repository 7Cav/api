package grpc

import (
	"context"
	"errors"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
	"github.com/7cav/api/xenforo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// fakeDatastore implements datastores.Datastore with just the methods we need
// for tickets-handler tests stubbed. Other methods panic so the test surface
// stays explicit.
type fakeDatastore struct {
	listTickets func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error)
	getTicket   func(uint32) (*proto.Ticket, error)
	getByRef    func(string) (*proto.Ticket, error)
	firstMsgs   func(uint32, int) ([]*proto.Message, uint32, error)
	listMsgs    func(uint32, uint32, uint32, bool) ([]*proto.Message, uint32, bool, error)
	listCats    func() ([]*proto.Category, error)
}

func (f *fakeDatastore) ListTickets(_ context.Context, _ datastores.TicketReferenceCache, fi *datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
	return f.listTickets(fi)
}
func (f *fakeDatastore) GetTicket(_ context.Context, _ datastores.TicketReferenceCache, id uint32, _ string) (*proto.Ticket, error) {
	return f.getTicket(id)
}
func (f *fakeDatastore) GetTicketByRef(_ context.Context, _ datastores.TicketReferenceCache, ref string, _ string) (*proto.Ticket, error) {
	return f.getByRef(ref)
}
func (f *fakeDatastore) GetTicketFirstMessages(_ context.Context, id uint32, n int, _ bool) ([]*proto.Message, uint32, error) {
	return f.firstMsgs(id, n)
}
func (f *fakeDatastore) ListTicketMessages(_ context.Context, id, after, per uint32, hidden bool) ([]*proto.Message, uint32, bool, error) {
	return f.listMsgs(id, after, per, hidden)
}
func (f *fakeDatastore) ListCategories(_ context.Context, _ datastores.TicketReferenceCache) ([]*proto.Category, error) {
	return f.listCats()
}

// Milpacs methods omitted; tickets tests don't use them.
func (f *fakeDatastore) FindProfilesById(_ ...uint64) ([]*proto.Profile, error)           { panic("unused") }
func (f *fakeDatastore) FindProfilesByUsername(string) ([]*proto.Profile, error)          { panic("unused") }
func (f *fakeDatastore) FindRosterByType(proto.RosterType) (*proto.Roster, error)         { panic("unused") }
func (f *fakeDatastore) FindLiteRosterByType(proto.RosterType) (*proto.LiteRoster, error) { panic("unused") }
func (f *fakeDatastore) FindProfileByKeycloakID(string) (*proto.Profile, error)           { panic("unused") }
func (f *fakeDatastore) FindProfileByDiscordID(string) (*proto.Profile, error)            { panic("unused") }
func (f *fakeDatastore) FindProfilesByPosition(string) (*proto.LiteRoster, error)         { panic("unused") }
func (f *fakeDatastore) FindS1UniformsRosterByType(proto.RosterType) (*proto.S1UniformsRoster, error) {
	panic("unused")
}
func (f *fakeDatastore) FindAllRanks() ([]*proto.RankExpanded, error)             { panic("unused") }
func (f *fakeDatastore) FindAllPositionGroups() ([]*proto.PositionGroup, error)   { panic("unused") }
func (f *fakeDatastore) FindAwol() ([]*proto.Awol, error)                         { panic("unused") }
func (f *fakeDatastore) GetTableUpdates() ([]xenforo.TableInfo, error)            { panic("unused") }
func (f *fakeDatastore) FindProfileByGamertag(string) (*proto.Profile, error)     { panic("unused") }
func (f *fakeDatastore) ValidateApiKey(string) (*datastores.ApiKeyResult, error)  { panic("unused") }

func withTicketsKey(scopes ...string) context.Context {
	m := map[string]struct{}{}
	for _, s := range scopes {
		m[s] = struct{}{}
	}
	return ContextWithKey(context.Background(), &datastores.ApiKeyResult{Scopes: m})
}

func TestListTickets_RequiresScope(t *testing.T) {
	svc := &TicketsService{Datastore: &fakeDatastore{}, ReferenceCache: &referencecache.Cache{}}
	ctx := withTicketsKey() // no scopes
	_, err := svc.ListTickets(ctx, &proto.ListTicketsRequest{})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestListTickets_HappyPath(t *testing.T) {
	called := false
	svc := &TicketsService{
		Datastore: &fakeDatastore{
			listTickets: func(f *datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
				called = true
				assert.Equal(t, []uint32{5}, f.CategoryIDs)
				assert.Equal(t, []string{"open"}, f.TicketStates)
				return []*proto.Ticket{{TicketId: 1}}, "next123", true, nil
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	ctx := withTicketsKey("read:tickets")
	resp, err := svc.ListTickets(ctx, &proto.ListTicketsRequest{
		CategoryId:  []uint32{5},
		TicketState: []string{"open"},
	})
	require.NoError(t, err)
	assert.True(t, called)
	require.Len(t, resp.Tickets, 1)
	assert.Equal(t, "next123", resp.NextCursor)
	assert.True(t, resp.HasMore)
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
			listMsgs: func(id, after, per uint32, hidden bool) ([]*proto.Message, uint32, bool, error) {
				assert.Equal(t, uint32(7499), id)
				assert.Equal(t, uint32(10), after)
				assert.Equal(t, uint32(50), per)
				assert.False(t, hidden)
				return []*proto.Message{{MessageId: 11}}, 11, true, nil
			},
		},
		ReferenceCache: &referencecache.Cache{},
	}
	resp, err := svc.ListTicketMessages(withTicketsKey("read:tickets"), &proto.ListTicketMessagesRequest{
		TicketId: 7499, AfterPosition: 10, PerPage: 50,
	})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 1)
	assert.Equal(t, uint32(11), resp.NextCursor)
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
