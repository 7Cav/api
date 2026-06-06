-- PRD #112 ("Goodbye gRPC") Phase 1 index script — SOURCE OF TRUTH.
--
-- Four indexes backing the API's hot read paths (mirror-measured):
--   - xf_post user_id_post_date: flips the last-post aggregation
--     (SELECT user_id, MAX(post_date) ... GROUP BY user_id) to a loose
--     index scan — 4,226ms -> 28ms; AWOL report 4,211ms -> 198ms.
--   - idx_relation_id on xf_nf_rosters_service_record and
--     xf_nf_rosters_user_award: index-backs the profile preloads
--     (WHERE relation_id IN (...)) — 44ms -> ~0ms.
--   - idx_user_id on xf_nf_rosters_user: index-backs roster member
--     lookups by forum user id.
--
-- The API never executes DDL. This script is applied in exactly two
-- ways:
--   1. By the EXPLAIN-plan tests in testdb/indexes_test.go, against
--      their own disposable harness databases.
--   2. Manually by the DB admin against production (human-gated,
--      tracked in issue #122):
--
--        mysql xenforo < testdb/indexes.sql
--
-- Re-apply procedure: a forum add-on upgrade that rebuilds any of
-- these tables silently drops the indexes. Restoring them is the same
-- one command — the script is idempotent (ADD INDEX IF NOT EXISTS), so
-- re-running it against a database that already carries some or all of
-- the indexes is safe and changes nothing. Long-term (per PRD #112)
-- the re-application belongs in the ApiKeyManager add-on's schema
-- step, so forum upgrades restore the indexes automatically; that is
-- deliberately NOT implemented here — until then, re-apply manually
-- after any add-on upgrade that touches xf_post or the rosters tables.
--
-- Do NOT fold these into testdb/schema.sql: the harness schema stays
-- unindexed so the red EXPLAIN plans remain reproducible.

ALTER TABLE xf_post ADD INDEX IF NOT EXISTS user_id_post_date (user_id, post_date);
ALTER TABLE xf_nf_rosters_service_record ADD INDEX IF NOT EXISTS idx_relation_id (relation_id);
ALTER TABLE xf_nf_rosters_user_award ADD INDEX IF NOT EXISTS idx_relation_id (relation_id);
ALTER TABLE xf_nf_rosters_user ADD INDEX IF NOT EXISTS idx_user_id (user_id);
