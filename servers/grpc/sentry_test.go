package grpc

import (
	"context"
	"encoding/json"
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
type captureTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *captureTransport) Configure(sentry.ClientOptions)        {}
func (t *captureTransport) Flush(time.Duration) bool              { return true }
func (t *captureTransport) FlushWithContext(context.Context) bool { return true }
func (t *captureTransport) Close()                                {}

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

// bindCaptureClient binds a capture-only Sentry client to the global hub for
// the duration of the test, restoring the unbound (disabled) state afterwards.
func bindCaptureClient(t *testing.T) *captureTransport {
	t.Helper()
	transport := &captureTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: transport})
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
