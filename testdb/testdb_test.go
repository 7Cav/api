package testdb_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/7cav/api/testdb"
)

// Tracer: Open hands back a connection to a seeded, forum-shaped database.
func TestOpen_YieldsSeededDatabase(t *testing.T) {
	db, _ := testdb.Open(t)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xf_user`).Scan(&n); err != nil {
		t.Fatalf("querying xf_user: %v", err)
	}
	if n == 0 {
		t.Fatal("expected seeded xf_user rows, got none")
	}
}

// The by-id profile route resolves its id against the milpac relation
// key, not the forum user id. The fixtures must make the two seams
// distinguishable: a relation_id that exists as some OTHER member's
// user_id. Looking up "205" must yield different members depending on
// which key is used.
func TestFixtures_RelationKeyDiffersFromForumUserId(t *testing.T) {
	db, _ := testdb.Open(t)

	var byRelation, byUser uint64
	if err := db.QueryRow(
		`SELECT user_id FROM xf_nf_rosters_user WHERE relation_id = 205`,
	).Scan(&byRelation); err != nil {
		t.Fatalf("looking up member by relation_id 205: %v", err)
	}
	if err := db.QueryRow(
		`SELECT relation_id FROM xf_nf_rosters_user WHERE user_id = 205`,
	).Scan(&byUser); err != nil {
		t.Fatalf("looking up member by user_id 205: %v", err)
	}

	if byRelation == 205 {
		t.Error("member at relation_id 205 must have a different user_id, got 205")
	}
	if byUser == 205 {
		t.Error("member with user_id 205 must have a different relation_id, got 205")
	}
}

// Members must come with the relational payload the profile queries
// preload: rank, position (with group), service records, awards (with
// catalog row), connected accounts, and roster field values.
func TestFixtures_MemberRelationsResolve(t *testing.T) {
	db, _ := testdb.Open(t)

	var orphans int
	for _, q := range []struct{ label, sql string }{
		{"rank", `SELECT COUNT(*) FROM xf_nf_rosters_user m LEFT JOIN xf_nf_rosters_rank r ON r.rank_id = m.rank_id WHERE r.rank_id IS NULL`},
		{"position", `SELECT COUNT(*) FROM xf_nf_rosters_user m LEFT JOIN xf_nf_rosters_position p ON p.position_id = m.position_id WHERE p.position_id IS NULL`},
		{"position group", `SELECT COUNT(*) FROM xf_nf_rosters_position p LEFT JOIN xf_nf_rosters_position_group g ON g.position_group_id = p.position_group_id WHERE g.position_group_id IS NULL`},
		{"forum user", `SELECT COUNT(*) FROM xf_nf_rosters_user m LEFT JOIN xf_user u ON u.user_id = m.user_id WHERE u.user_id IS NULL`},
		{"award catalog", `SELECT COUNT(*) FROM xf_nf_rosters_user_award ua LEFT JOIN xf_nf_rosters_award a ON a.award_id = ua.award_id WHERE a.award_id IS NULL`},
	} {
		if err := db.QueryRow(q.sql).Scan(&orphans); err != nil {
			t.Fatalf("checking %s linkage: %v", q.label, err)
		}
		if orphans != 0 {
			t.Errorf("%d member rows with dangling %s reference", orphans, q.label)
		}
	}

	for _, q := range []struct{ label, sql string }{
		{"service records", `SELECT COUNT(*) FROM xf_nf_rosters_service_record`},
		{"awards", `SELECT COUNT(*) FROM xf_nf_rosters_user_award`},
		{"connected accounts", `SELECT COUNT(*) FROM xf_user_connected_account`},
		{"field values", `SELECT COUNT(*) FROM xf_nf_rosters_field_value`},
	} {
		var n int
		if err := db.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", q.label, err)
		}
		if n == 0 {
			t.Errorf("expected seeded %s, got none", q.label)
		}
	}
}

// API-key fixtures must support both sides of bearer auth: the
// well-known raw key resolves to active scopes through the same
// UNHEX(SHA2(...)) shape the datastore uses, and revoked/inactive
// material exists for the negative paths.
func TestFixtures_ApiKeysResolveScopes(t *testing.T) {
	db, _ := testdb.Open(t)

	rows, err := db.Query(
		`SELECT sd.scope_name
		 FROM   xf_cav7_api_key k
		 JOIN   xf_cav7_api_key_scope ks     ON ks.key_id   = k.key_id
		 JOIN   xf_cav7_api_key_scope_def sd ON sd.scope_id = ks.scope_id
		 WHERE  k.key_hash   = UNHEX(SHA2(?, 256))
		   AND  k.is_active  = 1
		   AND  sd.is_active = 1`, testdb.ActiveAPIKey)
	if err != nil {
		t.Fatalf("resolving scopes for active key: %v", err)
	}
	defer rows.Close()
	scopes := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scanning scope: %v", err)
		}
		scopes[s] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating scopes: %v", err)
	}
	if len(scopes) < 2 {
		t.Errorf("active key should carry at least 2 active scopes, got %v", scopes)
	}
	if scopes["admin"] {
		t.Error("inactive scope 'admin' must not surface for the active key")
	}

	for _, neg := range []struct{ label, sql string }{
		{"inactive key", `SELECT COUNT(*) FROM xf_cav7_api_key WHERE is_active = 0`},
		{"inactive scope def", `SELECT COUNT(*) FROM xf_cav7_api_key_scope_def WHERE is_active = 0`},
	} {
		var n int
		if err := db.QueryRow(neg.sql).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", neg.label, err)
		}
		if n == 0 {
			t.Errorf("expected at least one %s for negative-path tests", neg.label)
		}
	}
}

// testdb.RevokedAPIKey and the seeded inactive key row are duplicated
// raw strings (constant in testdb.go, literal in fixtures.sql). If they
// drift, downstream negative-path tests pass for the wrong reason: the
// key fails validation because it's unknown, not because it's revoked.
// Pin them together: the exported constant must hash to exactly one
// seeded INACTIVE row.
func TestFixtures_RevokedAPIKeyResolvesToInactiveRow(t *testing.T) {
	db, _ := testdb.Open(t)

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM xf_cav7_api_key
		 WHERE key_hash = UNHEX(SHA2(?, 256)) AND is_active = 0`,
		testdb.RevokedAPIKey,
	).Scan(&n); err != nil {
		t.Fatalf("resolving revoked key: %v", err)
	}
	if n != 1 {
		t.Errorf("testdb.RevokedAPIKey must hash to exactly one inactive seeded row, got %d (constant and fixtures.sql drifted?)", n)
	}
}

// Each Open call must yield its own database so tests can run DDL
// (e.g. CREATE INDEX for green-plan comparisons) without leaking into
// sibling tests.
func TestOpen_IsolatesDatabasesPerCall(t *testing.T) {
	db1, dsn1 := testdb.Open(t)
	db2, dsn2 := testdb.Open(t)

	if dsn1 == dsn2 {
		t.Fatalf("two Open calls returned the same DSN: %s", dsn1)
	}

	if _, err := db1.Exec(`CREATE INDEX idx_relation_id ON xf_nf_rosters_service_record (relation_id)`); err != nil {
		t.Fatalf("creating index in first database: %v", err)
	}

	var n int
	if err := db2.QueryRow(
		`SELECT COUNT(*) FROM information_schema.statistics
		 WHERE table_schema = DATABASE()
		   AND table_name = 'xf_nf_rosters_service_record'
		   AND index_name = 'idx_relation_id'`,
	).Scan(&n); err != nil {
		t.Fatalf("checking second database: %v", err)
	}
	if n != 0 {
		t.Error("DDL in one Open database leaked into another")
	}
}

// The harness schema must not pre-create the four indexes proposed by
// PRD #112 — their absence is what keeps the "red" EXPLAIN plans of the
// index slice reproducible.
func TestSchema_CarriesNoPRDIndexes(t *testing.T) {
	db, _ := testdb.Open(t)

	for _, idx := range []struct{ table, index string }{
		{"xf_post", "user_id_post_date"},
		{"xf_nf_rosters_service_record", "idx_relation_id"},
		{"xf_nf_rosters_user_award", "idx_relation_id"},
		{"xf_nf_rosters_user", "idx_user_id"},
	} {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM information_schema.statistics
			 WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?`,
			idx.table, idx.index,
		).Scan(&n)
		if err != nil {
			t.Fatalf("checking index %s.%s: %v", idx.table, idx.index, err)
		}
		if n != 0 {
			t.Errorf("PRD index %s on %s must not exist in the harness schema", idx.index, idx.table)
		}
	}
}

// hotLastPostAggregation is the subquery driving the lite-roster
// last-forum-post column and the AWOL report — the PRD's measured
// hotspot (4.2s -> 28ms once MariaDB can run it as a loose index scan).
const hotLastPostAggregation = `SELECT user_id, MAX(post_date) FROM xf_post GROUP BY user_id`

// groupByOptimization returns the Extra column of the EXPLAIN row for
// the hot aggregation, which is where MariaDB reports loose index scans
// ("Using index for group-by").
func groupByOptimization(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN ` + hotLastPostAggregation)
	if err != nil {
		t.Fatalf("explaining hot aggregation: %v", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading EXPLAIN columns: %v", err)
	}
	if !rows.Next() {
		t.Fatal("EXPLAIN returned no rows")
	}
	vals := make([]sql.NullString, len(cols))
	scan := make([]any, len(cols))
	for i := range vals {
		scan[i] = &vals[i]
	}
	if err := rows.Scan(scan...); err != nil {
		t.Fatalf("scanning EXPLAIN row: %v", err)
	}
	for i, c := range cols {
		if c == "Extra" {
			return vals[i].String
		}
	}
	t.Fatal("EXPLAIN output has no Extra column")
	return ""
}

// The post fixtures must be voluminous enough that the loose-index-scan
// vs full-scan distinction is observable: the seeded schema (no PRD
// composite) must NOT plan a loose index scan, and adding the composite
// in a throwaway database must flip the very same query to one.
func TestFixtures_HotAggregationRedPlanReproducible(t *testing.T) {
	db, _ := testdb.Open(t)

	var posts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xf_post`).Scan(&posts); err != nil {
		t.Fatalf("counting posts: %v", err)
	}
	if posts < 10000 {
		t.Errorf("expected at least 10000 seeded posts for meaningful plans, got %d", posts)
	}

	const looseScan = "Using index for group-by"

	if extra := groupByOptimization(t, db); strings.Contains(extra, looseScan) {
		t.Errorf("red plan not reproducible: hot aggregation already runs as loose index scan (Extra=%q)", extra)
	}

	// Sanity in this test's own disposable database: the PRD composite
	// flips the same query to a loose index scan, proving the fixture
	// volume makes the distinction meaningful.
	if _, err := db.Exec(`CREATE INDEX user_id_post_date ON xf_post (user_id, post_date)`); err != nil {
		t.Fatalf("creating PRD composite in disposable database: %v", err)
	}
	if extra := groupByOptimization(t, db); !strings.Contains(extra, looseScan) {
		t.Errorf("with the PRD composite the hot aggregation should plan a loose index scan, got Extra=%q", extra)
	}
}

// The AWOL report (datastores FindAwol) computes its cutoff from the
// wall clock (now - 7 days), so the fixtures' recent-vs-AWOL contrast
// must hold relative to NOW, not to a fixed epoch:
//   - Discharged.F (301) never posted (left-join NULL case),
//   - Reservist.E (300) last posted far beyond any plausible cutoff,
//   - Trooper.C (150) and Trooper.D (205) posted within the last day.
func TestFixtures_AwolContrastHoldsRelativeToNow(t *testing.T) {
	db, _ := testdb.Open(t)

	var neverPosted int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM xf_post WHERE user_id = 301`,
	).Scan(&neverPosted); err != nil {
		t.Fatalf("counting posts for never-posted member 301: %v", err)
	}
	if neverPosted != 0 {
		t.Errorf("member 301 must have zero posts (left-join NULL case), got %d", neverPosted)
	}

	var ancientOK bool
	if err := db.QueryRow(
		`SELECT MAX(post_date) < UNIX_TIMESTAMP() - 30*86400 FROM xf_post WHERE user_id = 300`,
	).Scan(&ancientOK); err != nil {
		t.Fatalf("checking ancient poster 300: %v", err)
	}
	if !ancientOK {
		t.Error("member 300's last post must predate any plausible AWOL cutoff (older than 30 days)")
	}

	for _, userID := range []int{150, 205} {
		var recentOK bool
		if err := db.QueryRow(
			`SELECT MAX(post_date) > UNIX_TIMESTAMP() - 86400 FROM xf_post WHERE user_id = ?`, userID,
		).Scan(&recentOK); err != nil {
			t.Fatalf("checking recent poster %d: %v", userID, err)
		}
		if !recentOK {
			t.Errorf("member %d's last post must be within the last day so FindAwol's now-7d cutoff never flags it", userID)
		}
	}
}

// Ticket fixtures must span categories, statuses and states so the
// tickets datastore tests (#120) can exercise every filter knob:
// multiple categories (with a parent/child pair for subtree expansion),
// multiple status_ids, all three ticket_states, and at least one
// non-visible ticket for the include_hidden switch.
func TestFixtures_TicketsSpanCategoriesAndStatuses(t *testing.T) {
	db, _ := testdb.Open(t)

	counts := map[string]string{
		"distinct ticket categories":   `SELECT COUNT(DISTINCT ticket_category_id) FROM xf_nf_tickets_ticket`,
		"distinct status ids":          `SELECT COUNT(DISTINCT status_id) FROM xf_nf_tickets_ticket`,
		"distinct ticket states":       `SELECT COUNT(DISTINCT ticket_state) FROM xf_nf_tickets_ticket`,
		"child categories (depth > 0)": `SELECT COUNT(*) FROM xf_nf_tickets_category WHERE parent_category_id > 0`,
	}
	for label, q := range counts {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", label, err)
		}
		if n < 2 && label != "child categories (depth > 0)" {
			t.Errorf("expected at least 2 %s, got %d", label, n)
		}
		if n < 1 {
			t.Errorf("expected at least 1 of %s, got %d", label, n)
		}
	}

	var hidden int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM xf_nf_tickets_ticket WHERE discussion_state <> 'visible'`,
	).Scan(&hidden); err != nil {
		t.Fatalf("counting hidden tickets: %v", err)
	}
	if hidden == 0 {
		t.Error("expected at least one non-visible ticket for the include_hidden switch")
	}

	// Nested-set integrity: every child sits inside its parent's lft/rgt
	// span — the reference cache's subtree expansion depends on it.
	var broken int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM xf_nf_tickets_category c
		 JOIN xf_nf_tickets_category p ON p.ticket_category_id = c.parent_category_id
		 WHERE c.lft <= p.lft OR c.rgt >= p.rgt`,
	).Scan(&broken); err != nil {
		t.Fatalf("checking nested-set integrity: %v", err)
	}
	if broken != 0 {
		t.Errorf("%d categories violate nested-set containment", broken)
	}
}

// Cursor pagination orders by (last_modified_date DESC, ticket_id) with
// a tuple comparison for the tie-break. The fixtures promise a
// deterministic shape for that: last_modified_date descends
// (non-strictly) as ticket_id ascends, with exactly one deliberate tie
// — tickets 6 and 7 — so the tie-break path is actually exercised.
func TestFixtures_TicketCursorOrderingInvariant(t *testing.T) {
	db, _ := testdb.Open(t)

	rows, err := db.Query(
		`SELECT ticket_id, last_modified_date FROM xf_nf_tickets_ticket ORDER BY ticket_id`,
	)
	if err != nil {
		t.Fatalf("reading tickets: %v", err)
	}
	defer rows.Close()

	type row struct{ id, modified uint64 }
	var tickets []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.modified); err != nil {
			t.Fatalf("scanning ticket: %v", err)
		}
		tickets = append(tickets, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating tickets: %v", err)
	}
	if len(tickets) < 2 {
		t.Fatalf("expected multiple seeded tickets, got %d", len(tickets))
	}

	var ties [][2]uint64
	for i := 1; i < len(tickets); i++ {
		prev, cur := tickets[i-1], tickets[i]
		if cur.modified > prev.modified {
			t.Errorf("last_modified_date must descend (non-strictly) as ticket_id ascends: ticket %d (%d) > ticket %d (%d)",
				cur.id, cur.modified, prev.id, prev.modified)
		}
		if cur.modified == prev.modified {
			ties = append(ties, [2]uint64{prev.id, cur.id})
		}
	}

	if len(ties) != 1 || ties[0] != [2]uint64{6, 7} {
		t.Errorf("expected exactly one last_modified_date tie, between tickets 6 and 7 (the tuple-comparison tie-break fixture), got %v", ties)
	}
}

// Every ticket must resolve its reference-cached names: status,
// priority and prefix phrases, plus its category row. Messages must
// include a hidden one (message_state filter), and participants and
// field values must be present.
func TestFixtures_TicketRelationsResolve(t *testing.T) {
	db, _ := testdb.Open(t)

	for _, q := range []struct{ label, sql string }{
		{"status phrase", `SELECT COUNT(*) FROM xf_nf_tickets_ticket tk LEFT JOIN xf_phrase ph ON ph.title = CONCAT('nf_tickets_ticket_status.', tk.status_id) WHERE ph.phrase_id IS NULL`},
		{"category row", `SELECT COUNT(*) FROM xf_nf_tickets_ticket tk LEFT JOIN xf_nf_tickets_category c ON c.ticket_category_id = tk.ticket_category_id WHERE c.ticket_category_id IS NULL`},
	} {
		var n int
		if err := db.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatalf("checking %s linkage: %v", q.label, err)
		}
		if n != 0 {
			t.Errorf("%d tickets with unresolvable %s", n, q.label)
		}
	}

	for _, q := range []struct{ label, sql string }{
		{"visible messages", `SELECT COUNT(*) FROM xf_nf_tickets_message WHERE message_state = 'visible'`},
		{"hidden messages", `SELECT COUNT(*) FROM xf_nf_tickets_message WHERE message_state <> 'visible'`},
		{"participants", `SELECT COUNT(*) FROM xf_nf_tickets_ticket_participant`},
		{"ticket field values", `SELECT COUNT(*) FROM xf_nf_tickets_ticket_field_value`},
		{"priority phrases", `SELECT COUNT(*) FROM xf_phrase WHERE title LIKE 'nf_tickets_ticket_priority.%'`},
		{"prefix phrases", `SELECT COUNT(*) FROM xf_phrase WHERE title LIKE 'nf_tickets_ticket_prefix.%'`},
	} {
		var n int
		if err := db.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", q.label, err)
		}
		if n == 0 {
			t.Errorf("expected seeded %s, got none", q.label)
		}
	}
}
