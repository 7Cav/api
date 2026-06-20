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
route self-gates on a required scope via a `requireScope` wrapper
applied at route registration; missing scope → permission-denied.

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
- A single `net/http` middleware extracts and validates the bearer token
  ahead of routing. (An earlier revision split this across a gRPC
  `UnaryInterceptor` and the HTTP gateway middleware; the single-listener
  cutover removed the gRPC surface, leaving one auth path.)
- Adding a new endpoint requires picking the scope it gates on (or
  introducing a new scope in the upstream admin UI first) and registering
  the route with that scope gate.
- Because auth touches the same DB as the data path, a DB outage is
  intercepted on the auth path before any handler runs. It surfaces as a
  503 (Service Unavailable) — a structurally valid token whose lookup
  fails is a retryable server fault, kept distinct from the 401 an invalid
  key returns. Fault-injection smokes cannot observe a handler's own 5xx by
  breaking the shared DB: the auth 503 fires first.
