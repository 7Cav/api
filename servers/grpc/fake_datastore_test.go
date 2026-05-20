package grpc

import (
	"context"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/xenforo"
)

// fakeDatastore is the shared in-process Datastore stub used by both
// tickets_test.go and milpacs_test.go. Each handler-under-test gets the
// matching function field populated; milpacs methods panic until Task 2
// converts them to configurable function fields.
type fakeDatastore struct {
	// Tickets — configurable function fields used by tickets_test.go.
	listTickets func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error)
	getTicket   func(uint32) (*proto.Ticket, error)
	getByRef    func(string) (*proto.Ticket, error)
	firstMsgs   func(uint32, int) ([]*proto.Message, uint32, error)
	listMsgs    func(uint32, string, uint32, bool) ([]*proto.Message, string, bool, error)
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
func (f *fakeDatastore) ListTicketMessages(_ context.Context, id uint32, after string, per uint32, hidden bool) ([]*proto.Message, string, bool, error) {
	return f.listMsgs(id, after, per, hidden)
}
func (f *fakeDatastore) ListCategories(_ context.Context, _ datastores.TicketReferenceCache) ([]*proto.Category, error) {
	return f.listCats()
}

// Milpacs methods — left panicking in this task; converted to configurable
// function fields in Task 2.
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
func (f *fakeDatastore) FindAllRanks() ([]*proto.RankExpanded, error)            { panic("unused") }
func (f *fakeDatastore) FindAllPositionGroups() ([]*proto.PositionGroup, error)  { panic("unused") }
func (f *fakeDatastore) FindAwol() ([]*proto.Awol, error)                        { panic("unused") }
func (f *fakeDatastore) GetTableUpdates() ([]xenforo.TableInfo, error)           { panic("unused") }
func (f *fakeDatastore) FindProfileByGamertag(string) (*proto.Profile, error)    { panic("unused") }
func (f *fakeDatastore) ValidateApiKey(string) (*datastores.ApiKeyResult, error) { panic("unused") }

// withTicketsKey builds a context carrying an ApiKeyResult with the given
// scope set. Mirrors the auth middleware's behavior in production.
func withTicketsKey(scopes ...string) context.Context {
	m := map[string]struct{}{}
	for _, s := range scopes {
		m[s] = struct{}{}
	}
	return ContextWithKey(context.Background(), &datastores.ApiKeyResult{Scopes: m})
}
