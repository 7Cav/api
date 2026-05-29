# ADR 0004: Scope-based bearer auth validated against the upstream DB on every request

## Context

The API needs to authenticate machine clients and gate individual
endpoints by capability (e.g., a key that can read profile data should
not necessarily be able to read tickets). Keys are issued and managed in
the upstream forum application via its admin UI, not by this service.
We need an auth model where revocation and capability changes made in
the upstream UI take effect immediately, without a deploy or a cache
invalidation in this service.

## Decision

Clients send a `Bearer` token (case-insensitive prefix per RFC 7235),
which is validated on every request by a single JOIN over three upstream
tables: the key table, the key-to-scope mapping, and the scope catalog.
A row is returned per scope granted to the key. Zero rows → 401.

The validation result carries the set of granted scope names. Each
handler self-gates by calling a `RequireScope(ctx, "<scope_name>")`
helper at the top of its body; missing scope → permission-denied.

The scope catalog (which scopes exist, what they mean) is owned by the
upstream application's admin UI. This service treats the catalog as
read-only and never writes to it.

The bearer token has a hard length cap to bound the cost of the lookup
on malformed input.

## Consequences

- Revocation, key creation, and scope changes are instant: the next
  request hits the new state. No restart, no cache flush.
- Every request pays for one DB round-trip on the auth path. This is
  acceptable for the current load and keeps the model simple; if it
  becomes a bottleneck, a short-TTL in-process cache keyed by token hash
  is the obvious next step.
- The gRPC `UnaryInterceptor` and the HTTP gateway middleware must both
  extract and validate the bearer token; they share a parsing helper to
  keep the prefix handling consistent across surfaces.
- Adding a new endpoint requires picking the scope it gates on (or
  introducing a new scope in the upstream admin UI first) and adding a
  `RequireScope` call at the top of the handler.
- Because auth touches the same DB as the data path, an outage in that
  DB surfaces as 401 from this service, not as a 5xx — fault-injection
  smokes against handler-level error mapping must account for this.
