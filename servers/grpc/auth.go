package grpc

import (
	"context"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/rest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The key-on-context helpers moved to the rest package (the Phase 3 stack,
// #125) — their permanent home once this package is deleted at Phase 4. The
// delegating aliases below keep this package's API stable and, more
// importantly, keep BOTH stacks attaching/reading the same context key, so
// the gateway's Sentry key-id tagging works regardless of which middleware
// authenticated the request.

// ContextWithKey returns a new context carrying the given API key result.
func ContextWithKey(ctx context.Context, key *datastores.ApiKeyResult) context.Context {
	return rest.ContextWithKey(ctx, key)
}

// KeyFromContext returns the *ApiKeyResult attached to ctx, or nil if none.
func KeyFromContext(ctx context.Context) *datastores.ApiKeyResult {
	return rest.KeyFromContext(ctx)
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
