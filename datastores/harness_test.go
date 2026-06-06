// Behavior tests for the Datastore implementation, running against the
// dockerized MariaDB harness (testdb/). These replace the retired
// sqlmock pattern — which asserted the implementation's own SQL back at
// itself — with calls through the public Datastore methods against
// realistic forum-shaped fixtures, asserting returned domain data only
// (issue #120, PRD #112 "SQL seam").
//
// The tests skip when TESTDB_ADDR is unset (see testdb.Open); run them
// via `make test-integration`. CI runs them against the MariaDB service
// container with a no-silent-skip guard (.github/workflows/go.yml).
package datastores_test

import (
	"context"
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/referencecache"
	"github.com/7cav/api/testdb"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openHarnessDatastore dials a fresh, seeded harness database through
// gorm — the exact stack production uses — and returns the datastore
// under test. Each call gets its own disposable database (testdb.Open),
// so tests may not worry about each other's writes.
func openHarnessDatastore(t *testing.T) datastores.Mysql {
	t.Helper()
	_, dsn := testdb.Open(t)

	gormDB, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("dialing harness database through gorm: %v", err)
	}
	// Close gorm's pool on cleanup: each test opens its own pool, and the
	// stranded idle connections of a full run otherwise pile up against
	// MariaDB's default max_connections (151).
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("unwrapping gorm connection pool: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return datastores.Mysql{Db: gormDB}
}

// openTicketsHarness additionally builds the REAL reference cache from
// the datastore's own Loader implementation (phrase map + nested-set
// category tree loaded from the seeded tables), so ticket tests
// exercise name resolution and subtree expansion end-to-end instead of
// against a hand-rolled fake.
func openTicketsHarness(t *testing.T) (*datastores.Mysql, referencecache.ReferenceCache) {
	t.Helper()
	ds := openHarnessDatastore(t)
	rc := referencecache.New(&ds)
	if err := rc.Refresh(context.Background()); err != nil {
		t.Fatalf("refreshing reference cache from harness fixtures: %v", err)
	}
	return &ds, rc
}
