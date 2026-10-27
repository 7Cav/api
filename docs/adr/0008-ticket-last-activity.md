# Ticket last activity is derived by the API and served as `lastModifiedDate`

The NF Tickets add-on stopped maintaining `xf_nf_tickets_ticket.last_modified_date`, so every production ticket opened since 2026-01-25 reads 0 and `modifiedSince` missed replies, as #289 reports. The API now computes each ticket's last activity as the latest of its start, its last public reply, the old column, and a fixed list of actions in the add-on's activity log, and it sorts, filters, pages and serves `lastModifiedDate` on that value. A computed value reaches every existing caller with no code change on their side and doesn't wait on the add-on developer.

## Considered options

- **A new `lastActivityDate` field.** Callers would get either a mostly-zero `lastModifiedDate` beside it or two fields with the same value, and existing callers would see nothing change until they rewrote their code.
- **Fixing the forum.** That waits on the add-on developer. The forum's main ticket list already sorts by last message, so only its watched-tickets page would gain.
- **Counting every activity-log row.** The add-on writes a row to each of a member's open tickets when the member is banned, approved or rejected, and to a ticket when another ticket links it. Those rows would push tickets into polls with nothing in the response to show why. The list counts status, assignment, priority, category, title, prefix, lock and visibility changes, merges, and the ticket-opened and warning-created rows. A new action type counts only once someone adds it.
- **Indexes on the activity log.** On a production-sized copy the derived query took 23 to 54 ms, against 1.7 ms before. Two log indexes would bring a list to about 25 ms and a poll to about 7 ms. Requests already take about 300 ms end to end, so nobody would notice the gain, and each index is a hand-applied step that an add-on upgrade can drop. Add them through `testdb/indexes.sql` only if production latency calls for it.

## Consequences

- `lastModifiedDate` no longer equals the forum column it is named after. Don't map it back.
- Ticket queries read the activity log, which first appears in NF Tickets 2.11. The API needs that version or later.
