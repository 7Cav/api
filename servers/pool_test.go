package servers

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// TestPoolConfig_Defaults pins the conservative baked-in defaults that apply
// when every DB_* pool override is unset (empty). These defaults are the fix
// for #204: a bounded, warm pool that clears a single-key burst in well under a
// second instead of stampeding the shared MariaDB into Error 1040. The numbers
// are justified against prod (max_connections=300, forum pm.max_children=30,
// all-time peak 68): 25 open + 25 idle keeps the pool warm, 30m lifetime
// recycles conns under wait_timeout. See poolConfig's doc comment and
// CONTEXT.md.
//
// An UNSET override (empty string) is a clean default, NOT a rejected one, so it
// must produce ZERO warnings — silence is the signal that the operator made no
// choice. (Invalid/clamped overrides warn; see the warning tests below.)
func TestPoolConfig_Defaults(t *testing.T) {
	got, warns := poolConfig("", "", "")

	require.Equal(t, defaultMaxOpenConns, got.MaxOpen, "unset DB_MAX_OPEN_CONNS must fall back to the default")
	require.Equal(t, defaultMaxIdleConns, got.MaxIdle, "unset DB_MAX_IDLE_CONNS must fall back to the default")
	require.Equal(t, defaultConnMaxLifetime, got.MaxLifetime, "unset DB_CONN_MAX_LIFETIME must fall back to the default")

	require.Empty(t, warns, "a clean unset must be SILENT — no warnings for the no-override case")

	// Acceptance criterion: the pool must be bounded (non-zero, finite).
	require.Greater(t, got.MaxOpen, 0, "MaxOpen must be a positive, finite bound — 0 means unbounded, the bug")
	require.Greater(t, got.MaxLifetime, time.Duration(0), "MaxLifetime must be finite")
}

// TestPoolConfig_Overrides pins that explicit, valid env values win over the
// defaults — and a fully valid override set is SILENT (the operator's choice is
// honored, so there is nothing to warn about).
func TestPoolConfig_Overrides(t *testing.T) {
	got, warns := poolConfig("40", "10", "5m")

	require.Equal(t, 40, got.MaxOpen)
	require.Equal(t, 10, got.MaxIdle)
	require.Equal(t, 5*time.Minute, got.MaxLifetime)
	require.Empty(t, warns, "valid overrides must be honored silently — no warnings")
}

// TestPoolConfig_InvalidLifetimeWarns pins that a garbage DB_CONN_MAX_LIFETIME
// (unparseable duration) falls back to the default rather than silently
// disabling recycling — AND that the rejection is announced via a warning so an
// operator doesn't believe a bad override is live.
func TestPoolConfig_InvalidLifetimeWarns(t *testing.T) {
	got, warns := poolConfig("", "", "not-a-duration")
	require.Equal(t, defaultConnMaxLifetime, got.MaxLifetime, "an unparseable DB_CONN_MAX_LIFETIME must fall back to the default")
	requireWarningMentioning(t, warns, "DB_CONN_MAX_LIFETIME", "not-a-duration")
}

// TestPoolConfig_NonPositiveLifetimeWarns pins that a parseable-but-non-positive
// duration (e.g. "0s", "-5m") is rejected with a warning and the default kept,
// rather than silently disabling recycling (lifetime <= 0 means "never expire"
// to database/sql).
func TestPoolConfig_NonPositiveLifetimeWarns(t *testing.T) {
	for _, raw := range []string{"0s", "-5m"} {
		got, warns := poolConfig("", "", raw)
		require.Equal(t, defaultConnMaxLifetime, got.MaxLifetime, "a non-positive DB_CONN_MAX_LIFETIME (%q) must fall back to the default", raw)
		requireWarningMentioning(t, warns, "DB_CONN_MAX_LIFETIME", raw)
	}
}

// TestPoolConfig_InvalidOpenWarns pins that a non-numeric DB_MAX_OPEN_CONNS
// ("banana") falls back to the default AND warns — the operator must not believe
// a typo'd bound is live while the pool actually runs the default.
func TestPoolConfig_InvalidOpenWarns(t *testing.T) {
	got, warns := poolConfig("banana", "", "")
	require.Equal(t, defaultMaxOpenConns, got.MaxOpen, "an unparseable DB_MAX_OPEN_CONNS must fall back to the default")
	requireWarningMentioning(t, warns, "DB_MAX_OPEN_CONNS", "banana")
}

// TestPoolConfig_NegativeOpenWarns pins that a nonsensical negative
// DB_MAX_OPEN_CONNS falls back to the default — a non-positive bound means
// "unbounded" to database/sql (SetMaxOpenConns(<=0) is unlimited), reopening the
// exact #204 hole — AND that the rejection is announced.
func TestPoolConfig_NegativeOpenWarns(t *testing.T) {
	got, warns := poolConfig("-5", "", "")
	require.Equal(t, defaultMaxOpenConns, got.MaxOpen, "a non-positive DB_MAX_OPEN_CONNS must fall back to the default, never 0/unbounded")
	requireWarningMentioning(t, warns, "DB_MAX_OPEN_CONNS", "-5")
}

// TestPoolConfig_InvalidIdleWarns pins that a non-numeric DB_MAX_IDLE_CONNS
// falls back to the warm default AND warns.
func TestPoolConfig_InvalidIdleWarns(t *testing.T) {
	got, warns := poolConfig("25", "banana", "")
	require.Equal(t, defaultMaxIdleConns, got.MaxIdle, "an unparseable DB_MAX_IDLE_CONNS must fall back to the warm default")
	requireWarningMentioning(t, warns, "DB_MAX_IDLE_CONNS", "banana")
}

// TestPoolConfig_NegativeIdleWarns pins that a negative DB_MAX_IDLE_CONNS falls
// back to the default (then clamped to open), keeping the pool warm rather than
// cold (idle 0 would pay connection-setup cost mid-burst, the root cause of the
// #204 latency climb) — AND that the rejection is announced.
func TestPoolConfig_NegativeIdleWarns(t *testing.T) {
	got, warns := poolConfig("25", "-1", "")
	require.Equal(t, defaultMaxIdleConns, got.MaxIdle, "a negative DB_MAX_IDLE_CONNS must fall back to the warm default")
	requireWarningMentioning(t, warns, "DB_MAX_IDLE_CONNS", "-1")
}

// TestPoolConfig_IdleClampedToOpenWarns pins that idle conns never exceed open
// conns: database/sql silently reduces idle to match max-open, but we make the
// intent explicit so an operator who sets idle > open gets the sane, documented
// behavior — AND a warning that their requested idle was clamped, rather than a
// surprising silent reduction.
func TestPoolConfig_IdleClampedToOpenWarns(t *testing.T) {
	got, warns := poolConfig("10", "50", "")
	require.Equal(t, 10, got.MaxIdle, "MaxIdle must be clamped down to MaxOpen — more idle than open is meaningless")
	requireWarningMentioning(t, warns, "DB_MAX_IDLE_CONNS", "50")
}

// TestPoolConfig_AppliesNonZeroFiniteMaxOpen is the acceptance-criterion test:
// after applying the resolved pool config to a real *sql.DB, the pool reports a
// non-zero, finite MaxOpenConnections (0 == unbounded, the #204 bug). sql.Open
// is lazy — it never dials — so this exercises the exact apply path
// setupDatasource uses (SetMaxOpenConns/SetMaxIdleConns/SetConnMaxLifetime on
// the underlying *sql.DB) WITHOUT opening a real database or calling os.Exit.
func TestPoolConfig_AppliesNonZeroFiniteMaxOpen(t *testing.T) {
	// Lazy handle to a host that is never contacted (Open does not connect).
	db, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/xenforo")
	require.NoError(t, err)
	defer db.Close()

	require.Equal(t, 0, db.Stats().MaxOpenConnections, "precondition: a fresh *sql.DB is unbounded (0) — exactly the #204 starting state")

	pool, _ := poolConfig("", "", "") // defaults
	db.SetMaxOpenConns(pool.MaxOpen)
	db.SetMaxIdleConns(pool.MaxIdle)
	db.SetConnMaxLifetime(pool.MaxLifetime)

	require.Equal(t, defaultMaxOpenConns, db.Stats().MaxOpenConnections, "after applying the default pool config the *sql.DB must report a non-zero, finite MaxOpenConnections")
	require.Greater(t, db.Stats().MaxOpenConnections, 0, "MaxOpenConnections must be a positive, finite bound — never 0/unbounded")
}

// TestPoolConfig_AppliesClampedIdle is the applied-path companion to the
// resolver-level clamp test: it asserts the CLAMPED idle bound survives the
// round-trip onto a real lazy *sql.DB. database/sql does not expose the
// configured MaxIdleConns directly, but MaxOpenConnections caps the effective
// idle bound — so applying a config where idle was clamped to open (idle==open)
// must leave the *sql.DB reporting MaxOpenConnections == the clamped value, with
// no idle setting able to exceed it. We assert the resolved clamp AND that the
// applied open bound equals it (the ceiling the *sql.DB enforces on idle).
func TestPoolConfig_AppliesClampedIdle(t *testing.T) {
	db, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/xenforo")
	require.NoError(t, err)
	defer db.Close()

	// idle (50) > open (10) -> idle clamped to 10 at the resolver.
	pool, warns := poolConfig("10", "50", "")
	require.Equal(t, 10, pool.MaxOpen)
	require.Equal(t, 10, pool.MaxIdle, "resolver must clamp idle down to open")
	requireWarningMentioning(t, warns, "DB_MAX_IDLE_CONNS", "50")

	db.SetMaxOpenConns(pool.MaxOpen)
	db.SetMaxIdleConns(pool.MaxIdle)

	// The applied open bound is the ceiling database/sql enforces on idle: the
	// configured idle (10) can never exceed it. Assert the bound landed, which
	// is the observable guarantee that the clamp protects the shared MariaDB.
	require.Equal(t, 10, db.Stats().MaxOpenConnections, "the clamped open bound must be reflected on the *sql.DB — idle can never exceed it")
}

// requireWarningMentioning asserts exactly one warning was produced and that it
// names the given env var and the offending raw value, so the operator log line
// is actionable (mirrors runReferenceCacheRefresh's "invalid X %q, using
// default" style).
func requireWarningMentioning(t *testing.T, warns []string, envVar, rawValue string) {
	t.Helper()
	require.Len(t, warns, 1, "expected exactly one warning, got: %v", warns)
	require.Contains(t, warns[0], envVar, "warning must name the env var so the operator knows which override was rejected")
	require.Contains(t, warns[0], rawValue, "warning must echo the offending raw value")
}
