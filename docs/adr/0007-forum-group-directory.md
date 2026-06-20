# ADR 0007: The forum-group directory is served whole, unfiltered, under the `read` scope

## Context

The [UserGroupsScope][ugs] add-on lets a forum OAuth client read the
authenticated user's own group IDs from the forum's `/api/me`. That
response is numeric IDs only, and the forum API has no ID-to-name
lookup, so a consumer that wants to show a readable group name has to
hardcode the mapping itself. #203 asks this API to provide the lookup,
since it is already a read layer over the same forum DB.

The IDs come from `xf_user_group`, the forum's permission-group
directory. In the production forum that table holds about 300 groups.
Most are rank and staff-position groups whose names are already
derivable from the milpac surface this API serves under `read`. A tail
of them are internal or utility groups (Discord sync groups, bot
accounts, admin overrides) whose names are not otherwise exposed.

Three things had to be decided: how much of the directory to expose,
which scope gates it, and the response shape.

## Decision

`GET /api/v1/forum/groups` returns the whole directory: every row of
`xf_user_group` as a `{groupId, groupName}` pair, ordered by `groupId`
ascending, with no filtering. It is gated by the existing `read` scope.
IDs are the stable key; names are display data.

We expose the full directory rather than a filtered set or a per-request
subset because the consumer's IDs come from the same `xf_user_group`
space. Any group a member belongs to can appear in the forum's
`/api/me`, so hiding a group would leave the consumer holding an ID it
cannot resolve, which is the exact gap the endpoint exists to close. A
subset endpoint (resolve only the IDs I pass) was a fair alternative,
but it adds query-binding surface for a small, highly cacheable payload,
and the full directory subsumes it.

We reuse `read` rather than add a `read:groups` scope. The scope catalog
is owned upstream in the `Cav7/ApiKeyManager` ACP (ADR 0004), so a new
scope would have to be created there and granted to existing keys before
the endpoint was usable. The directory's sensitivity matches what `read`
already exposes, so that coordination cost buys little.

## Consequences

- Every `read` key can enumerate all forum group names, including the
  internal and utility groups that are not otherwise on the served
  surface. This is names only; the endpoint never exposes group
  membership.
- Narrowing the set later (filtering some groups out) is a breaking
  change for any consumer that has come to rely on resolving an
  arbitrary ID, so the full-directory contract is hard to walk back.
- If a future consumer should see group names but not the milpac
  surface, that is the point to revisit and introduce `read:groups`.
  Until then, no upstream scope work is required.
- The directory is small and slow-changing, so it is served with the
  same `Cache-Control` max-age as the rest of the `read` family.

[ugs]: https://github.com/7Cav/UserGroupsScope
