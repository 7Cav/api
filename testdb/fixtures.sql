-- Fixture data for the MariaDB integration harness (issue #115).
--
-- Shaped to exercise the behaviors the downstream test families need:
--   * members whose milpac relation_id differs from their forum user_id
--     (the by-id profile route's frozen semantic),
--   * a post table bulky enough for meaningful EXPLAIN plans on the hot
--     last-post aggregation,
--   * tickets spread across categories, statuses, states and visibility,
--   * active and inactive API keys/scopes.

-- ---------------------------------------------------------------------
-- Forum users
-- ---------------------------------------------------------------------

INSERT INTO xf_user
  (user_id, username, email, language_id, style_id, timezone,
   user_group_id, secondary_group_ids, permission_combination_id, secret_key)
VALUES
  (100, 'Trooper.A',   'a@example.test', 1, 0, 'UTC', 2, '', 1, 'k100'),
  (105, 'Trooper.B',   'b@example.test', 1, 0, 'UTC', 2, '', 1, 'k105'),
  (150, 'Trooper.C',   'c@example.test', 1, 0, 'UTC', 2, '', 1, 'k150'),
  (205, 'Trooper.D',   'd@example.test', 1, 0, 'UTC', 2, '', 1, 'k205'),
  (300, 'Reservist.E', 'e@example.test', 1, 0, 'UTC', 2, '', 1, 'k300'),
  (301, 'Discharged.F','f@example.test', 1, 0, 'UTC', 2, '', 1, 'k301'),
  (400, 'TicketGuy.G', 'g@example.test', 1, 0, 'UTC', 2, '', 1, 'k400'),
  (401, 'Helpdesk.H',  'h@example.test', 1, 0, 'UTC', 2, '', 1, 'k401');

-- ---------------------------------------------------------------------
-- Milpacs (NF Rosters)
-- ---------------------------------------------------------------------

INSERT INTO xf_nf_rosters_rank (rank_id, title, rank_image, display_order, extra_group_ids) VALUES
  ( 5, 'Sergeant Major',   5, 50,  ''),
  (15, 'Sergeant',        15, 150, ''),
  (24, 'Specialist',      24, 240, ''),
  (27, 'Private',         27, 270, '');

INSERT INTO xf_nf_rosters_position_group (position_group_id, title, display_order) VALUES
  (1, 'Regimental HQ',              10),
  (2, 'New Recruits',               20),
  (3, 'Alpha Company',              30),
  (4, 'Extended Leave Of Absence',  40);

INSERT INTO xf_nf_rosters_position
  (position_id, position_title, position_group_id, display_order, extra_group_ids, possible_secondary) VALUES
  (10, 'Rifleman',         3, 10, '', 0),
  (11, 'Squad Leader',     3, 20, '', 0),
  (20, 'Military Police',  1, 30, '', 1),
  (30, 'Recruit',          2, 40, '', 0),
  (40, 'ELOA',             4, 50, '', 0);

-- Roster ids follow proto.RosterType: 1 combat, 2 reserve, 6 past members.
--
-- The crossing pair is the load-bearing fixture: relation 205 belongs to
-- forum user 150, while forum user 205 belongs to relation 310. A by-id
-- lookup of "205" returns different members depending on which key the
-- query uses — the frozen semantic of the by-id profile route resolves
-- against relation_id.
INSERT INTO xf_nf_rosters_user
  (relation_id, roster_id, user_id, username, position_id, secondary_position_ids,
   rank_id, bio, uniform_date, added_date, custom_fields) VALUES
  (  1, 1, 100, 'Trooper.A',    11, '20', 5, '', 1700000000, 1500000000,
     '{"realName":"Alpha Smith","joinDate":"2015-03-01","promoDate":"2023-05-01","mos":"11B","consoleGamertag":"AlphaGamer"}'),
  (105, 1, 105, 'Trooper.B',    10, '',  24, '', 0, 1600000000,
     '{"realName":"Bravo Jones","joinDate":"2019-07-15","promoDate":"2022-01-10","mos":"11B","consoleGamertag":""}'),
  (205, 1, 150, 'Trooper.C',    10, '',  24, '', 0, 1650000000,
     '{"realName":"Charlie Brown","joinDate":"2020-02-20","promoDate":"2024-03-12","mos":"68W","consoleGamertag":"CharlieZulu"}'),
  (310, 1, 205, 'Trooper.D',    30, '',  27, '', 0, 1690000000,
     '{"realName":"Delta Green","joinDate":"2023-11-05","promoDate":"","mos":"","consoleGamertag":""}'),
  (320, 2, 300, 'Reservist.E',  40, '',  15, '', 0, 1620000000,
     '{"realName":"Echo White","joinDate":"2018-06-30","promoDate":"2021-08-19","mos":"11B","consoleGamertag":""}'),
  (330, 6, 301, 'Discharged.F', 10, '',  27, '', 0, 1550000000,
     '{"realName":"Foxtrot Black","joinDate":"2016-01-12","promoDate":"2017-02-28","mos":"11B","consoleGamertag":""}');

INSERT INTO xf_nf_rosters_service_record
  (record_id, relation_id, details, record_date, citation_date, record_type_id) VALUES
  (1001,   1, 'Promoted to Sergeant Major',    1683000000, 1683000000, 1),
  (1002,   1, 'Assigned to Squad Leader',      1684000000, 1684000000, 2),
  (1003, 205, 'Graduated basic training',      1582000000, 1582000000, 5),
  (1004, 205, 'Promoted to Specialist',        1710000000, 1710000000, 1),
  (1005, 310, 'Enlisted',                      1699000000, 1699000000, 5),
  (1006, 320, 'Transferred to reserve',        1630000000, 1630000000, 2),
  (1007, 330, 'Honorably discharged',          1556000000, 1556000000, 3);

INSERT INTO xf_nf_rosters_award (award_id, title, award_image, award_group_id, display_order) VALUES
  (1, 'Good Conduct Medal', 1, 1, 10),
  (2, 'Purple Heart',       2, 1, 20),
  (3, 'World War I Victory Medal', 3, 2, 30);

INSERT INTO xf_nf_rosters_user_award
  (record_id, relation_id, award_id, from_user_id, details, award_date, citation_date) VALUES
  (2001,   1, 1, 401, 'For exemplary conduct',     1685000000, 1685000000),
  (2002,   1, 2, 401, 'Wounded in action',         1686000000, 1686000000),
  (2003, 205, 1, 401, 'For exemplary conduct',     1711000000, 1711000000),
  (2004, 320, 3, 401, 'Campaign participation',    1632000000, 1632000000);

INSERT INTO xf_user_connected_account (user_id, provider, provider_key, extra_data) VALUES
  (100, 'nfDiscord', '111111111111111111', ''),
  (100, 'keycloak',  '6f1c0000-aaaa-bbbb-cccc-000000000100', ''),
  (150, 'nfDiscord', '222222222222222222', ''),
  (150, 'keycloak',  '6f1c0000-aaaa-bbbb-cccc-000000000150', ''),
  (205, 'nfDiscord', '333333333333333333', '');

INSERT INTO xf_nf_rosters_field_value (relation_id, field_id, field_value) VALUES
  (  1, 'consoleGamertag', 'AlphaGamer'),
  (205, 'consoleGamertag', 'CharlieZulu');

-- ---------------------------------------------------------------------
-- Forum posts
-- ---------------------------------------------------------------------
-- Bulk volume for the hot last-post aggregation
-- (SELECT user_id, MAX(post_date) FROM xf_post GROUP BY user_id):
-- 20k posts over 40 distinct posters makes the loose-index-scan vs
-- full-scan distinction observable in EXPLAIN. Generated through
-- MariaDB's sequence engine, deterministic by seq.
INSERT INTO xf_post
  (post_id, thread_id, user_id, username, post_date, message, position,
   type_data, reaction_users, vote_score)
SELECT
  seq,
  1 + (seq MOD 200),
  100 + (seq MOD 40),
  CONCAT('poster.', 100 + (seq MOD 40)),
  1500000000 + seq * 60,
  CONCAT('post body ', seq),
  seq MOD 50,
  '', '', 0
FROM seq_1_to_20000;

-- Targeted posts for roster members outside the bulk poster pool:
-- Trooper.C (150) and Trooper.D (205) post recently; Reservist.E (300)
-- last posted long ago (AWOL-shaped); Discharged.F (301) never posted
-- (left-join NULL case). The "recent" rows are seeded RELATIVE to the
-- wall clock because FindAwol computes its cutoff from now-7d — fixed
-- epochs would silently go stale and break the recent-vs-AWOL contrast
-- (pinned by TestFixtures_AwolContrastHoldsRelativeToNow). The ancient
-- row stays fixed: its distance from any future "now" only grows.
INSERT INTO xf_post
  (post_id, thread_id, user_id, username, post_date, message, position,
   type_data, reaction_users, vote_score) VALUES
  (20001, 1, 150, 'Trooper.C',   UNIX_TIMESTAMP() - 7200, 'recent post', 0, '', '', 0),
  (20002, 1, 150, 'Trooper.C',   UNIX_TIMESTAMP() - 3600, 'most recent post', 1, '', '', 0),
  (20003, 1, 205, 'Trooper.D',   UNIX_TIMESTAMP() - 1800, 'recent post', 2, '', '', 0),
  (20004, 2, 300, 'Reservist.E', 1600000000, 'ancient post', 0, '', '', 0);

-- ---------------------------------------------------------------------
-- Tickets (NF Tickets)
-- ---------------------------------------------------------------------

-- Nested-set category tree: Admin Office (1) contains Recruiting (2);
-- Tech Support (3) is a sibling root. Subtree expansion of [1] must
-- yield [1, 2].
INSERT INTO xf_nf_tickets_category
  (ticket_category_id, title, description, parent_category_id, depth, lft, rgt,
   display_order, ticket_count, last_ticket_title, breadcrumb_data, field_cache,
   category_emails, prefix_cache, notify_emails) VALUES
  (1, 'Admin Office',  'General admin requests', 0, 0, 1, 4, 10, 3, '', '', '', '', '', ''),
  (2, 'Recruiting',    'Enlistment paperwork',   1, 1, 2, 3, 10, 2, '', '', '', '', '', ''),
  (3, 'Tech Support',  'TeamSpeak and game tech',0, 0, 5, 6, 20, 2, '', '', '', '', '', '');

-- Phrases backing the reference cache: status / priority / prefix names.
INSERT INTO xf_phrase (language_id, title, phrase_text) VALUES
  (0, 'nf_tickets_ticket_status.1',   'Awaiting Support'),
  (0, 'nf_tickets_ticket_status.2',   'In Progress'),
  (0, 'nf_tickets_ticket_status.3',   'Closed'),
  (0, 'nf_tickets_ticket_priority.1', 'Low'),
  (0, 'nf_tickets_ticket_priority.2', 'Normal'),
  (0, 'nf_tickets_ticket_priority.3', 'High'),
  (0, 'nf_tickets_ticket_prefix.1',   'Urgent'),
  (0, 'nf_tickets_ticket_status_no_id', 'edge: no trailing id');

-- Tickets span categories 1/2/3, statuses 1/2/3, all three states, and
-- include one deleted (hidden) ticket. last_modified_date descends
-- (non-strictly) with ticket_id ascending so cursor pagination is
-- exercised against a deterministic order; tickets 6 and 7 deliberately
-- share a value to exercise the tuple-comparison tie-break (pinned by
-- TestFixtures_TicketCursorOrderingInvariant).
INSERT INTO xf_nf_tickets_ticket
  (ticket_id, ticket_ref, title, user_id, username, user_name, user_email, password,
   start_date, first_message_id, first_message_date, priority, status_id, ticket_state,
   discussion_state, assigned_user_id, assigned_username, ticket_category_id,
   last_message_id, last_message_date, last_message_user_id, last_message_username,
   last_modified_date, reply_count, prefix_id, custom_fields,
   starter_user_id, starter_username) VALUES
  (1, 'AA-0001', 'Cannot access milpacs',  400, 'TicketGuy.G', '', '', '',
   1740000000, 3001, 1740000000, 2, 1, 'open',
   'visible', 401, 'Helpdesk.H', 1,
   3003, 1740001200, 401, 'Helpdesk.H',
   1740700000, 2, 0, '', 400, 'TicketGuy.G'),
  (2, 'RE-0002', 'Enlistment paperwork',   100, 'Trooper.A', '', '', '',
   1740100000, 3004, 1740100000, 1, 2, 'pending',
   'visible', 401, 'Helpdesk.H', 2,
   3004, 1740100000, 100, 'Trooper.A',
   1740600000, 0, 1, '', 100, 'Trooper.A'),
  (3, 'TS-0003', 'TeamSpeak unreachable',  150, 'Trooper.C', '', '', '',
   1740200000, 3005, 1740200000, 3, 1, 'open',
   'visible', 0, '', 3,
   3005, 1740200000, 150, 'Trooper.C',
   1740500000, 0, 0, '', 150, 'Trooper.C'),
  (4, 'AA-0004', 'Discharge request',      300, 'Reservist.E', '', '', '',
   1740300000, 3006, 1740300000, 2, 3, 'resolved',
   'visible', 401, 'Helpdesk.H', 1,
   3007, 1740310000, 401, 'Helpdesk.H',
   1740400000, 1, 0, '', 300, 'Reservist.E'),
  (5, 'TS-0005', 'Spam ticket',            205, 'Trooper.D', '', '', '',
   1740350000, 3008, 1740350000, 1, 1, 'open',
   'deleted', 0, '', 3,
   3008, 1740350000, 205, 'Trooper.D',
   1740300000, 0, 0, '', 205, 'Trooper.D'),
  (6, 'RE-0006', 'Transfer request',       105, 'Trooper.B', '', '', '',
   1740360000, 3009, 1740360000, 2, 2, 'pending',
   'visible', 401, 'Helpdesk.H', 2,
   3009, 1740360000, 105, 'Trooper.B',
   1740200000, 0, 0, '', 105, 'Trooper.B'),
  (7, 'AA-0007', 'Award citation query',   301, 'Discharged.F', '', '', '',
   1740370000, 3010, 1740370000, 1, 3, 'resolved',
   'visible', 401, 'Helpdesk.H', 1,
   3010, 1740370000, 301, 'Discharged.F',
   1740200000, 0, 1, '', 301, 'Discharged.F');

-- Messages for ticket 1 include a hidden reply between two visible ones
-- (positions stay dense and unique per ticket); single openers for the
-- other conversational fixtures.
INSERT INTO xf_nf_tickets_message
  (message_id, ticket_id, user_id, username, user_name, user_email,
   message_date, message, message_state, position, reaction_users) VALUES
  (3001, 1, 400, 'TicketGuy.G', '', '', 1740000000, 'I cannot see my milpacs page.',  'visible', 0, ''),
  (3002, 1, 401, 'Helpdesk.H',  '', '', 1740000600, '(internal note: checking logs)', 'hidden',  1, ''),
  (3003, 1, 401, 'Helpdesk.H',  '', '', 1740001200, 'Fixed, please retry.',           'visible', 2, ''),
  (3004, 2, 100, 'Trooper.A',   '', '', 1740100000, 'Enlistment forms attached.',     'visible', 0, ''),
  (3005, 3, 150, 'Trooper.C',   '', '', 1740200000, 'TS3 timing out since patch.',    'visible', 0, ''),
  (3006, 4, 300, 'Reservist.E', '', '', 1740300000, 'Requesting discharge.',          'visible', 0, ''),
  (3007, 4, 401, 'Helpdesk.H',  '', '', 1740310000, 'Processed. o7',                  'visible', 1, ''),
  (3008, 5, 205, 'Trooper.D',   '', '', 1740350000, 'buy gold now',                   'visible', 0, ''),
  (3009, 6, 105, 'Trooper.B',   '', '', 1740360000, 'Requesting transfer to Bravo.',  'visible', 0, ''),
  (3010, 7, 301, 'Discharged.F','', '', 1740370000, 'Where is my citation?',          'visible', 0, '');

INSERT INTO xf_nf_tickets_ticket_participant (ticket_id, user_id, last_read_date) VALUES
  (1, 400, 1740001200),
  (1, 401, 1740001200),
  (2, 100, 1740100000),
  (4, 300, 1740310000),
  (4, 401, 1740310000);

INSERT INTO xf_nf_tickets_ticket_field_value (ticket_id, field_id, field_value) VALUES
  (1, 'discordId', '111111111111111111'),
  (2, 'milpacId',  '1'),
  (3, 'gameServer', 'arma3-tac1');

-- ---------------------------------------------------------------------
-- API keys (Cav7 ApiKeyManager)
-- ---------------------------------------------------------------------
-- Raw key material is the testdb package's public contract
-- (testdb.ActiveAPIKey / testdb.RevokedAPIKey); only hashes are stored,
-- matching the datastore's UNHEX(SHA2(?, 256)) lookup. Scope 3 is an
-- inactive scope attached to the active key: it must NOT surface.
INSERT INTO xf_cav7_api_key_scope_def
  (scope_id, scope_name, title, description, is_active) VALUES
  (1, 'read',         'Read',         'Read milpacs data',  1),
  (2, 'read:tickets', 'Read tickets', 'Read tickets data',  1),
  (3, 'admin',        'Admin',        'Retired scope',      0);

INSERT INTO xf_cav7_api_key
  (key_id, user_id, key_hash, key_prefix, is_active, created_date) VALUES
  (1, 401, UNHEX(SHA2('cav7_harness_active', 256)),  'cav7_harness', 1, 1740000000),
  (2, 400, UNHEX(SHA2('cav7_harness_revoked', 256)), 'cav7_harness', 0, 1740000000);

INSERT INTO xf_cav7_api_key_scope (key_id, scope_id) VALUES
  (1, 1),
  (1, 2),
  (1, 3),
  (2, 1);
