package grpc

import (
	"bytes"
	"context"
	"net"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// captureInfo redirects the package Info logger to a buffer for the duration
// of the test and restores it on cleanup.
func captureInfo(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := Info.Writer()
	Info.SetOutput(buf)
	t.Cleanup(func() { Info.SetOutput(prev) })
	return buf
}

func TestKeyFromContext_NilWhenAbsent(t *testing.T) {
	got := KeyFromContext(context.Background())
	assert.Nil(t, got)
}

func TestKeyFromContext_RoundTrip(t *testing.T) {
	key := &datastores.ApiKeyResult{
		KeyId:  1,
		UserId: 2,
		Scopes: map[string]struct{}{"read": {}},
	}
	ctx := ContextWithKey(context.Background(), key)
	got := KeyFromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, key, got)
}

func TestRequireScope_AllowsWhenPresent(t *testing.T) {
	key := &datastores.ApiKeyResult{
		Scopes: map[string]struct{}{"read:tickets": {}},
	}
	ctx := ContextWithKey(context.Background(), key)
	err := RequireScope(ctx, "read:tickets")
	assert.NoError(t, err)
}

func TestRequireScope_RejectsWhenMissing(t *testing.T) {
	key := &datastores.ApiKeyResult{
		Scopes: map[string]struct{}{"read": {}},
	}
	ctx := ContextWithKey(context.Background(), key)
	err := RequireScope(ctx, "read:tickets")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

func TestRequireScope_RejectsWhenKeyAbsent(t *testing.T) {
	err := RequireScope(context.Background(), "read")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
}

// buildAuthCtx returns an incoming-gRPC context populated with a bearer token
// and a peer address — the two things the interceptor reads off the wire.
func buildAuthCtx(token, peerIP string) context.Context {
	ctx := metadata.NewIncomingContext(
		context.Background(),
		metadata.Pairs("authorization", "Bearer "+token),
	)
	return peer.NewContext(ctx, &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(peerIP), Port: 4242},
	})
}

func TestAuthInterceptor_LogsRequestOnSuccess(t *testing.T) {
	ds := &fakeDatastore{
		validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
			return &datastores.ApiKeyResult{KeyId: 17}, nil
		},
	}
	buf := captureInfo(t)
	interceptor := NewAuthInterceptor(ds)
	ctx := buildAuthCtx("cav7_abc", "10.0.0.5")
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.MilpacService/GetProfile"}
	resp, err := interceptor(ctx, "req", info, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp)

	logged := buf.String()
	assert.Contains(t, logged, "[REQ] transport=grpc")
	assert.Contains(t, logged, "method=/proto.MilpacService/GetProfile")
	assert.Contains(t, logged, "peer=10.0.0.5:4242")
	assert.Contains(t, logged, "key_id=17")
}

// rawAuthCtx sets the authorization metadata verbatim (no implicit "Bearer "
// prefix), so scheme-problem cases can be exercised.
func rawAuthCtx(authHeader, peerIP string) context.Context {
	var ctx context.Context
	if authHeader == "" {
		ctx = context.Background()
	} else {
		ctx = metadata.NewIncomingContext(
			context.Background(),
			metadata.Pairs("authorization", authHeader),
		)
	}
	return peer.NewContext(ctx, &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(peerIP), Port: 4242},
	})
}

func runInterceptor(t *testing.T, ds *fakeDatastore, ctx context.Context) (any, error, bool) {
	t.Helper()
	interceptor := NewAuthInterceptor(ds)
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.MilpacService/GetProfile"}
	handlerCalled := false
	resp, err := interceptor(ctx, "req", info, func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		return "ok", nil
	})
	return resp, err, handlerCalled
}

func TestAuthInterceptor_NoAuthHeader_NamesBearerScheme(t *testing.T) {
	ds := &fakeDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		t.Fatal("ValidateApiKey must not be called when no authorization metadata is present")
		return nil, nil
	}}
	_, err, called := runInterceptor(t, ds, rawAuthCtx("", "10.0.0.5"))

	require.Error(t, err)
	assert.False(t, called)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Contains(t, st.Message(), "Bearer")
	assert.Contains(t, st.Message(), "Authorization")
}

func TestAuthInterceptor_RawKeyNoBearerPrefix_NamesBearerScheme(t *testing.T) {
	ds := &fakeDatastore{validateApiKey: func(string) (*datastores.ApiKeyResult, error) {
		t.Fatal("ValidateApiKey must not be called when the Bearer scheme is absent")
		return nil, nil
	}}
	_, err, called := runInterceptor(t, ds, rawAuthCtx("cav7_rawkeynoprefix", "10.0.0.5"))

	require.Error(t, err)
	assert.False(t, called)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Contains(t, st.Message(), "Bearer")
	assert.Contains(t, st.Message(), "Authorization")
}

func TestAuthInterceptor_BadKey_GenericNoLeak(t *testing.T) {
	ds := &fakeDatastore{validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
		assert.Equal(t, "cav7_badkey", token)
		return nil, nil
	}}
	_, err, called := runInterceptor(t, ds, buildAuthCtx("cav7_badkey", "10.0.0.5"))

	require.Error(t, err)
	assert.False(t, called)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	// Generic — must not name the Bearer scheme (that's reserved for scheme errors).
	assert.NotContains(t, st.Message(), "Bearer")
}

func TestAuthInterceptor_LogsRequestOnAuthFailure(t *testing.T) {
	ds := &fakeDatastore{
		validateApiKey: func(token string) (*datastores.ApiKeyResult, error) {
			return nil, status.Errorf(codes.Unauthenticated, "bad")
		},
	}
	buf := captureInfo(t)
	interceptor := NewAuthInterceptor(ds)
	ctx := buildAuthCtx("cav7_bogus", "10.0.0.5")
	info := &grpc.UnaryServerInfo{FullMethod: "/proto.MilpacService/GetProfile"}
	handlerCalled := false
	_, err := interceptor(ctx, "req", info, func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		return nil, nil
	})
	require.Error(t, err)
	assert.False(t, handlerCalled, "handler must not run on auth failure")

	logged := buf.String()
	assert.Contains(t, logged, "[REQ] transport=grpc")
	assert.Contains(t, logged, "method=/proto.MilpacService/GetProfile")
	assert.Contains(t, logged, "peer=10.0.0.5:4242")
	assert.Contains(t, logged, "key_id=none")
}
