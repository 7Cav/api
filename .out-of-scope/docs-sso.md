# SSO integration with the API docs

The docs surface stays anonymous. There is no plan to wire the forum's SSO into
the Scalar docs page so logged-in members get a different experience.

## Why this is out of scope

The payoff was never pinned down. The request that raised it (#216) was an open
placeholder — scope, behavior, and implementation all undecided — with no
concrete member-facing benefit that being logged in would unlock on a static API
reference page. Weighed against the work to put a session layer on a surface
that is currently static embedded assets (`rest.DocsHandler` serves the vendored
Scalar bundle and the spec verbatim, no cookies, no session), the juice isn't
worth the squeeze.

This is a value call, not a technical impossibility. If a real, member-facing
reason for logged-in docs ever turns up, delete this file and let it go through
triage fresh.

## Prior requests

- #216 — "Investigate SSO integration with the API docs" (placeholder, closed
  out-of-scope 2026-06-29)
