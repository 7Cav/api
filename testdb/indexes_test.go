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
