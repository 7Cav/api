package grpc

import (
	"context"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/xenforo"
)

// fakeDatastore is the shared in-process Datastore stub used by both
// tickets_test.go and milpacs_test.go. Each handler-under-test gets the
// matching function field populated; everything else either panics (methods
// this PR does not exercise) or nil-derefs on call (unset configurable
// fields) so an accidentally-untouched test fails loudly with a clear stack
// rather than a silent zero-value response.
type fakeDatastore struct {
	// Tickets — configurable function fields used by tickets_test.go.
	listTickets func(*datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error)
	getTicket   func(uint32) (*proto.Ticket, error)
	getByRef    func(string) (*proto.Ticket, error)
	firstMsgs   func(uint32, int) ([]*proto.Message, uint32, error)
	listMsgs    func(uint32, string, uint32, bool) ([]*proto.Message, string, bool, error)
	listCats    func() ([]*proto.Category, error)

	// Milpacs — configurable function fields. Unset fields nil-deref on call;
	// same diagnostic level as tickets fields.
	findProfilesById           func(...uint64) ([]*proto.Profile, error)
	findProfilesByUsername     func(string) ([]*proto.Profile, error)
	findProfileByKeycloakID    func(string) (*proto.Profile, error)
	findProfileByDiscordID     func(string) (*proto.Profile, error)
	findProfileByGamertag      func(string) (*proto.Profile, error)
	findRosterByType           func(proto.RosterType) (*proto.Roster, error)
	findLiteRosterByType       func(proto.RosterType) (*proto.LiteRoster, error)
	findS1UniformsRosterByType func(proto.RosterType) (*proto.S1UniformsRoster, error)
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

func (f *fakeDatastore) FindProfilesById(ids ...uint64) ([]*proto.Profile, error) {
	return f.findProfilesById(ids...)
}
func (f *fakeDatastore) FindProfilesByUsername(u string) ([]*proto.Profile, error) {
	return f.findProfilesByUsername(u)
}
func (f *fakeDatastore) FindRosterByType(t proto.RosterType) (*proto.Roster, error) {
	return f.findRosterByType(t)
}
func (f *fakeDatastore) FindLiteRosterByType(t proto.RosterType) (*proto.LiteRoster, error) {
	return f.findLiteRosterByType(t)
}
func (f *fakeDatastore) FindProfileByKeycloakID(k string) (*proto.Profile, error) {
	return f.findProfileByKeycloakID(k)
}
func (f *fakeDatastore) FindProfileByDiscordID(d string) (*proto.Profile, error) {
	return f.findProfileByDiscordID(d)
}
func (f *fakeDatastore) FindS1UniformsRosterByType(t proto.RosterType) (*proto.S1UniformsRoster, error) {
	return f.findS1UniformsRosterByType(t)
}
func (f *fakeDatastore) FindProfileByGamertag(g string) (*proto.Profile, error) {
	return f.findProfileByGamertag(g)
}

// Milpacs methods this PR does not exercise — stay panicking.
func (f *fakeDatastore) FindProfilesByPosition(string) (*proto.LiteRoster, error)         { panic("unused") }
func (f *fakeDatastore) FindAllRanks() ([]*proto.RankExpanded, error)                     { panic("unused") }
func (f *fakeDatastore) FindAllPositionGroups() ([]*proto.PositionGroup, error)           { panic("unused") }
func (f *fakeDatastore) FindAwol() ([]*proto.Awol, error)                                 { panic("unused") }
func (f *fakeDatastore) GetTableUpdates() ([]xenforo.TableInfo, error)                    { panic("unused") }
func (f *fakeDatastore) ValidateApiKey(string) (*datastores.ApiKeyResult, error)          { panic("unused") }

// makeKeyCtx builds a context carrying an ApiKeyResult with the given scope
// set. The withTicketsKey / withMilpacsKey wrappers stay as named entry
// points so a test reader sees which handler family the scope applies to.
func makeKeyCtx(scopes ...string) context.Context {
	m := map[string]struct{}{}
	for _, s := range scopes {
		m[s] = struct{}{}
	}
	return ContextWithKey(context.Background(), &datastores.ApiKeyResult{Scopes: m})
}

// withTicketsKey builds a context carrying an ApiKeyResult with the given
// scope set. Mirrors the auth middleware's behavior in production.
func withTicketsKey(scopes ...string) context.Context { return makeKeyCtx(scopes...) }

// withMilpacsKey is the milpacs-handler equivalent of withTicketsKey.
// Pass "read" to satisfy RequireScope; pass nothing for permission-denied paths.
func withMilpacsKey(scopes ...string) context.Context { return makeKeyCtx(scopes...) }
