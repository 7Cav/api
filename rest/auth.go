package rest

import (
	"context"
	"net/http"

	"github.com/7cav/api/datastores"
)

// The HTTP auth middleware below moved here verbatim from servers/gateway
// (its original seam) for the Phase 3 rewrite: the gateway delegates to this
// implementation until it is deleted, so the two stacks can never diverge.
// The two-tier 401 behavior is golden-pinned (#106): scheme errors name the
// expected header form, unknown keys get the generic line, both plain text,
// no WWW-Authenticate challenge.

// maxTokenLen is the maximum length of a raw API key we'll accept.
// cav7_ prefix (5) + 64 hex chars = 69; 128 gives generous headroom.
const maxTokenLen = 128

// errBearerScheme is the 401 body returned when the Authorization header is
// missing or doesn't carry a usable Bearer token (no/empty/oversized token).
// It names the expected format so callers who paste a raw key without the
// "Bearer " prefix get a self-explanatory error. The key-validation-failure
// branch stays the generic "Unauthorized" so it leaks nothing about whether a
// key exists, is expired, or lacks scopes.
const errBearerScheme = "Unauthorized: expected 'Authorization: Bearer <key>' header"

// AuthMiddleware authenticates every request against the upstream key tables
// (ADR 0004) and attaches the validated key to the request context. It runs
// BEFORE routing — an unknown path without credentials is a 401, not a 404
// (golden-pinned). Authorization (scope membership) is per-route: see
// requireScope.
//
// Exported because the legacy gateway chain reuses it until cutover deletes
// that stack.
func AuthMiddleware(ds datastores.Datastore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := datastores.ParseBearerToken(r.Header.Get("Authorization"), maxTokenLen)
		if token == "" {
			Warn.Printf("Unauthorized HTTP access attempt (bad bearer scheme) from %s", r.RemoteAddr)
			http.Error(w, errBearerScheme, http.StatusUnauthorized)
			return
		}

		key, err := ds.ValidateApiKey(token)
		if err != nil || key == nil {
			Warn.Printf("Unauthorized HTTP access attempt from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Attach the validated key to the request ctx so downstream
		// consumers — per-route scope checks, Sentry key-id tagging (#132),
		// the metrics key-id label (#130) — can identify the caller without
		// ever seeing the bearer token.
		next.ServeHTTP(w, r.WithContext(ContextWithKey(r.Context(), key)))
	})
}

// requireScope gates a route on scope membership (ADR 0004: scope checks are
// per-handler; "read" does not imply "read:tickets" and vice versa). A key
// lacking the scope gets the PermissionDenied JSON via the error-writer
// choke point — message string frozen by the goldens.
func requireScope(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !KeyFromContext(r.Context()).HasScope(scope) {
			writeError(w, r, codePermissionDenied, "scope required: %s", scope)
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
