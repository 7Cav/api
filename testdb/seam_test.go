package testdb_test

import (
	"testing"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/testdb"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The harness is the SQL seam for the datastore test families. This
// smoke runs the REAL datastore code path end-to-end against the seeded
// schema, proving the schema's tables, columns and joins line up with
// what the application actually queries — including the frozen by-id
// semantic: profile id 205 resolves against the milpac relation key and
// returns forum user 150, not forum user 205.
func TestSeam_DatastoreResolvesProfileByRelationKey(t *testing.T) {
	_, dsn := testdb.Open(t)

	gormDB, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("dialing harness database through gorm: %v", err)
	}
	ds := datastores.Mysql{Db: gormDB}

	profiles, err := ds.FindProfilesById(205)
	if err != nil {
		t.Fatalf("FindProfilesById(205): %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one profile, got %d", len(profiles))
	}

	got := profiles[0]
	if got.User.UserId != 150 {
		t.Errorf("profile id 205 must resolve via relation key to forum user 150, got %d", got.User.UserId)
	}
	if got.User.Username != "Trooper.C" {
		t.Errorf("expected username Trooper.C, got %q", got.User.Username)
	}
	if got.RealName != "Charlie Brown" {
		t.Errorf("expected custom-field real name to unmarshal, got %q", got.RealName)
	}
	if len(got.Records) == 0 {
		t.Error("expected preloaded service records")
	}
	if len(got.Awards) == 0 {
		t.Error("expected preloaded awards")
	}
	if got.DiscordId == "" {
		t.Error("expected connected-account discord id")
	}
}
