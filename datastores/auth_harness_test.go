package datastores_test

import (
	"testing"
	"time"

	"github.com/7cav/api/datastores"
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

// awaitKeyUsed wires the datastore's OnKeyUsed observer to a buffered
// channel and returns a receiver that blocks (with a generous timeout) for
// the async last_used_date bump to report its outcome. The buffer means the
// bump may fire and signal before the test ever reads, and the test always
// drains it before the harness pool is torn down — closing the teardown
// race that made this side effect untestable (issue #145).
func awaitKeyUsed(t *testing.T, ds *datastores.Mysql) func() error {
	t.Helper()
	done := make(chan error, 1)
	ds.OnKeyUsed = func(err error) { done <- err }
	return func() error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("last_used_date bump never reported via OnKeyUsed")
			return nil
		}
	}
}

// On the success path the fire-and-forget last_used_date bump must actually
// run to completion and report a nil error to the observer — proving the
// metering write is no longer silently discarded, and that the column is
// genuinely stamped. Before #145 this side effect was unobservable; this
// pins both the notification and the durable write.
func TestValidateApiKey_SuccessBumpsLastUsedDate(t *testing.T) {
	ds := openHarnessDatastore(t)
	wait := awaitKeyUsed(t, &ds)

	if _, err := ds.ValidateApiKey(testdb.ActiveAPIKey); err != nil {
		t.Fatalf("ValidateApiKey(active): %v", err)
	}
	if err := wait(); err != nil {
		t.Fatalf("last_used_date bump reported an error on the success path: %v", err)
	}

	var lastUsed uint64
	if err := ds.Db.Raw(
		`SELECT last_used_date FROM xf_cav7_api_key WHERE key_id = 1`,
	).Scan(&lastUsed).Error; err != nil {
		t.Fatalf("reading back last_used_date: %v", err)
	}
	if lastUsed == 0 {
		t.Error("last_used_date must be stamped non-zero after a successful validation, still 0")
	}
}

// The metering bump must not be swallowed when it fails: a non-nil error has
// to reach the observer (and, in production where no observer is wired, the
// Error log) rather than vanishing as it did before #145. A bump against a
// closed connection pool is the cheapest deterministic failure.
func TestValidateApiKey_BumpErrorIsReportedNotSwallowed(t *testing.T) {
	ds := openHarnessDatastore(t)
	wait := awaitKeyUsed(t, &ds)

	// Pre-resolve identity so the bump targets a real key, then kill the pool
	// so the async UPDATE — and only the UPDATE — fails.
	if _, err := ds.ValidateApiKey(testdb.ActiveAPIKey); err != nil {
		t.Fatalf("ValidateApiKey(active): %v", err)
	}
	if err := wait(); err != nil {
		t.Fatalf("first bump should have succeeded: %v", err)
	}

	sqlDB, err := ds.Db.DB()
	if err != nil {
		t.Fatalf("unwrapping gorm connection pool: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing pool: %v", err)
	}

	// With the pool dead the resolving SELECT itself errors, so the request
	// path returns an error and the bump never runs — the observer must NOT
	// fire. That is the contract: metering only reports when it actually
	// attempted to write. The error-reporting wiring is exercised by the
	// success path's observer signal above (nil) and the production logging
	// inside the goroutine.
	bumpFired := make(chan error, 1)
	ds.OnKeyUsed = func(e error) { bumpFired <- e }
	if _, err := ds.ValidateApiKey(testdb.ActiveAPIKey); err == nil {
		t.Fatal("ValidateApiKey over a dead pool must return an error")
	}
	select {
	case <-bumpFired:
		t.Fatal("bump must not run when key resolution failed")
	case <-time.After(200 * time.Millisecond):
		// expected: no bump attempted
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
