# Gzip close-log hijack faithfulness

The gzip middleware does not distinguish a clean connection takeover
(`Hijack()`) from one that abandoned already-committed output. The coarse
behavior shipped in #175 — suppress the deferred `gz.Close()` failure log on
`errors.Is(err, http.ErrHijacked)` — is the accepted state until a real
hijacking handler exists. No faithfulness machinery gets built against
synthesized probes.

## Why this is out of scope

**No caller exists.** The hijack *capability* is live end to end — every
wrapper in the `rest.New` chain implements `Unwrap`, so
`http.ResponseController.Hijack()` walks to the root and succeeds — but grep
finds no production code calling it; every reference is a comment. The gap is
real in the code; the traffic that would expose it is not.

**The feature that would create a caller is itself out of scope.** The API is
read-only by published contract. The only roadmap-shaped feature that wants a
persistent connection is consumer notification tooling, which PRD #112
explicitly lists as out of scope. And if streaming ever lands, the natural
shape for a read-only API behind a reverse proxy is SSE — which streams via
Flusher (the path #167/#174 hardened) and **never hijacks**. HTTP/2
ResponseWriters don't implement Hijacker at all (`ErrNotSupported`, a
different error).

**A deliberate WebSocket implementation wouldn't trigger it either.** Upgrade
endpoints are mounted outside response-compression middleware (compression is
negotiated in-protocol as permessage-deflate), so the corruption shapes below
require *both* an out-of-scoped feature *and* a mount mistake — a conjunction,
not a roadmap item.

**The mount mistake is guarded by a tripwire instead.** #174 carries a
mechanical convention test in the #148/#162/#173 family: no handler in the
rest package hijacks through the gzip chain. The day someone adds one, CI
fails and points here. The trigger is self-announcing; nothing depends on
anyone remembering this file.

## The preserved spec (the behavioral contract, if this is ever reopened)

Three corruption shapes, all probe-reproduced during three rounds of
adversarial review of #175 (probes lived at /tmp/gzprobe):

1. **Hijack-after-write / hijack-after-flush (silent corruption).** Handler
   writes/flushes through the gzip wrapper (compressed bytes buffered or
   partially on the wire), then hijacks and the takeover dies. Client gets
   `Content-Encoding: gzip` with an undecodable/unterminated body; every
   handler call returned nil; the coarse carve-out logs nothing — and the
   close log is, per its own comment, the only server-side signal of this
   corruption class.

2. **Commit-then-hijack (keep-alive response-queue smuggling).** Handler
   commits `WriteHeader(200)` (or a bodyless 304/HEAD), writes nothing, then
   hijacks. stdlib's `Hijack()` flushes the committed headers via its own
   `cw.flush()` (net/http server.go:2206) BEFORE handing over the conn —
   bypassing any wrapper byte-counter. "No bytes through the wrapper" is
   FALSE as a proxy for "nothing on the wire": a fully-formed head-response
   went out, and the hijacker's bytes land in the next-response slot on the
   kept-alive connection. Zero log. The bodyless/HEAD twins are the worst
   case (the flushed response is complete, so the takeover's bytes are
   unambiguously a smuggled second response).

3. **False-truncation log (trust erosion).** Any verdict keyed on "handler
   attempted output" rather than "bytes reached the wire" mislabels intact
   wires: a stray write after a clean hijack, or a flush behind a bona fide
   bodyless status, makes the log claim truncation when the wire is provably
   intact (or smuggled — not truncated).

**The discriminator a proper fix needs:** the close-time error CLASS plus
genuine wire-truth — bytes net/http actually *flushed to the conn*, not bytes
the wrapper accepted, and not the committed-status latch alone. The
bodyless/HEAD skip arms must consult hijack state (or let `gz.Close()` run
and inspect `errors.Is(err, http.ErrHijacked)` vs `http.ErrBodyNotAllowed`)
before staying quiet, rather than returning on a "nothing written" premise
that `Hijack()`'s header flush violates.

**Acceptance:** a handler that hijacks after committing/writing/flushing
produces a distinct, accurate server-side log naming the real shape
(committed-flushed-no-stream / truncated-after-output /
smuggled-after-bodyless); a clean pristine takeover stays quiet; no intact
wire is ever labeled truncated. Pin each shape end-to-end with a real conn
hijack. Build against a real hijacking handler — real wire shapes beat
synthesized ones.

## Prior requests

- #181 — "rest: gzip close-log hijack faithfulness — distinguish clean
  takeover from abandoned-output corruption" (carved out of #175 by
  maintainer ruling 2026-06-07; closed out-of-scope 2026-06-07, tripwire
  folded into #174)
