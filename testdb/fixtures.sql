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
