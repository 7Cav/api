// Package testdb is the dockerized MariaDB integration-test harness.
//
// It provides real-database seams for tests that need actual MariaDB
// behavior (EXPLAIN plans, optimizer choices, SQL semantics) instead of
// sqlmock. The schema is forum-shaped: it carries exactly the XenForo
// and add-on tables the API reads, cribbed from production DDL, with
// production's stock indexes — and deliberately WITHOUT the four indexes
// proposed by the "Goodbye gRPC" PRD (#112), so that "red" EXPLAIN plans
// stay reproducible:
//
//   - xf_post: no user_id_post_date composite
//   - xf_nf_rosters_service_record: no idx_relation_id
//   - xf_nf_rosters_user_award: no idx_relation_id
//   - xf_nf_rosters_user: no idx_user_id
//
// Tests opt in via the TESTDB_ADDR environment variable (host:port of a
// MariaDB server, e.g. 127.0.0.1:3310). When it is unset, Open skips the
// calling test, so a plain `go test ./...` stays green without docker.
// Use `make test-integration` to spin up the dockerized server and run
// the full suite against it.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"

	_ "embed"

	"github.com/go-sql-driver/mysql"
)

// rootPassword is the throwaway root password of the harness MariaDB
// server (set in testdb/compose.yaml and the CI service container). It
// guards nothing: the server only ever holds disposable fixture data.
const rootPassword = "harness"

// ActiveAPIKey is the raw bearer key seeded with active scopes
// ("read", "read:tickets"); its hash lives in xf_cav7_api_key.
// RevokedAPIKey is seeded inactive and must fail validation.
const (
	ActiveAPIKey  = "cav7_harness_active"
	RevokedAPIKey = "cav7_harness_revoked"
)

//go:embed schema.sql
var schemaSQL string

//go:embed fixtures.sql
var fixturesSQL string

// Open creates a fresh database on the harness MariaDB server, applies
// the forum-shaped schema and fixtures, and returns an open handle plus
// the DSN of the new database (for callers that dial their own
// connection, e.g. through gorm). The database is uniquely named per
// call, so tests may freely mutate it — including DDL such as CREATE
// INDEX — without affecting other tests. It is dropped on test cleanup.
//
// Skips the calling test when TESTDB_ADDR is unset; fails it when the
// variable is set but the server is unreachable or seeding fails.
func Open(t *testing.T) (*sql.DB, string) {
	t.Helper()

	addr := os.Getenv("TESTDB_ADDR")
	if addr == "" {
		t.Skip("TESTDB_ADDR not set; skipping MariaDB integration test (run `make test-integration`)")
	}

	name := "testdb_" + randomSuffix(t)

	admin, err := sql.Open("mysql", dsn(addr, ""))
	if err != nil {
		t.Fatalf("testdb: opening admin connection: %v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Fatalf("testdb: creating database %s on %s: %v", name, addr, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name)
		admin.Close()
	})

	dbDSN := dsn(addr, name)
	db, err := sql.Open("mysql", dbDSN)
	if err != nil {
		t.Fatalf("testdb: opening database %s: %v", name, err)
	}
	t.Cleanup(func() { db.Close() })

	for _, script := range []struct{ label, sql string }{
		{"schema", schemaSQL},
		{"fixtures", fixturesSQL},
	} {
		if _, err := db.Exec(script.sql); err != nil {
			t.Fatalf("testdb: applying %s: %v", script.label, err)
		}
	}

	return db, dbDSN
}

// dsn builds a go-sql-driver DSN for the harness server. multiStatements
// lets the embedded schema and fixture scripts run as single Exec calls.
func dsn(addr, database string) string {
	cfg := mysql.NewConfig()
	cfg.User = "root"
	cfg.Passwd = rootPassword
	cfg.Net = "tcp"
	cfg.Addr = addr
	cfg.DBName = database
	cfg.MultiStatements = true
	return cfg.FormatDSN()
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("testdb: generating database name: %v", err)
	}
	return hex.EncodeToString(b[:])
}
