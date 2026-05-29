# Context

Domain language used by the 7Cav API. The proto files
(`proto/milpacs.proto`, `proto/tickets.proto`) are the contract; this
document covers the concepts and any nuance that isn't obvious from
reading the schema.

## Source data

The API is a read layer over a [XenForo](https://xenforo.com) forum's
MySQL database, augmented by the `NF Rosters` add-on (which contributes
the `xf_nf_rosters_*` tables that hold milpac records) and the
`Cav7/ApiKeyManager` add-on (which contributes the API-key and scope
tables). The API itself owns no schema; it queries upstream tables and
maps them to its own proto types.

## Milpac

A "milpac" is a member's military-personnel-record entry: rank,
position, awards, service record, and the identifiers used to look them
up across the wider 7Cav stack. `MilpacService` is the surface that
serves them.

## Profile shapes

A member's milpac is served in three shapes by different RPCs, each
tuned to a known consumer:

- **`Profile`** — full view: rank, positions, awards, records, and the
  connected-account identifiers.
- **`LiteProfile`** — slim view: enough to render a roster row, without
  the per-member relational payload.
- **`S1UniformsProfile`** — view for the uniforms tool; includes
  uniform-relevant fields and omits the rest.

The three are not subsets of one type; they are hand-mapped from the
same upstream rows into distinct proto messages.

## Roster and RosterType

A `Roster` is a collection of members grouped by unit, course, or
status. `RosterType` is an enum identifying which roster is requested;
its numeric values are used as the `roster_id` foreign key in the
upstream tables. The three roster RPCs (`GetRoster`, `GetLiteRoster`,
`GetS1UniformsRoster`) return the same set of members in the
corresponding profile shape above.

## Rank, Position, PositionGroup

A `Rank` is a pay-grade entry from the upstream rank catalog. A
`Position` is an org-chart slot; positions are grouped into
`PositionGroup`s for hierarchical browsing. `RankExpanded` and
`PositionExpanded` are the variants that include relational fields the
plain message omits.

## Record and Award

A `Record` is an entry on a member's service history (joins, promotions,
transfers, etc.); `RecordType` enumerates the categories. An `Award` is
a decoration entry.

## AWOL

An entry on the AWOL list — members flagged as absent without leave.
Served by `GetAwol`, used by status-tracking consumers.

## Connected accounts

Members are looked up by 7Cav user id, by username, and by external
account identifiers maintained by the forum's connected-account
integrations:

- **Discord** — Discord user id.
- **Gamertag** — Xbox / PlayStation handle.
- **Keycloak** — legacy SSO identifier. The Keycloak auth path has been
  removed; the lookup RPC is on the chopping block and should not be
  used in new code.

## Tickets

`TicketsService` exposes the forum's ticket system (powered by the
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
