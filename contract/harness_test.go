package contract

import (
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/referencecache"
	"github.com/7cav/api/rest"
)

// Compile-time guarantee the recording fake implements the full interface.
var _ datastores.Datastore = recordingDatastore{}

var (
	stackHandler http.Handler
	stackErr     error
)

// TestMain mounts the production stack in-process exactly once:
//
//	httptest request → rest.New(ds, rc).ServeHTTP (real /api routing, real
//	auth middleware, real sentry/gzip chain) → milpacs/tickets handlers →
//	recordingDatastore.
//
// Since the single-listener cutover (#134) there is one stack: the stdlib
// net/http rest package. The gRPC server and the grpc-gateway translation
// layer are gone, so the harness mounts rest.New directly instead of dialing a
// gRPC server behind a gateway.
//
// Differences from production, all behavior-neutral by construction:
//   - the datastore is the seeded fake (no MySQL),
//   - SENTRY_DSN is unset (TestMain enforces it), so the sentry layer is a
//     pass-through,
//   - the TicketReferenceCache is the no-op stub below — the fake bakes its
//     reference-name resolution into the seeds and never consults the cache.
//
// There is no Redis anywhere: the response cache was deleted at Phase 2
// de-cache (#123 middleware, #124 package + Redis), so the stack serves with
// no cache backend at all, exactly like production.
func TestMain(m *testing.M) {
	quietProductionLoggers()
	os.Unsetenv("SENTRY_DSN") // make the pass-through claim above true by construction
	stackHandler, stackErr = mountCurrentStack()
	os.Exit(m.Run())
}

func currentStack(t *testing.T) http.Handler {
	t.Helper()
	if stackErr != nil {
		t.Fatalf("mounting current stack: %v", stackErr)
	}
	return stackHandler
}

func mountCurrentStack() (http.Handler, error) {
	ds := recordingDatastore{}
	return rest.New(ds, stubReferenceCache{}), nil
}

// stubReferenceCache is the no-op TicketReferenceCache the corpus mounts:
// rest.New refuses nil, and recordingDatastore resolves all reference names in
// its seeds (it never calls back into the cache), so every method can return
// the zero value.
type stubReferenceCache struct{}

func (stubReferenceCache) StatusName(uint32) string                       { return "" }
func (stubReferenceCache) PriorityName(uint32) string                     { return "" }
func (stubReferenceCache) PrefixName(uint32) string                       { return "" }
func (stubReferenceCache) Category(uint32) *referencecache.CategoryRecord { return nil }
func (stubReferenceCache) CategoryAncestors(uint32) []uint32              { return nil }
func (stubReferenceCache) CategoryTree() []*referencecache.CategoryRecord { return nil }
func (stubReferenceCache) ExpandSubtree(ids []uint32) []uint32            { return ids }

// quietProductionLoggers silences the chatty Info/Warn loggers of the stack
// under test so corpus runs stay readable. Errors stay visible.
func quietProductionLoggers() {
	for _, l := range []interface{ SetOutput(io.Writer) }{
		datastores.Info, datastores.Warn,
		rest.Info, rest.Warn,
	} {
		l.SetOutput(io.Discard)
	}
}
