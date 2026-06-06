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
