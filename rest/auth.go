package rest

import (
	"context"
	"net/http"
	"strconv"

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
		if err != nil {
			// Datastore failure is a server fault, not a credential
			// rejection: a 401 here would tell a legitimate client its key
			// went bad in the middle of an outage. Unavailable (503) signals
			// retryable, through the JSON choke point like every non-401
			// error. Log the cause with request context — the goldens cannot
			// witness this branch — but NEVER the token.
			Error.Printf("validating API key for %s %s: %v", r.Method, r.URL.Path, err)
			writeError(w, r, codeUnavailable, "service unavailable")
			return
		}
		if key == nil {
			// Zero rows: an unknown/expired key. The generic 401 tier —
			// leaks nothing about whether a key exists, is expired, or
			// lacks scopes (golden-pinned, #106).
			Warn.Printf("Unauthorized HTTP access attempt from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Fill the metrics label-holder's key-id slot (#130): the metrics
		// middleware is UPSTREAM (outer) of auth and r.WithContext below
		// clones the request, so the key attached to the INNER context never
		// reaches it — the mutable holder in the (shared parent) context is
		// the only channel. The id, never the bearer token (same rule as the
		// Sentry key_id tag). nil holder = chain without metrics (the legacy
		// gateway reuses this middleware until cutover deletes that stack).
		if labels := metricLabelsFromContext(r.Context()); labels != nil {
			labels.keyID = strconv.FormatUint(uint64(key.KeyId), 10)
		}

		// Attach the validated key to the request ctx so INNER consumers can
		// identify the caller without ever seeing the bearer token: the
		// per-route scope checks (requireScope) and, until cutover deletes
		// it, the legacy gateway's Sentry key-id tagging (its sentry layer
		// sits inside auth). The new stack's sentry/metrics middlewares are
		// UPSTREAM (outer) of auth — r.WithContext clones the request, so
		// their request never carries this value; key-id reaches them via
		// the context label-holder mechanism (#130), not this key.
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
