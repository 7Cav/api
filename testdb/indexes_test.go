package testdb_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/7cav/api/testdb"
)

// applyIndexDDL runs the in-repo index script against a disposable
// harness database, exactly as the DB admin runs it against production
// (mysql xenforo < testdb/indexes.sql).
func applyIndexDDL(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(testdb.IndexDDL); err != nil {
		t.Fatalf("applying index script: %v", err)
	}
}

// looseScan is what MariaDB's EXPLAIN Extra column reports when the
// GROUP BY aggregation runs as a loose index scan — the PRD #112
// keystone (post-date aggregation 4,226ms -> 28ms on the mirror).
const looseScan = "Using index for group-by"

// The PRD #112 index script must flip the hot last-post aggregation
// from a full scan to a loose index scan. Red half: the unindexed
// harness schema must NOT already plan a loose scan (keeps the red
// plan reproducible). Green half: after applying the in-repo script,
// the very same query must plan one — a future query or schema change
// that silently reintroduces the full scan fails here instead of a
// production latency budget.
func TestIndexDDL_HotAggregationFlipsToLooseIndexScan(t *testing.T) {
	db, _ := testdb.Open(t)

	if extra := groupByOptimization(t, db); strings.Contains(extra, looseScan) {
		t.Fatalf("red plan not reproducible: unindexed schema already plans a loose index scan (Extra=%q)", extra)
	}

	applyIndexDDL(t, db)

	if extra := groupByOptimization(t, db); !strings.Contains(extra, looseScan) {
		t.Errorf("after the index script the hot aggregation must plan a loose index scan, got Extra=%q", extra)
	}
}

// preloadLookups are the relation-shaped lookups the datastore issues
// when hydrating profiles: gorm preloads service records and awards by
// relation_id, and resolves roster members by forum user_id (e.g. the
// last-post-date map). Each must go index-backed once the script runs.
var preloadLookups = []struct{ label, query, index string }{
	{"service records", `SELECT * FROM xf_nf_rosters_service_record WHERE relation_id IN (201, 205)`, "idx_relation_id"},
	{"awards", `SELECT * FROM xf_nf_rosters_user_award WHERE relation_id IN (201, 205)`, "idx_relation_id"},
	{"roster members by user id", `SELECT * FROM xf_nf_rosters_user WHERE user_id IN (100, 150)`, "idx_user_id"},
}

// The relation-id preloads must flip from full table scans (type=ALL,
// no usable key) to index-backed lookups on the script's indexes.
func TestIndexDDL_RelationPreloadsGoIndexBacked(t *testing.T) {
	db, _ := testdb.Open(t)

	for _, l := range preloadLookups {
		row := explainFirstRow(t, db, l.query)
		if row["type"] != "ALL" {
			t.Errorf("red plan not reproducible: %s lookup should full-scan on the unindexed schema, got type=%q key=%q", l.label, row["type"], row["key"])
		}
	}

	applyIndexDDL(t, db)

	for _, l := range preloadLookups {
		row := explainFirstRow(t, db, l.query)
		if row["key"] != l.index {
			t.Errorf("after the index script the %s lookup must use %s, got type=%q key=%q", l.label, l.index, row["type"], row["key"])
		}
	}
}

// explainFirstRow returns the first EXPLAIN row of the query as a
// column-name -> value map (NULLs become empty strings).
func explainFirstRow(t *testing.T, db *sql.DB, query string) map[string]string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN ` + query)
	if err != nil {
		t.Fatalf("explaining %q: %v", query, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("reading EXPLAIN columns: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("EXPLAIN returned no rows for %q", query)
	}
	vals := make([]sql.NullString, len(cols))
	scan := make([]any, len(cols))
	for i := range vals {
		scan[i] = &vals[i]
	}
	if err := rows.Scan(scan...); err != nil {
		t.Fatalf("scanning EXPLAIN row: %v", err)
	}
	row := make(map[string]string, len(cols))
	for i, c := range cols {
		row[c] = vals[i].String
	}
	return row
}
