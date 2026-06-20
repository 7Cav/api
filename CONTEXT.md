# Context

Domain language used by the 7Cav API. The contract is a trio that is
checked in and CI-enforced, not a generated artifact: the **types
package** (`types/`, the hand-written wire types), the **hand-owned
OpenAPI 3.1 spec** (`openapi/openapi.yaml`), and the **golden corpus**
(`contract/goldens/`, one recorded request/response per public route).
The spec is validated against the corpus, so the three stay in step. This
document covers the concepts and any nuance that isn't obvious from
reading them. The proto/buf toolchain was retired in Phase 4 (#135, ADR
0006); the API is now a plain net/http JSON service with hand-written
handlers and types.

## Source data

The API is a read layer over a [XenForo](https://xenforo.com) forum's
MySQL database, augmented by the `NF Rosters` add-on (which contributes
the `xf_nf_rosters_*` tables that hold milpac records) and the
`Cav7/ApiKeyManager` add-on (which contributes the API-key and scope
tables). The API itself owns no schema; it queries upstream tables and
maps them to its own Go types (the `types` package).

## Milpac

A "milpac" is a member's military-personnel-record entry: rank,
position, awards, service record, and the identifiers used to look them
up across the wider 7Cav stack. `MilpacService` is the surface that
serves them.

## Profile shapes

A member's milpac is served in three shapes by different routes, each
tuned to a known consumer:

- **`Profile`** — full view: rank, positions, awards, records, and the
  connected-account identifiers.
- **`LiteProfile`** — slim view: enough to render a roster row, without
  the per-member relational payload.
- **`S1UniformsProfile`** — view for the uniforms tool; includes
  uniform-relevant fields and omits the rest.

The three are not subsets of one type; they are hand-mapped from the
same upstream rows into distinct wire types in the `types` package.

## Roster and RosterType

A `Roster` is a collection of members grouped by unit, course, or
status. `RosterType` is an enum identifying which roster is requested;
its numeric values are used as the `roster_id` foreign key in the
upstream tables. The three roster routes return the same set of members
in the corresponding profile shape above.

## Rank, Position, PositionGroup

A `Rank` is a pay-grade entry from the upstream rank catalog. A
`Position` is an org-chart slot; positions are grouped into
`PositionGroup`s for hierarchical browsing. `RankExpanded` and
`PositionExpanded` are the variants that include relational fields the
plain message omits.

## Forum group

A "forum group" is an entry in the XenForo permission-group directory
(`xf_user_group`): a named membership group such as a rank, a staff
position, or a member-status flag. These are the IDs the forum's
`UserGroupsScope` add-on hands a client from the forum's own `/api/me`.

A forum group is not a `PositionGroup`. A `PositionGroup` is a roster
construct from the NF Rosters add-on used to browse the org chart. A
forum group is the forum's own membership unit, a wider set that also
covers ranks and non-roster groups.

The group directory resolves a forum-group ID to its display name. The
ID is the stable key; the name is display data that can change at any
time, so consumers key their logic on IDs and treat names as labels.

## Record and Award

A `Record` is an entry on a member's service history (joins, promotions,
transfers, etc.); `RecordType` enumerates the categories. An `Award` is
a decoration entry.

## AWOL

An entry on the AWOL list — members flagged as absent without leave.
Served by the AWOL route, used by status-tracking consumers.

## Connected accounts

Members are looked up by 7Cav user id, by username, and by external
account identifiers maintained by the forum's connected-account
integrations:

- **Discord** — Discord user id.
- **Gamertag** — Xbox / PlayStation handle.
- **Keycloak** — legacy SSO identifier. The Keycloak auth path was
  removed, and the lookup route went with the cutover: the route and the
  `keycloakId` field are gone from the served surface, and the golden
  corpus records that removal (`stripKeycloakID` in `contract/canon.go`).
  Nothing in new code should reference either.

## Tickets

The tickets surface exposes the forum's ticket system (powered by the
`NF Tickets` add-on) as a read-only API.

- **`Ticket`** — a thread: title, status, category, participants,
  message count, timestamps. `forum_url` is populated when the API is
  configured with the public forum base URL.
- **`Message`** — one post within a ticket, addressed by `position`
  (0-indexed within the thread).
- **`Category`** — a top-level grouping for tickets; carries a current
  ticket count.
- **`TicketParticipant`** — a member-to-ticket association with a role.

`ListTicketMessages` paginates with an opaque cursor whose semantic is
"next `position` to include" (inclusive lower bound), so `position=0`
is reachable.

## API key and scope

Clients authenticate with a `Bearer` token (case-insensitive prefix per
RFC 7235). Tokens are issued by the forum admin UI, not by this
service. Each token carries a set of named scopes. Current scopes:

- **`read`** — gates the milpac surface (profiles, rosters, ranks,
  positions, AWOL).
- **`read:tickets`** — gates the tickets surface.

Scope membership is checked per-handler; a token with `read` cannot
read tickets, and vice versa.

## Wire conventions (house style)

The generated stack produced a particular JSON shape; the rewrite kept it
and adopted it as house style for every new field. The golden corpus and
the spec replay enforce it on covered routes, and the tag lint in
`types/wireconventions_test.go` enforces it at declaration time:

- **JSON names are lowerCamelCase** (e.g. `rankId`, not `rank_id`).
- **Emit everything**, no `omitempty`. An absent key, `""` and `0` are
  distinct wire states, so every field is always present. Unset nested
  messages serialize as `null`; empty collections serialize as `[]` / `{}`
  (never `null`, so the handlers must allocate empty slices and maps).
- **64-bit integers serialize as JSON strings** (the `,string` tag);
  32-bit integers stay JSON numbers. `"3"` and `3` are different on the
  wire, and the contract differ preserves the distinction.
- **Enums serialize as their name strings**, with the zero value emitting
  the `*_UNSPECIFIED` name. Values without a name fall back to the bare
  number. `RosterType` is the worked example.

## Adding an endpoint

The sequence that used to be one proto edit is now a short hand-written
checklist. The spec and golden steps are CI-enforced, so a missed step
fails the build naming the operation or field rather than shipping drift:

1. **Wire types** in `types/`: follow the house style above; the tag
   lint checks the tags with no registration needed.
2. **Handler** in `rest/`: map the datastore result to the wire types
   (allocate empty collections), write JSON on success, and write the
   frozen error message on failure.
3. **Route registration** in `rest/`'s `routes()`: register the path
   with its required scope gate and `Cache-Control` max-age.
4. **Spec operation** in `openapi/openapi.yaml`: its documented responses
   feed the two-way coverage check.
5. **Goldens** in `contract/goldens/`: add the route's cases so the
   replay harness covers it.

`contract/spec_test.go` asserts coverage in both directions: every spec
operation needs a witnessing golden (with at least one 2xx), and every
golden route must resolve to an operation. A new endpoint that skips the
spec or golden step fails that suite; there is no tribal knowledge to
forget. See `rest/rest.go` and `types/types.go` for the in-code long form.

## Which test do I write?

The repo has several test idioms; a change usually touches more than one:

- **Handler behavior** → a test in `rest/`: black-box (`package rest_test`,
  driving the mounted handler) for request/response behavior, white-box
  (`package rest`) for an unexported seam.
- **Wire contract** → a golden in `contract/goldens/` plus the matching
  operation in `openapi/openapi.yaml`; `contract/spec_test.go` enforces the
  two-way coverage and fails naming the gap.
- **SQL / datastore** → a `datastores/*_harness_test.go` against the MariaDB
  seam below (`testdb.Open(t)`), and only when you touched a query or schema.

Rule of thumb for a new endpoint: unit-test the handler in `rest/`, add the
golden in `contract/`, and add a datastore harness test only if SQL changed. CI
runs the unit + contract suite on every push and the harness suite against a
MariaDB service container; run `make test-integration` for the harness locally.

## SQL seam (integration-test harness)

`testdb/` is the dockerized MariaDB harness — the "SQL seam" from PRD
#112's testing decisions. `testdb.Open(t)` hands a test its own
disposable database (forum-shaped schema + fixtures, embedded in the
package) on a MariaDB 11.5 server; tests opt in via `TESTDB_ADDR` and
skip without it. Run locally with `make test-integration`.

Two properties of the harness are load-bearing:

- The schema deliberately omits the four indexes PRD #112 proposes, so
  "red" EXPLAIN plans stay reproducible (each test may CREATE INDEX in
  its own database to observe the flip).
- The fixtures include a member whose milpac `relation_id` collides
  with another member's forum `user_id` (205), keeping the by-id
  profile route's frozen relation-key semantic testable.

## Index script (PRD #112 Phase 1)

`testdb/indexes.sql` is the in-repo source of truth for the four
indexes backing the hot read paths (composite `user_id_post_date` on
`xf_post` serving two distinct aggregations — a loose index scan for
the last-post aggregation and a covering index scan for the AWOL
report's variant, whose extra `MAX(post_id)` disqualifies the loose
scan; relation-id and user-id indexes on the rosters tables for the
profile preloads). The EXPLAIN-plan tests in `testdb/indexes_test.go`
pin each flip red→green: the unindexed schema must full-scan, the
script must produce the loose scan, the covering scan, and
index-backed preloads — a query or schema change that silently
reintroduces a full scan fails a test, not a production latency
budget.

The API never executes DDL. The script is applied manually by the DB
admin (`mysql xenforo < testdb/indexes.sql`, human-gated in #122) and
re-applied with the same one command after any forum add-on upgrade
that rebuilds the tables (idempotent: `ADD INDEX IF NOT EXISTS`). The
long-term home for re-application is the ApiKeyManager add-on's schema
step (per PRD #112) — documented intent only, not implemented.
