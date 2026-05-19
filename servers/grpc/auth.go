package grpc

import (
	"context"

	"github.com/7cav/api/datastores"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// keyContextKey is the private type used to attach *ApiKeyResult to a request
// ctx. Private so external packages can't accidentally clobber it.
type keyContextKey struct{}

// ContextWithKey returns a new context carrying the given API key result.
func ContextWithKey(ctx context.Context, key *datastores.ApiKeyResult) context.Context {
	return context.WithValue(ctx, keyContextKey{}, key)
}

// KeyFromContext returns the *ApiKeyResult attached to ctx, or nil if none.
func KeyFromContext(ctx context.Context) *datastores.ApiKeyResult {
	v, _ := ctx.Value(keyContextKey{}).(*datastores.ApiKeyResult)
	return v
}

// RequireScope returns codes.PermissionDenied if the key on ctx lacks the named
// scope. Call at the top of every RPC that requires authorization.
func RequireScope(ctx context.Context, scope string) error {
	key := KeyFromContext(ctx)
	if !key.HasScope(scope) {
		return status.Errorf(codes.PermissionDenied, "scope required: %s", scope)
	}
	return nil
}
