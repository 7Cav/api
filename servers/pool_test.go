package servers

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// TestPoolConfig_Defaults pins the conservative baked-in defaults that apply
// when every DB_* pool override is unset (zero / empty). These defaults are the
// fix for #204: a bounded, warm pool that clears a single-key burst in well
// under a second instead of stampeding the shared MariaDB into Error 1040. The
// numbers are justified against prod (max_connections=300, forum
// pm.max_children=30, all-time peak 68): 25 open + 25 idle keeps the pool warm,
// 30m lifetime recycles conns under wait_timeout. See poolConfig's doc comment
// and CONTEXT.md.
func TestPoolConfig_Defaults(t *testing.T) {
	got := poolConfig(0, 0, "")

	require.Equal(t, defaultMaxOpenConns, got.MaxOpen, "unset DB_MAX_OPEN_CONNS must fall back to the default")
	require.Equal(t, defaultMaxIdleConns, got.MaxIdle, "unset DB_MAX_IDLE_CONNS must fall back to the default")
	require.Equal(t, defaultConnMaxLifetime, got.MaxLifetime, "unset DB_CONN_MAX_LIFETIME must fall back to the default")

	// Acceptance criterion: the pool must be bounded (non-zero, finite).
	require.Greater(t, got.MaxOpen, 0, "MaxOpen must be a positive, finite bound — 0 means unbounded, the bug")
	require.Greater(t, got.MaxLifetime, time.Duration(0), "MaxLifetime must be finite")
}

// TestPoolConfig_Overrides pins that explicit, valid env values win over the
// defaults.
func TestPoolConfig_Overrides(t *testing.T) {
	got := poolConfig(40, 10, "5m")

	require.Equal(t, 40, got.MaxOpen)
	require.Equal(t, 10, got.MaxIdle)
	require.Equal(t, 5*time.Minute, got.MaxLifetime)
}

// TestPoolConfig_InvalidLifetimeFallsBack pins that a garbage DB_CONN_MAX_LIFETIME
// (unparseable duration) falls back to the default rather than silently
// disabling recycling.
func TestPoolConfig_InvalidLifetimeFallsBack(t *testing.T) {
	got := poolConfig(0, 0, "not-a-duration")
	require.Equal(t, defaultConnMaxLifetime, got.MaxLifetime, "an unparseable DB_CONN_MAX_LIFETIME must fall back to the default")
}

// TestPoolConfig_NegativeOpenFallsBack pins that a nonsensical negative
// DB_MAX_OPEN_CONNS falls back to the default — a negative bound would mean
// "unbounded" to database/sql (SetMaxOpenConns(<=0) is unlimited), reopening the
// exact #204 hole.
func TestPoolConfig_NegativeOpenFallsBack(t *testing.T) {
	got := poolConfig(-5, 0, "")
	require.Equal(t, defaultMaxOpenConns, got.MaxOpen, "a non-positive DB_MAX_OPEN_CONNS must fall back to the default, never 0/unbounded")
}

// TestPoolConfig_IdleClampedToOpen pins that idle conns never exceed open conns:
// database/sql silently reduces idle to match max-open, but we make the intent
// explicit so an operator who sets idle > open gets the sane, documented
// behavior rather than a surprising silent clamp.
func TestPoolConfig_IdleClampedToOpen(t *testing.T) {
	got := poolConfig(10, 50, "")
	require.Equal(t, 10, got.MaxIdle, "MaxIdle must be clamped down to MaxOpen — more idle than open is meaningless")
}

// TestPoolConfig_NegativeIdleFallsBack pins that a negative DB_MAX_IDLE_CONNS
// falls back to the default (then clamped to open), keeping the pool warm rather
// than cold (idle 0 would pay connection-setup cost mid-burst, the root cause of
// the latency climb in #204).
func TestPoolConfig_NegativeIdleFallsBack(t *testing.T) {
	got := poolConfig(25, -1, "")
	require.Equal(t, defaultMaxIdleConns, got.MaxIdle, "a negative DB_MAX_IDLE_CONNS must fall back to the warm default")
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

	pool := poolConfig(0, 0, "") // defaults
	db.SetMaxOpenConns(pool.MaxOpen)
	db.SetMaxIdleConns(pool.MaxIdle)
	db.SetConnMaxLifetime(pool.MaxLifetime)

	require.Equal(t, defaultMaxOpenConns, db.Stats().MaxOpenConnections, "after applying the default pool config the *sql.DB must report a non-zero, finite MaxOpenConnections")
	require.Greater(t, db.Stats().MaxOpenConnections, 0, "MaxOpenConnections must be a positive, finite bound — never 0/unbounded")
}
