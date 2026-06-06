package testdb_test

import (
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
