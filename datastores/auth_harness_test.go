package datastores_test

import (
	"testing"

	"github.com/7cav/api/testdb"
)

// A known active key resolves to its key/user identity and exactly the
// ACTIVE scopes granted to it — the seeded inactive "admin" scope is
// attached to the key but must not surface.
func TestValidateApiKey_ActiveKeyResolvesActiveScopes(t *testing.T) {
	ds := openHarnessDatastore(t)

	result, err := ds.ValidateApiKey(testdb.ActiveAPIKey)
	if err != nil {
		t.Fatalf("ValidateApiKey(active): %v", err)
	}
	if result == nil {
		t.Fatal("active key must validate, got nil result")
	}

	if result.KeyId != 1 {
		t.Errorf("KeyId = %d, want 1", result.KeyId)
	}
	if result.UserId != 401 {
		t.Errorf("UserId = %d, want 401 (key owner)", result.UserId)
	}
	for _, scope := range []string{"read", "read:tickets"} {
		if !result.HasScope(scope) {
			t.Errorf("active key must carry scope %q", scope)
		}
	}
	if result.HasScope("admin") {
		t.Error("inactive scope 'admin' must not surface for the active key")
	}
	if result.HasScope("write:something") {
		t.Error("never-granted scope must not surface")
	}
}

// A revoked (is_active = 0) key must fail validation with a nil result
// and no error — the auth boundary then responds 401.
func TestValidateApiKey_RevokedKeyYieldsNil(t *testing.T) {
	ds := openHarnessDatastore(t)

	result, err := ds.ValidateApiKey(testdb.RevokedAPIKey)
	if err != nil {
		t.Fatalf("ValidateApiKey(revoked): %v", err)
	}
	if result != nil {
		t.Errorf("revoked key must yield nil result, got %+v", result)
	}
}

// A key that was never issued behaves exactly like a revoked one:
// nil result, no error.
func TestValidateApiKey_UnknownKeyYieldsNil(t *testing.T) {
	ds := openHarnessDatastore(t)

	result, err := ds.ValidateApiKey("cav7_never_issued")
	if err != nil {
		t.Fatalf("ValidateApiKey(unknown): %v", err)
	}
	if result != nil {
		t.Errorf("unknown key must yield nil result, got %+v", result)
	}
}

// A database failure must surface as a non-nil error, distinct from the
// (nil, nil) "key not found" outcome — the auth boundary turns the
// former into a 500 and the latter into a 401. Killing the underlying
// pool is the cheapest real DB failure.
func TestValidateApiKey_DatabaseErrorIsErrorNotUnauthenticated(t *testing.T) {
	ds := openHarnessDatastore(t)

	sqlDB, err := ds.Db.DB()
	if err != nil {
		t.Fatalf("unwrapping gorm connection pool: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing pool: %v", err)
	}

	result, err := ds.ValidateApiKey(testdb.ActiveAPIKey)
	if err == nil {
		t.Fatalf("ValidateApiKey over a dead pool must return an error (500 path), got (%+v, nil) — indistinguishable from an invalid key (401 path)", result)
	}
}
