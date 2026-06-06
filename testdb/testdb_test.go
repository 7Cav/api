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
