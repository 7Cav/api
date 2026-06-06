# Golden contract corpus

The recorded contract of the public HTTP API (PRD #112, issue #116). These
goldens are the red suite for the stdlib `net/http` rewrite and live on
permanently as the contract regression net: any stack that serves these
routes must reproduce every golden in `goldens/`.

## What a golden is

One file per battery case (`battery.go` → `Cases()`), named
`goldens/<case>.golden.json`. Each records:

- the request (method, path, auth tier — see `Auth` in `golden.go`),
- the response **status code**,
- the **contract-relevant headers** — allowlist in `golden.go`
  (`Content-Type`, `X-Content-Type-Options`, `WWW-Authenticate`); everything
  else (`Date`, `Content-Length`, `X-Cache`, `Grpc-Metadata-*`) is
  infrastructure of the current stack, not contract. `WWW-Authenticate` pins
  an *absence*: #106 froze the 401 tiers as challenge-free, so no golden
  records it and a stack that adds it diffs red,
- the response **body**: canonicalized JSON (`body`) or, for the plain-text
  401 tier pinned by #106, the verbatim text (`bodyText`).

## Comparison is semantic, never byte-based

`protojson` randomizes whitespace per build, so today's API is already not
byte-stable. Replay therefore parses both sides, canonicalizes, and diffs
structurally (`CompareGolden`; the canonicalizer and differ are internal):

- object key order never matters;
- array order and length matter;
- number *form* matters: protojson emits 64-bit integers as JSON strings and
  32-bit integers as JSON numbers — `"3"` ≠ `3`;
- `null`, `{}`/`[]`, and an absent key are three distinct states
  (emit-everything semantics are part of the contract);
- byte-level output is informational only (printed on failure for debugging).

## The single transform: `stripKeycloakID`

Exactly one transform sits between the wire and the goldens: every object key
named `keycloakId`, at any depth, is removed (`stripKeycloakID` in
`canon.go`, applied automatically inside `RunCase`). The field — like the keycloak lookup route — is deleted at
cutover; goldens record the truth the new stack must reproduce, and the
transform documents the break. It is applied symmetrically: to responses at
record time and at replay time, so the corpus stays green against the old
stack while it remains in-tree. **No other transform exists.**

The keycloak lookup route (`/api/v1/milpac/keycloak/{keycloak_id}`) is
deliberately **not** recorded — it dies at cutover, so there is no golden to
hold it to. That leaves the 16 surviving public routes, all covered
(enumerated on `Cases()` in `battery.go`; coverage enforced by
`TestBatteryIsWellFormed`).

## Public surface

The package is a deep module: a small frozen API for the Phase 3 replay
consumers (#125–#129), with the canonicalizer and differ kept internal
(`canonicalize`, `marshalCanonical`, `stripKeycloakID`, `diff` in `canon.go`).
Re-exporting any of them later is a non-breaking change if a consumer ever
needs one. The exported surface is exactly five entries:

1. `Cases() []Case` / `Case` — the recorded request battery.
2. `Auth` and its constants (`AuthNone`, `AuthRawKey`, `AuthInvalidKey`,
   `AuthRead`, `AuthReadTickets`, `AuthNoScopes`) — credential tiers.
3. `Golden` — the recorded contract for one case.
4. `RunCase(http.Handler, Case)` — drive one case through a mounted stack,
   returning the observed `Golden` (canonicalized, transform applied).
5. `CompareGolden(want, got)` / `SaveGolden` / `LoadGolden` — semantic
   comparison and golden persistence.

## How the corpus was recorded

`TestMain` (`harness_test.go`) mounts the **current production stack**
in-process: the real `gateway.Service.Server()` handler (auth middleware,
sentry, cache, compression, `/api` routing, grpc-gateway mux) dialing a real
`grpc.Server` over TCP with the production interceptor chain, over a seeded
deterministic fake datastore (`fake_datastore_test.go`). No MySQL, no Redis,
no docker: the Redis client points at an always-erroring local stub, so every
request takes the cache-miss path (the cache layer leaves at Phase 2, #123).

Seed highlights (all referenced by path literals in the battery):

- **relation 1 ↔ user 3** (`Jarvis.A`): the by-id profile route's path value
  is the milpac *relation* key, not the forum user id — mirrors
  `datastores.Mysql.FindProfilesById`, which keys `First()` on the
  `milpacs.Profile` primary key (`relation_id`). The
  `milpacs/profile_by_id_happy` golden proves relation wins.
- relation 2 ↔ user 8 (`John.Doe`): the sparse profile — unset nested
  message (`primary: null`), empty collections, empty strings.
- tickets 42/43/44 with distinct categories (5 is a child of 1), states,
  statuses and timestamps, so every list filter selects a distinguishable
  subset; cursors are the stack's real base64url forms.
- injected outages: profile id `777` and `ROSTER_TYPE_ARLINGTON` produce the
  Internal-error (500) goldens with their leaked wrapped messages — frozen.
- position search returns hits only for the exact title
  `Regimental Technical Aide`; everything else is the frozen `{"profiles":{}}`
  empty result (#137).

## Replaying

```
go test ./contract/...
```

Runs the whole battery against the in-process current stack and compares
every response to its committed golden. Plain `go test`, no docker, no
network beyond loopback, sub-second after build.

To replay against a *future* stack: mount its `http.Handler`, then for each
`Cases()` entry run `RunCase(handler, c)` and `CompareGolden` the result
against `LoadGolden("contract/goldens", c.Name)`. The new stack's test seed
must reproduce the logical seed above (the goldens themselves are the
authoritative value reference) and accept the battery's bearer tokens
(`authHeader` in `golden.go`).

## Re-recording

Possible only while the old stack remains in-tree:

```
go test ./contract -run TestContractCorpus -update
```

This overwrites `goldens/` from the live current stack. Review the diff like
any other code change — a re-record that changes bodies is a contract change
and needs the same scrutiny as one. `TestCorpusHasNoOrphanGoldens` fails if a
golden loses its battery case; delete orphaned files as part of the same
change that removes a case.
