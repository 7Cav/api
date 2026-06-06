package contract

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/7cav/api/servers/gateway"
	grpcServices "github.com/7cav/api/servers/grpc"
	"google.golang.org/grpc"
)

// Compile-time guarantee the recording fake implements the full interface.
var _ datastores.Datastore = recordingDatastore{}

var (
	stackHandler http.Handler
	stackErr     error
	// grpcServeErr receives the srv.Serve result if Serve ever returns
	// (buffered 1, written exactly once). Peeked via pendingServeErr so a
	// startup or mid-run server death is reported instead of discarded.
	grpcServeErr chan error
)

// TestMain mounts the CURRENT production stack in-process exactly once:
//
//	httptest request → gateway.Service.Server().Handler (real /api routing,
//	real auth middleware, real sentry/compression chain) → real gRPC
//	client conn over TCP → real grpc.Server with the production interceptor
//	chain (auth outer, sentry inner; mirrors servers.apiUnaryInterceptors)
//	→ MilpacsService + TicketsService handlers → recordingDatastore.
//
// Differences from production, all behavior-neutral by construction:
//   - the datastore is the seeded fake (no MySQL),
//   - SENTRY_DSN is unset (TestMain enforces it), so both sentry layers are
//     pass-throughs,
//   - the TicketsService reference cache is nil — the fake never touches it.
//
// There is no Redis anywhere: the response cache was deleted at Phase 2
// de-cache (#123 middleware, #124 package + Redis), so the stack mounts and
// serves with no cache backend at all — exactly like production.
func TestMain(m *testing.M) {
	quietProductionLoggers()
	os.Unsetenv("SENTRY_DSN") // make the pass-through claim above true by construction
	stackHandler, stackErr = mountCurrentStack()
	os.Exit(m.Run())
}

func currentStack(t *testing.T) http.Handler {
	t.Helper()
	if stackErr == nil {
		if err := pendingServeErr(); err != nil {
			stackErr = fmt.Errorf("grpc server exited mid-run: %w", err)
		}
	}
	if stackErr != nil {
		t.Fatalf("mounting current stack: %v", stackErr)
	}
	return stackHandler
}

// pendingServeErr non-blockingly peeks at grpcServeErr and puts any value
// back, so the one Serve result stays observable by every later caller.
func pendingServeErr() error {
	select {
	case err := <-grpcServeErr:
		grpcServeErr <- err
		return err
	default:
		return nil
	}
}

func mountCurrentStack() (http.Handler, error) {
	ds := recordingDatastore{}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
	}

	// Production interceptor chain order (servers.apiUnaryInterceptors):
	// auth outer, sentry inner.
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(
		grpcServices.NewAuthInterceptor(ds),
		grpcServices.NewSentryInterceptor(),
	))
	proto.RegisterMilpacServiceServer(srv, &grpcServices.MilpacsService{Datastore: ds})
	proto.RegisterTicketsServiceServer(srv, &grpcServices.TicketsService{Datastore: ds})
	grpcServeErr = make(chan error, 1)
	go func() {
		grpcServeErr <- srv.Serve(lis)
	}()

	svc := gateway.Service{
		Address:   lis.Addr().String(),
		Datastore: ds,
	}
	// gateway.Service.Server() dials with grpc.WithBlock() on
	// context.Background() — an unbounded blocking dial. If the gRPC server
	// never came up, that dial would hang TestMain forever with zero output,
	// so run the mount in a goroutine and watchdog it: finish, see the Serve
	// error, or time out — never hang silently.
	const mountTimeout = 10 * time.Second
	mounted := make(chan *http.Server, 1)
	go func() {
		mounted <- svc.Server()
	}()
	var httpSrv *http.Server
	select {
	case httpSrv = <-mounted:
	case err := <-grpcServeErr:
		grpcServeErr <- err // keep it observable for pendingServeErr
		return nil, fmt.Errorf("grpc server exited before gateway mount; serve error: %v", err)
	case <-time.After(mountTimeout):
		return nil, fmt.Errorf("grpc dial did not become ready in %s; serve error: %v", mountTimeout, pendingServeErr())
	}
	if httpSrv == nil {
		return nil, fmt.Errorf("gateway.Service.Server() returned nil (dial or registration failure)")
	}
	return httpSrv.Handler, nil
}

// quietProductionLoggers silences the chatty Info/Warn loggers of the stack
// under test so corpus runs stay readable. Errors stay visible.
//
// Do NOT silence gateway.Error (or the grpc Error loggers): gateway.Service.
// Server() reports dial/registration failure only via its Error log plus a
// nil return, so muting Error would reduce a mount failure to a bare
// "returned nil" with no cause.
func quietProductionLoggers() {
	for _, l := range []interface{ SetOutput(io.Writer) }{
		gateway.Info, gateway.Warn,
		grpcServices.Info, grpcServices.Warn,
		datastores.Info, datastores.Warn,
		rest.Info, rest.Warn, // the gateway delegates auth/gzip to rest (#125)
	} {
		l.SetOutput(io.Discard)
	}
}
