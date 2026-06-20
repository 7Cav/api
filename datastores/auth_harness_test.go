package datastores_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/testdb"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
// drains it before the harness pool is torn down — narrowing the teardown
// race that made this side effect awkward to test (issue #145) to a window
// the test controls.
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
//
// The fixture seeds key_id 1 with last_used_date = 0 (DEFAULT 0, not listed
// in the INSERT), so a bare non-zero readback would already pass without a
// bump only because of that seed. To stay non-vacuous regardless of the
// fixture's seed value, this captures a lower bound from the server clock
// before validating and asserts the column advanced into a recent window —
// it has to be a fresh stamp, not a pre-existing value.
func TestValidateApiKey_SuccessBumpsLastUsedDate(t *testing.T) {
	ds := openHarnessDatastore(t)
	wait := awaitKeyUsed(t, &ds)

	// Lower bound from the same clock the bump uses (UNIX_TIMESTAMP() on the
	// server), captured before the bump runs.
	var before uint64
	if err := ds.Db.Raw(`SELECT UNIX_TIMESTAMP()`).Scan(&before).Error; err != nil {
		t.Fatalf("reading server clock lower bound: %v", err)
	}

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
	if lastUsed < before {
		t.Errorf("last_used_date = %d, must have advanced to at least the pre-validation clock %d (a fresh stamp, not the seeded 0)", lastUsed, before)
	}
}

// The headline regression #145 guards: when the bump's UPDATE itself fails —
// key resolution having SUCCEEDED — the error must NOT be swallowed. It has
// to reach the observer AND the production Error log, rather than vanishing
// the way the old fire-and-forget `go ds.Db.Exec(...)` discarded it.
//
// To fail ONLY the UPDATE (and not resolution), the test drops the
// last_used_date column after a known-good validation. The resolving SELECT
// reads k.key_id, k.user_id, sd.scope_name and never touches last_used_date,
// so resolution still succeeds; the `SET last_used_date = ?` UPDATE then
// fails with "unknown column". This is the deterministic equivalent of a
// schema/permission fault hitting only the metering write — exactly the swallow
// #145 fixed. If the production swallow were reintroduced, wait() would see a
// nil error and the log buffer would stay empty, and this test would fail.
func TestValidateApiKey_BumpUpdateFailureIsReportedNotSwallowed(t *testing.T) {
	ds := openHarnessDatastore(t)

	// Redirect the package Error logger to a buffer so we can assert the
	// production log branch (the one that fires when OnKeyUsed is nil in prod)
	// actually emits, with key_id attribution. Restore on cleanup.
	var logBuf bytes.Buffer
	prevOut := datastores.Error.Writer()
	datastores.Error.SetOutput(&logBuf)
	t.Cleanup(func() { datastores.Error.SetOutput(prevOut) })

	// First, a clean validation to confirm the key resolves and the bump
	// succeeds while the column still exists.
	wait := awaitKeyUsed(t, &ds)
	if _, err := ds.ValidateApiKey(testdb.ActiveAPIKey); err != nil {
		t.Fatalf("ValidateApiKey(active): %v", err)
	}
	if err := wait(); err != nil {
		t.Fatalf("first bump should have succeeded: %v", err)
	}

	// Now break ONLY the UPDATE's target column. Resolution does not select it,
	// so the next validation still resolves; the async UPDATE fails.
	if err := ds.Db.Exec(`ALTER TABLE xf_cav7_api_key DROP COLUMN last_used_date`).Error; err != nil {
		t.Fatalf("dropping last_used_date to fault only the UPDATE: %v", err)
	}

	wait = awaitKeyUsed(t, &ds)
	result, err := ds.ValidateApiKey(testdb.ActiveAPIKey)
	if err != nil {
		t.Fatalf("resolution must still succeed after dropping last_used_date (the SELECT does not read it): %v", err)
	}
	if result == nil {
		t.Fatal("active key must still validate after dropping last_used_date")
	}

	bumpErr := wait()
	if bumpErr == nil {
		t.Fatal("the bump UPDATE failed (unknown column) but the observer received a nil error — #145 swallow reintroduced")
	}

	// The production log branch must also have fired, attributing the key_id.
	logged := logBuf.String()
	if logged == "" {
		t.Fatal("a failed bump must be logged via datastores.Error, but the buffer is empty — log branch did not fire")
	}
	if want := fmt.Sprintf("key_id %d", result.KeyId); !bytes.Contains(logBuf.Bytes(), []byte(want)) {
		t.Errorf("Error log must attribute the failing key_id; log=%q, want substring %q", logged, want)
	}
}

// The companion contract: when key RESOLUTION fails, the bump goroutine never
// runs, so the observer must NOT fire. (This was the only failure case the
// original test covered, despite its name claiming to prove error reporting —
// that claim is now genuinely covered by
// TestValidateApiKey_BumpUpdateFailureIsReportedNotSwallowed above.) A dead
// pool makes the resolving SELECT error before the bump is ever scheduled.
func TestValidateApiKey_FailedKeyResolutionFiresNoBump(t *testing.T) {
	ds := openHarnessDatastore(t)

	sqlDB, err := ds.Db.DB()
	if err != nil {
		t.Fatalf("unwrapping gorm connection pool: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing pool: %v", err)
	}

	// With the pool dead the resolving SELECT itself errors, so the request
	// path returns an error and the bump never runs — the observer must NOT
	// fire. Metering only reports when it actually attempted to write.
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

// Production wires no observer (OnKeyUsed == nil). This pins the exact
// `if ds.OnKeyUsed != nil` branch the binary runs: with nil, validation must
// not panic, and the bump must still durably stamp last_used_date. Polled
// with a bound because, without an observer, there is no signal to await on.
func TestValidateApiKey_NilObserverStillBumps(t *testing.T) {
	_, dsn := testdb.Open(t)
	gormDB, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("dialing harness database through gorm: %v", err)
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		t.Fatalf("unwrapping gorm connection pool: %v", err)
	}
	// No idle barrier here: a deliberately nil OnKeyUsed gives no completion
	// signal, so the test polls the row instead and closes the pool only after
	// the stamp lands (or the poll deadline trips).
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("closing gorm pool: %v", err)
		}
	})

	// Production default: no observer.
	ds := datastores.Mysql{Db: gormDB}
	if ds.OnKeyUsed != nil {
		t.Fatal("OnKeyUsed must be nil to exercise the production branch")
	}

	var before uint64
	if err := gormDB.Raw(`SELECT UNIX_TIMESTAMP()`).Scan(&before).Error; err != nil {
		t.Fatalf("reading server clock lower bound: %v", err)
	}

	result, err := ds.ValidateApiKey(testdb.ActiveAPIKey) // must not panic
	if err != nil {
		t.Fatalf("ValidateApiKey(active): %v", err)
	}
	if result == nil {
		t.Fatal("active key must validate")
	}

	// Poll (bounded) for the async stamp, since there is no observer to await.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var lastUsed uint64
		if err := gormDB.Raw(
			`SELECT last_used_date FROM xf_cav7_api_key WHERE key_id = 1`,
		).Scan(&lastUsed).Error; err != nil {
			t.Fatalf("reading back last_used_date: %v", err)
		}
		if lastUsed >= before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("last_used_date never advanced past %d with a nil observer (still %d) — bump did not run", before, lastUsed)
		}
		time.Sleep(20 * time.Millisecond)
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
