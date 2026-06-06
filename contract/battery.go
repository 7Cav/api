package contract

// Cases returns the full recorded request battery: every surviving public
// route × happy/edge/error/auth cases, both enum path forms, repeated-filter
// and dual-spelling query combinations on tickets.
//
// 16 surviving public routes (the keycloak lookup route
// /api/v1/milpac/keycloak/{keycloak_id} is deliberately NOT recorded — it
// dies at cutover, so there is no golden to hold it to):
//
//  1. GET /api/v1/milpacs/profile/id/{user_id}
//  2. GET /api/v1/milpacs/profile/username/{username}
//  3. GET /api/v1/milpac/discord/{discord_id}
//  4. GET /api/v1/milpac/gamertag/{gamertag}
//  5. GET /api/v1/roster/{roster}
//  6. GET /api/v1/roster/{roster}/lite
//  7. GET /api/v1/s1/uniforms/{roster}
//  8. GET /api/v1/milpacs/position/search/{position_query=**}
//  9. GET /api/v1/milpacs/ranks
//  10. GET /api/v1/milpacs/position/groups
//  11. GET /api/v1/milpacs/awol
//  12. GET /api/v1/tickets
//  13. GET /api/v1/tickets/{ticket_id}
//  14. GET /api/v1/tickets/ref/{ticket_ref}
//  15. GET /api/v1/tickets/{ticket_id}/messages
//  16. GET /api/v1/tickets/categories
//
// Path literals reference the recording seed (see fake_datastore_test.go and
// contract/README.md): relation 1 ↔ user 3 (Jarvis.A), relation 2 ↔ user 8
// (John.Doe), tickets 42/43/44, injected-outage ids 777 (profile) and
// ROSTER_TYPE_ARLINGTON (roster). Cursor literals are base64url of the
// deterministic seed cursors, exactly as the stack emits them.
func Cases() []Case {
	const (
		// base64url("1748700000:44") — next_cursor after page 1 of
		// /api/v1/tickets?per_page=1 against the seed.
		ticketsPage2Cursor = "MTc0ODcwMDAwMDo0NA"
		// base64url("1") — next_cursor after page 1 of
		// /api/v1/tickets/42/messages?per_page=1 against the seed.
		messagesPage2Cursor = "MQ"
	)

	return []Case{
		// --- Auth tiers (pinned by #106): plain-text two-tier 401s, no
		// WWW-Authenticate; scope failures surface as gRPC-status JSON 403s.
		{Name: "auth/milpacs_missing_header", Method: "GET", Path: "/api/v1/milpacs/profile/id/1", Auth: AuthNone,
			Notes: "No Authorization header: plain-text 401 naming the Bearer scheme."},
		{Name: "auth/milpacs_raw_key", Method: "GET", Path: "/api/v1/milpacs/profile/id/1", Auth: AuthRawKey,
			Notes: "Raw key without 'Bearer ' prefix: same scheme-error 401 tier."},
		{Name: "auth/milpacs_invalid_key", Method: "GET", Path: "/api/v1/milpacs/profile/id/1", Auth: AuthInvalidKey,
			Notes: "Well-formed Bearer token, unknown key: generic 'Unauthorized', leaks nothing."},
		{Name: "auth/milpacs_wrong_scope", Method: "GET", Path: "/api/v1/milpacs/profile/id/1", Auth: AuthReadTickets,
			Notes: "Valid key lacking 'read': PermissionDenied JSON via gRPC status mapping."},
		{Name: "auth/milpacs_no_scopes", Method: "GET", Path: "/api/v1/milpacs/profile/id/1", Auth: AuthNoScopes,
			Notes: "Valid key with empty scope set: same PermissionDenied surface."},
		{Name: "auth/tickets_missing_header", Method: "GET", Path: "/api/v1/tickets", Auth: AuthNone,
			Notes: "Tickets surface, no Authorization header: plain-text scheme-error 401."},
		{Name: "auth/tickets_raw_key", Method: "GET", Path: "/api/v1/tickets", Auth: AuthRawKey,
			Notes: "Tickets surface, raw key: scheme-error 401."},
		{Name: "auth/tickets_invalid_key", Method: "GET", Path: "/api/v1/tickets", Auth: AuthInvalidKey,
			Notes: "Tickets surface, unknown key: generic 'Unauthorized'."},
		{Name: "auth/tickets_wrong_scope", Method: "GET", Path: "/api/v1/tickets", Auth: AuthRead,
			Notes: "'read' does not imply 'read:tickets': PermissionDenied JSON."},
		{Name: "auth/unknown_path_authenticated", Method: "GET", Path: "/api/v1/does/not/exist", Auth: AuthRead,
			Notes: "Unknown path under the API prefix with valid auth: JSON 404 body from the gateway mux."},
		{Name: "auth/unknown_path_unauthenticated", Method: "GET", Path: "/api/v1/does/not/exist", Auth: AuthNone,
			Notes: "Auth runs before routing: unknown path without auth is the 401 tier, not 404."},

		// --- Profile by id / username -----------------------------------
		{Name: "milpacs/profile_by_id_happy", Method: "GET", Path: "/api/v1/milpacs/profile/id/1", Auth: AuthRead,
			Notes: "Path value is the MILPAC RELATION key, not the forum user id: seed diverges (relation 1 ↔ user 3) and this golden proves relation wins (user.userId is \"3\"). keycloakId stripped by the corpus transform."},
		{Name: "milpacs/profile_by_id_sparse", Method: "GET", Path: "/api/v1/milpacs/profile/id/2", Auth: AuthRead,
			Notes: "Emit-everything semantics: unset nested message is null (primary), empty collections are [], unset strings are \"\", 64-bit ints are strings."},
		{Name: "milpacs/profile_by_id_not_found", Method: "GET", Path: "/api/v1/milpacs/profile/id/999", Auth: AuthRead,
			Notes: "NotFound: handler-specific message string, verbatim."},
		{Name: "milpacs/profile_by_id_zero", Method: "GET", Path: "/api/v1/milpacs/profile/id/0", Auth: AuthRead,
			Notes: "id 0 parses but is the proto zero value: 'no username or user ID provided'."},
		{Name: "milpacs/profile_by_id_parse_error", Method: "GET", Path: "/api/v1/milpacs/profile/id/abc", Auth: AuthRead,
			Notes: "Non-numeric id: gateway type-mismatch error leaks parse-error text. Frozen."},
		{Name: "milpacs/profile_by_id_internal_error", Method: "GET", Path: "/api/v1/milpacs/profile/id/777", Auth: AuthRead,
			Notes: "Datastore failure: Internal JSON with the wrapped error message leaked. Frozen."},
		{Name: "milpacs/profile_by_username_happy", Method: "GET", Path: "/api/v1/milpacs/profile/username/Jarvis.A", Auth: AuthRead,
			Notes: "Username binding of the same RPC."},
		{Name: "milpacs/profile_by_username_not_found", Method: "GET", Path: "/api/v1/milpacs/profile/username/Ghost.User", Auth: AuthRead,
			Notes: "NotFound message names the username."},

		// --- Discord / gamertag lookups ----------------------------------
		{Name: "milpacs/discord_happy", Method: "GET", Path: "/api/v1/milpac/discord/112233445566778899", Auth: AuthRead,
			Notes: "Full profile via Discord snowflake."},
		{Name: "milpacs/discord_not_found", Method: "GET", Path: "/api/v1/milpac/discord/999000999", Auth: AuthRead,
			Notes: "NotFound message names the discord id."},
		{Name: "milpacs/gamertag_happy", Method: "GET", Path: "/api/v1/milpac/gamertag/CavGamer77", Auth: AuthRead,
			Notes: "Full profile via console gamertag."},
		{Name: "milpacs/gamertag_not_found", Method: "GET", Path: "/api/v1/milpac/gamertag/GhostTag", Auth: AuthRead,
			Notes: "NotFound message names the gamertag."},

		// --- Reference lists ---------------------------------------------
		{Name: "milpacs/ranks", Method: "GET", Path: "/api/v1/milpacs/ranks", Auth: AuthRead,
			Notes: "Rank reference list; 64-bit rankId as string, 32-bit displayOrder as number."},
		{Name: "milpacs/position_groups", Method: "GET", Path: "/api/v1/milpacs/position/groups", Auth: AuthRead,
			Notes: "Nested groups → positions, including false booleans emitted."},
		{Name: "milpacs/awol", Method: "GET", Path: "/api/v1/milpacs/awol", Auth: AuthRead,
			Notes: "AWOL list; uint64 timestamp/postId/milpacId emitted as strings."},

		// --- Roster: both enum path forms, empty, errors -------------------
		{Name: "roster/combat_by_name", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_COMBAT", Auth: AuthRead,
			Notes: "Enum NAME path form. Map keyed by relation id (string keys in JSON)."},
		{Name: "roster/combat_by_number", Method: "GET", Path: "/api/v1/roster/1", Auth: AuthRead,
			Notes: "Enum NUMBER path form: same payload as combat_by_name."},
		{Name: "roster/reserve_empty", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_RESERVE", Auth: AuthRead,
			Notes: "Roster with no members: {\"profiles\":{}}."},
		{Name: "roster/unspecified_by_name", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_UNSPECIFIED", Auth: AuthRead,
			Notes: "Explicit zero enum: 'cannot request null roster type'."},
		{Name: "roster/unspecified_by_number", Method: "GET", Path: "/api/v1/roster/0", Auth: AuthRead,
			Notes: "Numeric zero: same InvalidArgument."},
		{Name: "roster/bogus_enum", Method: "GET", Path: "/api/v1/roster/IMAGINARY_ROSTER", Auth: AuthRead,
			Notes: "Unknown enum literal: gateway parse error leaks enum-parse text. Frozen."},
		{Name: "roster/internal_error", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_ARLINGTON", Auth: AuthRead,
			Notes: "Datastore failure: Internal JSON; message embeds the enum name and wrapped error."},
		{Name: "roster/unknown_query_param_ignored", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_COMBAT?totally_unknown=1&alsoUnknown=x", Auth: AuthRead,
			Notes: "Unknown query parameters are ignored: identical payload to combat_by_name."},

		// --- Lite roster ----------------------------------------------------
		{Name: "roster/lite_combat_by_name", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_COMBAT/lite", Auth: AuthRead,
			Notes: "Lite profile shape (no records/awards arrays)."},
		{Name: "roster/lite_combat_by_number", Method: "GET", Path: "/api/v1/roster/1/lite", Auth: AuthRead,
			Notes: "Number form of the lite roster."},
		{Name: "roster/lite_reserve_empty", Method: "GET", Path: "/api/v1/roster/2/lite", Auth: AuthRead,
			Notes: "Empty lite roster: {\"profiles\":{}}."},
		{Name: "roster/lite_unspecified", Method: "GET", Path: "/api/v1/roster/ROSTER_TYPE_UNSPECIFIED/lite", Auth: AuthRead,
			Notes: "Zero enum on the lite binding."},

		// --- S1 uniforms ----------------------------------------------------
		{Name: "s1/uniforms_combat_by_name", Method: "GET", Path: "/api/v1/s1/uniforms/ROSTER_TYPE_COMBAT", Auth: AuthRead,
			Notes: "S1 uniforms shape, enum name form."},
		{Name: "s1/uniforms_combat_by_number", Method: "GET", Path: "/api/v1/s1/uniforms/1", Auth: AuthRead,
			Notes: "S1 uniforms, enum number form: same payload."},
		{Name: "s1/uniforms_unspecified", Method: "GET", Path: "/api/v1/s1/uniforms/0", Auth: AuthRead,
			Notes: "Zero enum on the uniforms binding."},

		// --- Position search (multi-segment glob) --------------------------
		{Name: "position/search_happy", Method: "GET", Path: "/api/v1/milpacs/position/search/Regimental%20Technical%20Aide", Auth: AuthRead,
			Notes: "URL-encoded spaces decode before reaching the handler; exact title hits."},
		{Name: "position/search_empty_result", Method: "GET", Path: "/api/v1/milpacs/position/search/squad%20leader", Auth: AuthRead,
			Notes: "Plausible query, no rows: {\"profiles\":{}} with 200 — frozen as-is, see #137."},
		{Name: "position/search_multi_segment", Method: "GET", Path: "/api/v1/milpacs/position/search/Platoon/Leader", Auth: AuthRead,
			Notes: "{position_query=**} swallows slashes: multi-segment query routes, returns empty."},
		{Name: "position/search_trailing_slash", Method: "GET", Path: "/api/v1/milpacs/position/search/", Auth: AuthRead,
			Notes: "Bare search path: the ** glob matches the empty segment and the handler rejects it — 'position query cannot be empty'."},

		// --- Tickets: list -------------------------------------------------
		{Name: "tickets/list_default", Method: "GET", Path: "/api/v1/tickets", Auth: AuthReadTickets,
			Notes: "All seeded tickets, last_modified DESC; empty next_cursor, has_more false."},
		{Name: "tickets/list_repeated_status_filter", Method: "GET", Path: "/api/v1/tickets?status_id=1&status_id=5", Auth: AuthReadTickets,
			Notes: "Repeated query params build a repeated field: tickets 43 and 42 only."},
		{Name: "tickets/list_state_filter", Method: "GET", Path: "/api/v1/tickets?ticket_state=resolved", Auth: AuthReadTickets,
			Notes: "String filter: resolved only."},
		{Name: "tickets/list_category_includes_subcategories", Method: "GET", Path: "/api/v1/tickets?category_id=1", Auth: AuthReadTickets,
			Notes: "Category filter expands the subtree by default: parent 1 pulls in child category 5."},
		{Name: "tickets/list_category_exclude_subcategories", Method: "GET", Path: "/api/v1/tickets?category_id=1&exclude_subcategories=true", Auth: AuthReadTickets,
			Notes: "exclude_subcategories=true matches the exact category only."},
		{Name: "tickets/list_starter_filter", Method: "GET", Path: "/api/v1/tickets?starter_user_id=8", Auth: AuthReadTickets,
			Notes: "Starter filter; forum user id (8), not relation key."},
		{Name: "tickets/list_per_page_snake", Method: "GET", Path: "/api/v1/tickets?per_page=1", Auth: AuthReadTickets,
			Notes: "snake_case query key; page 1 with next_cursor and has_more true."},
		{Name: "tickets/list_per_page_camel", Method: "GET", Path: "/api/v1/tickets?perPage=1", Auth: AuthReadTickets,
			Notes: "camelCase spelling of the same key: identical payload to list_per_page_snake."},
		{Name: "tickets/list_page_two", Method: "GET", Path: "/api/v1/tickets?per_page=1&after_cursor=" + ticketsPage2Cursor, Auth: AuthReadTickets,
			Notes: "Round-tripped cursor: page 2 holds ticket 43 and a further cursor."},
		{Name: "tickets/list_invalid_cursor_camel", Method: "GET", Path: "/api/v1/tickets?afterCursor=bogus", Auth: AuthReadTickets,
			Notes: "Malformed cursor (camelCase key): InvalidArgument 'invalid after_cursor'."},
		{Name: "tickets/list_unknown_param_ignored", Method: "GET", Path: "/api/v1/tickets?utterly_unknown=42", Auth: AuthReadTickets,
			Notes: "Unknown query parameter ignored: identical payload to list_default."},

		// --- Tickets: get by id / ref ---------------------------------------
		{Name: "tickets/get_by_id_happy", Method: "GET", Path: "/api/v1/tickets/42", Auth: AuthReadTickets,
			Notes: "Ticket + firstMessages + deprecated top-level totalMessageCount duplicating ticket.totalMessageCount."},
		{Name: "tickets/get_by_id_not_found", Method: "GET", Path: "/api/v1/tickets/9999", Auth: AuthReadTickets,
			Notes: "NotFound names the numeric id."},
		{Name: "tickets/get_by_id_parse_error", Method: "GET", Path: "/api/v1/tickets/abc", Auth: AuthReadTickets,
			Notes: "Non-numeric id under {ticket_id}: leaked parse-error text. Frozen."},
		{Name: "tickets/get_by_ref_happy", Method: "GET", Path: "/api/v1/tickets/ref/MF1UI9HE", Auth: AuthReadTickets,
			Notes: "Ref binding returns the same GetTicketResponse shape."},
		{Name: "tickets/get_by_ref_not_found", Method: "GET", Path: "/api/v1/tickets/ref/NOPE9999", Auth: AuthReadTickets,
			Notes: "NotFound quotes the ref."},

		// --- Tickets: messages ----------------------------------------------
		{Name: "tickets/messages_default", Method: "GET", Path: "/api/v1/tickets/42/messages", Auth: AuthReadTickets,
			Notes: "Full thread, position ascending; BBCode and &/<> characters unescaped in JSON strings."},
		{Name: "tickets/messages_per_page_snake", Method: "GET", Path: "/api/v1/tickets/42/messages?per_page=1", Auth: AuthReadTickets,
			Notes: "Message pagination: 1 row, position-based next_cursor, has_more true."},
		{Name: "tickets/messages_per_page_camel", Method: "GET", Path: "/api/v1/tickets/42/messages?perPage=1", Auth: AuthReadTickets,
			Notes: "camelCase spelling: identical payload to messages_per_page_snake."},
		{Name: "tickets/messages_page_two", Method: "GET", Path: "/api/v1/tickets/42/messages?per_page=1&after_cursor=" + messagesPage2Cursor, Auth: AuthReadTickets,
			Notes: "Round-tripped message cursor: second message only."},
		{Name: "tickets/messages_invalid_cursor_snake", Method: "GET", Path: "/api/v1/tickets/42/messages?after_cursor=bogus", Auth: AuthReadTickets,
			Notes: "Malformed message cursor: InvalidArgument 'invalid after_cursor'."},
		{Name: "tickets/messages_unknown_ticket", Method: "GET", Path: "/api/v1/tickets/555/messages", Auth: AuthReadTickets,
			Notes: "Unknown ticket id: empty page, not 404. Frozen."},
		{Name: "tickets/messages_parse_error", Method: "GET", Path: "/api/v1/tickets/abc/messages", Auth: AuthReadTickets,
			Notes: "Non-numeric id on the messages binding: leaked parse-error text."},

		// --- Tickets: categories ---------------------------------------------
		{Name: "tickets/categories", Method: "GET", Path: "/api/v1/tickets/categories", Auth: AuthReadTickets,
			Notes: "Category reference tree; literal 'categories' segment wins over {ticket_id}."},
	}
}
