package grpc

import (
	"context"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
