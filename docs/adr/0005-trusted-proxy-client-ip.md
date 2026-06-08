# Client-IP resolution is gated on a trusted-proxy set

The two auth 401 log lines must report the real client IP. The API runs behind a
TLS-terminating reverse proxy, so the socket peer (`r.RemoteAddr`) is always that
proxy, not the caller. We resolve the client address from forwarding headers
(`X-Forwarded-For` / `X-Real-IP`) **only when the TCP peer is in a configured
trusted-proxy set** (`TRUSTED_PROXIES`, comma-separated CIDRs; default empty =
trust nothing). For a trusted peer we take the right-most `X-Forwarded-For` entry
not in the trusted set, fall back to `X-Real-IP`, then the socket peer.

The trusted set contains **only the immediate upstream proxy the API actually
peers with** — not any edge CDN in front of it. The edge already normalizes the
connecting client into the forwarding headers, and the API never connects to the
CDN directly, so trusting the CDN's ranges would be both unnecessary and a wider
trust surface than required.

## Considered Options

- **Honor forwarding headers unconditionally** — rejected: forwarding headers are
  caller-suppliable and must only be trusted when they arrive from a known proxy;
  honoring them from any peer would let the logged client address be forged.
- **Read `X-Real-IP` or the left-most `X-Forwarded-For` entry without a trust
  gate** — rejected: both are caller-suppliable; the left-most XFF entry is the
  spoofable one.
- **Peer-gated right-most-untrusted XFF** — chosen: the only variant that resists
  a forged client address.

## Consequences

Client IPs are currently used for diagnostic logging only. The trust gate is built
now so that any future use depending on a trustworthy client address (e.g. the
per-key rate-limiting noted in PRD #112) can rely on it without rework. A
non-empty but malformed `TRUSTED_PROXIES` is fatal at startup; empty/unset trusts
nothing and preserves the prior (proxy-address) logging behavior.

The malformed-XFF → `X-Real-IP` fallback is an **availability-biased** choice for
diagnostic logging, not a security control: both headers originate from the same
trusted proxy, and the result is not used for authorization or rate limiting. It
is documented here so it is not later "tightened" into a stricter rejection.
