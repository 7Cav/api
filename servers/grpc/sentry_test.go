package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// captureTransport is an in-memory sentry.Transport recording every event the
// client would have sent over the wire — the observable seam for these tests.
// flushFails makes Flush report timeout; flushed records that a synchronous
// flush happened at all (pinning the sync-flush-on-panic contract).
type captureTransport struct {
	mu         sync.Mutex
	events     []*sentry.Event
	flushed    bool
	flushFails bool
}

func (t *captureTransport) Configure(sentry.ClientOptions) {}

func (t *captureTransport) Flush(time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushed = true
	return !t.flushFails
}

func (t *captureTransport) FlushWithContext(context.Context) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushed = true
	return !t.flushFails
}

func (t *captureTransport) Close() {}

func (t *captureTransport) Flushed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.flushed
}

func (t *captureTransport) SendEvent(event *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

func (t *captureTransport) Events() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

// testRelease stands in for the build-time version injection so tests can
// prove events carry the release tag.
const testRelease = "test-release-1.2.3"

// bindCaptureClient binds a capture-only Sentry client to the global hub for
// the duration of the test, restoring the unbound (disabled) state afterwards.
// AttachStacktrace mirrors production (setupSentry) so string-panic events
// carry frames here exactly as they do live.
func bindCaptureClient(t *testing.T) *captureTransport {
	t.Helper()
	transport := &captureTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport, Release: testRelease, AttachStacktrace: true})
	require.NoError(t, err)
	sentry.CurrentHub().BindClient(client)
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	return transport
}

func unaryInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

func TestSentryInterceptor_InternalError_CapturedWithRouteAndKeyID(t *testing.T) {
	transport := bindCaptureClient(t)
	interceptor := NewSentryInterceptor()

	ctx := ContextWithKey(context.Background(), &datastores.ApiKeyResult{KeyId: 42})
	handlerErr := status.Error(codes.Internal, "boom")
	resp, err := interceptor(ctx, nil, unaryInfo("/proto.MilpacService/GetProfile"),
		func(ctx context.Context, req any) (any, error) {
			return nil, handlerErr
		})

	assert.Nil(t, resp)
	assert.Equal(t, handlerErr, err, "interceptor must pass the handler error through unchanged")

	events := transport.Events()
	require.Len(t, events, 1, "an Internal-class error must produce exactly one event")
	event := events[0]
	assert.Equal(t, "/proto.MilpacService/GetProfile", event.Tags["route"])
	assert.Equal(t, "42", event.Tags["key_id"])
	assert.Equal(t, "grpc", event.Tags["transport"])
	assert.Equal(t, "Internal", event.Tags["grpc_code"])
}

func TestSentryInterceptor_ClientClassOutcomes_NoEvents(t *testing.T) {
	transport := bindCaptureClient(t)
	interceptor := NewSentryInterceptor()
	ctx := context.Background()
	info := unaryInfo("/proto.MilpacService/GetProfile")

	cases := []struct {
		name string
		err  error
	}{
		{"success", nil},
		{"not found", status.Error(codes.NotFound, "no such profile")},
		{"unauthenticated", status.Error(codes.Unauthenticated, "invalid api key")},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad cursor")},
		{"permission denied", status.Error(codes.PermissionDenied, "scope required")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := interceptor(ctx, nil, info,
				func(ctx context.Context, req any) (any, error) {
					if tc.err != nil {
						return nil, tc.err
					}
					return "ok", nil
				})
			assert.Equal(t, tc.err, err)
			if tc.err == nil {
				assert.Equal(t, "ok", resp)
			}
		})
	}

	assert.Empty(t, transport.Events(), "expected outcomes must not generate Sentry noise")
}

// TestSentryInterceptor_CapturedErrorClass pins exactly which gRPC outcomes
// are Internal-class (captured) versus expected behavior (silent). A plain
// non-status error surfaces as codes.Unknown — the most common real-world
// server bug shape — and must be captured.
func TestSentryInterceptor_CapturedErrorClass(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		captured bool
		wantCode string
	}{
		{"plain error becomes Unknown — captured", errors.New("kapow"), true, "Unknown"},
		{"unavailable — captured", status.Error(codes.Unavailable, "downstream gone"), true, "Unavailable"},
		{"deadline exceeded — captured", status.Error(codes.DeadlineExceeded, "too slow"), true, "DeadlineExceeded"},
		{"aborted — silent", status.Error(codes.Aborted, "tx conflict"), false, ""},
		{"resource exhausted — silent", status.Error(codes.ResourceExhausted, "rate limited"), false, ""},
		{"not found — silent", status.Error(codes.NotFound, "no such row"), false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := bindCaptureClient(t)
			interceptor := NewSentryInterceptor()

			_, err := interceptor(context.Background(), nil, unaryInfo("/proto.MilpacService/GetProfile"),
				func(ctx context.Context, req any) (any, error) {
					return nil, tc.err
				})

			assert.Equal(t, tc.err, err, "the handler error must pass through unchanged")
			events := transport.Events()
			if !tc.captured {
				assert.Empty(t, events, "expected outcomes must not generate Sentry noise")
				return
			}
			require.Len(t, events, 1, "an Internal-class outcome must produce exactly one event")
			assert.Equal(t, tc.wantCode, events[0].Tags["grpc_code"])
		})
	}
}

// TestSentryInterceptor_BehindAuthInterceptor_EventCarriesKeyID composes the
// real auth interceptor around the real Sentry interceptor — mirroring the
// grpc.ChainUnaryInterceptor order in server.go — and proves the key auth
// attaches to ctx is what feeds the key_id tag. No test-injected key.
func TestSentryInterceptor_BehindAuthInterceptor_EventCarriesKeyID(t *testing.T) {
	transport := bindCaptureClient(t)
	ds := &fakeDatastore{validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
		assert.Equal(t, "cav7_goodkey", token)
		return &datastores.ApiKeyResult{KeyId: 42}, nil
	}}
	authInterceptor := NewAuthInterceptor(ds)
	sentryInterceptor := NewSentryInterceptor()
	info := unaryInfo("/proto.MilpacService/GetProfile")

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer cav7_goodkey"))
	handlerErr := status.Error(codes.Internal, "boom")
	resp, err := authInterceptor(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
		return sentryInterceptor(ctx, req, info, func(ctx context.Context, req any) (any, error) {
			return nil, handlerErr
		})
	})

	assert.Nil(t, resp)
	assert.Equal(t, handlerErr, err)
	events := transport.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "42", events[0].Tags["key_id"],
		"the key_id must come from auth's ctx attachment, proving the chain wiring end-to-end")
	assert.Equal(t, "grpc", events[0].Tags["transport"])
}

func TestSentryInterceptor_HandlerPanic_CapturedThenRepanics(t *testing.T) {
	transport := bindCaptureClient(t)
	interceptor := NewSentryInterceptor()
	ctx := ContextWithKey(context.Background(), &datastores.ApiKeyResult{KeyId: 7})

	assert.PanicsWithValue(t, "kaboom", func() {
		_, _ = interceptor(ctx, nil, unaryInfo("/proto.TicketsService/ListTickets"),
			func(ctx context.Context, req any) (any, error) {
				panic("kaboom")
			})
	}, "panic must propagate after capture — Phase 0 observes, it does not change crash semantics")

	events := transport.Events()
	require.Len(t, events, 1, "a handler panic must produce exactly one event")
	event := events[0]
	assert.Equal(t, "/proto.TicketsService/ListTickets", event.Tags["route"])
	assert.Equal(t, "7", event.Tags["key_id"])
	assert.Equal(t, "grpc", event.Tags["transport"])
	assert.Equal(t, sentry.LevelFatal, event.Level)
	assert.Equal(t, testRelease, event.Release, "panic events must carry the release")
	assert.True(t, transport.Flushed(),
		"panic events must be flushed synchronously — the process is about to die and the async transport with it")
	require.NotEmpty(t, event.Threads, "a string panic must carry a stack (AttachStacktrace shapes it as a thread)")
	require.NotNil(t, event.Threads[0].Stacktrace)
	assert.NotEmpty(t, event.Threads[0].Stacktrace.Frames, "a string panic without frames is undebuggable")
}

func TestSentryInterceptor_PanicFlushTimeout_Warns(t *testing.T) {
	transport := bindCaptureClient(t)
	transport.flushFails = true

	var warnBuf bytes.Buffer
	Warn.SetOutput(&warnBuf)
	defer Warn.SetOutput(os.Stdout)

	interceptor := NewSentryInterceptor()
	assert.PanicsWithValue(t, "kaboom", func() {
		_, _ = interceptor(context.Background(), nil, unaryInfo("/proto.TicketsService/ListTickets"),
			func(ctx context.Context, req any) (any, error) {
				panic("kaboom")
			})
	}, "a failed flush must not block the re-panic")

	assert.Contains(t, warnBuf.String(), "panic event for /proto.TicketsService/ListTickets was likely dropped",
		"a dropped crash event must at least leave a trace in the process logs")
}

func TestSentryInterceptor_NoClient_PassThrough(t *testing.T) {
	// No client bound — the no-DSN state. Errors and panics must behave
	// exactly as they do today.
	require.Nil(t, sentry.CurrentHub().Client(), "test requires the disabled state")
	interceptor := NewSentryInterceptor()
	info := unaryInfo("/proto.MilpacService/GetProfile")

	handlerErr := status.Error(codes.Internal, "boom")
	resp, err := interceptor(context.Background(), nil, info,
		func(ctx context.Context, req any) (any, error) {
			return nil, handlerErr
		})
	assert.Nil(t, resp)
	assert.Equal(t, handlerErr, err)

	assert.PanicsWithValue(t, "kaboom", func() {
		_, _ = interceptor(context.Background(), nil, info,
			func(ctx context.Context, req any) (any, error) {
				panic("kaboom")
			})
	}, "panics must still propagate when Sentry is disabled")
}

func TestSentryInterceptor_BearerTokenNeverInPayload(t *testing.T) {
	transport := bindCaptureClient(t)
	interceptor := NewSentryInterceptor()

	// Simulate the real request shape: the bearer token sits in the incoming
	// gRPC metadata on ctx, exactly where the auth interceptor read it.
	const secret = "cav7_topsecrettokenvalue"
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"authorization", "Bearer "+secret,
	))
	ctx = ContextWithKey(ctx, &datastores.ApiKeyResult{KeyId: 42})

	_, _ = interceptor(ctx, nil, unaryInfo("/proto.MilpacService/GetProfile"),
		func(ctx context.Context, req any) (any, error) {
			return nil, status.Error(codes.Internal, "boom")
		})
	assert.PanicsWithValue(t, "kaboom", func() {
		_, _ = interceptor(ctx, nil, unaryInfo("/proto.MilpacService/GetProfile"),
			func(ctx context.Context, req any) (any, error) {
				panic("kaboom")
			})
	})

	events := transport.Events()
	require.Len(t, events, 2)
	for _, event := range events {
		payload, err := json.Marshal(event)
		require.NoError(t, err)
		assert.NotContains(t, string(payload), secret, "bearer material must never appear in any event payload")
		assert.Equal(t, "42", event.Tags["key_id"], "events carry the key id, not the key")
	}
}
