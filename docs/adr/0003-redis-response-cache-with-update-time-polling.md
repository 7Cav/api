# ADR 0003: Redis response cache invalidated by polling MySQL UPDATE_TIME

> **Status: Superseded** by PRD #112 ("Goodbye gRPC"), Phase 2 — De-cache.
>
> The cache existed to paper over missing database indexes. Phase 1 (#119,
> applied to prod via #122) added the covering/composite indexes, and the
> measured numbers made the cache redundant: cache-miss latency fell from
> ~1.2–1.7s (combat roster 1.70s, AWOL 1.17s, profiles ~1.25s) to ~27–40ms
> on profile lookups (~40×), 123ms AWOL, 145ms position search, 503ms
> combat roster (now serialization-dominated — #127's problem). Every
> request now takes the former "miss" path and is still an order of
> magnitude faster than the latency the cache was built to hide.
>
> The cache middleware left the HTTP chain at #123 (with it, the `X-Cache`
> header — an enumerated break); after a clean soak, #124 deleted the
> cache package, the `CacheManager` polling goroutine, the
> `GetTableUpdates` datastore method, and Redis itself (client dependency,
> `REDIS_*` env, compose service). The binary starts and serves with no
> Redis configured at all. `referencecache/` (in-memory ticket reference
> data) is unrelated and remains.

## Context

The API is a read layer over a MySQL database it does not own. Almost
every endpoint is a GET that runs joins and preloads against tables that
change infrequently relative to read volume. We need a cache layer that
keeps responses fast without serving data that has gone stale after a
write — but the writes happen in the upstream application, not in this
service, so we cannot bust the cache from the write path.

## Decision

Cache successful HTTP GET responses in Redis, keyed by request path, with
a long TTL (currently 6h). The cache middleware sits inside the auth
middleware so authentication still runs on every request, and serves
cached bodies pre-gzipped.

Invalidation runs in a background goroutine that polls
`information_schema.tables` on a fixed interval (currently 10 minutes)
for a fixed set of monitored tables. If any monitored table's
`UPDATE_TIME` is newer than the cached snapshot, the goroutine issues a
Redis `FlushAll`. There is no per-record bust and no per-endpoint bust.

## Consequences

- Reads are cheap and uniform; the long TTL is safe because the polling
  goroutine flushes everything when upstream data changes.
- Stale data is possible for up to one polling interval after an upstream
  write — acceptable because the read surface is not transactional.
- The blast radius of any write is one `FlushAll`; this is intentional
  and keeps the invalidation logic trivial.
- Adding a new endpoint that reads from a previously-unmonitored upstream
  table requires registering that table in the monitored set, or stale
  responses linger for up to one TTL.
- Cache middleware logging is intentionally verbose at INFO (HIT / MISS
  per request); it is the current substitute for request analytics.
