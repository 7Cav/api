/*
 *  Copyright (C) 2021 7Cav.us
 *  This file is part of 7Cav-API <https://github.com/7cav/api>.
 *
 *  7Cav-API is free software: you can redistribute it and/or modify
 *  it under the terms of the GNU General Public License as published by
 *  the Free Software Foundation, either version 3 of the License, or
 *  (at your option) any later version.
 *
 *  7Cav-API is distributed in the hope that it will be useful,
 *  but WITHOUT ANY WARRANTY; without even the implied warranty of
 *  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 *  GNU General Public License for more details.
 *
 *  You should have received a copy of the GNU General Public License
 *  along with 7Cav-API. If not, see <http://www.gnu.org/licenses/>.
 */

package servers

import (
	"fmt"
	"strconv"
	"time"
)

// Connection-pool defaults (#204). The API and the XenForo forum share one
// MariaDB. With GORM defaults the underlying database/sql pool is UNBOUNDED
// (MaxOpenConns == 0), so a single-key burst opens one MySQL conn per in-flight
// request at once and climbs toward the server's global max_connections — on
// 2026-06-18 a 67-call burst drove 8 `Error 1040 (Too many connections)`,
// shared-fate with the forum.
//
// These conservative defaults are justified against prod limits:
//   - server max_connections = 300
//   - forum InnoDB pool / pm.max_children = 30 (the forum's reserved share)
//   - all-time peak observed = 68 conns
//
// Budget: 25 (API) + 30 (forum) + ~8 (exporters) ≈ 63, comfortably under 300,
// leaving headroom for the forum and the metric exporters. MaxIdle == MaxOpen
// keeps the pool fully WARM, so a burst queues against ready conns instead of
// paying connection-setup cost mid-storm (the root cause of the latency climb):
// the 66-call burst clears in ~90 ms instead of stampeding into 1040s.
// ConnMaxLifetime is finite so conns recycle, but well under MySQL wait_timeout.
//
// All three are overridable via the DB_* env convention (viper AutomaticEnv):
// DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS, DB_CONN_MAX_LIFETIME. An invalid,
// out-of-range, or clamped override is REJECTED with a logged warning (the
// safe default / clamp is used instead) — never silently dropped. See
// poolConfig; the call site logs the returned warnings via Warn.Printf.
const (
	defaultMaxOpenConns    = 25
	defaultMaxIdleConns    = 25
	defaultConnMaxLifetime = 30 * time.Minute
)

// dbPoolConfig is the resolved connection-pool sizing applied to the underlying
// *sql.DB after gorm.Open.
type dbPoolConfig struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
}

// poolConfig maps the RAW env strings to the connection-pool settings, applying
// the conservative #204 defaults whenever an override is unset or invalid. It is
// a PURE function (no viper, no DB, no os.Exit) so it is unit-testable directly:
// setupDatasource reads the env via viper (GetString) and passes the raw values
// in, then logs each returned warning.
//
// It returns the resolved config and a slice of human-readable warnings, one per
// REJECTED or CLAMPED override. The empty string ("") means "unset" and is a
// clean default — it produces NO warning. An invalid (non-numeric / unparseable),
// out-of-range (non-positive), or clamped (idle > open) override falls back to
// the safe default / clamp WITH a warning, so an operator never believes a bad
// override is live. Parsing the raw string here (rather than taking pre-parsed
// ints) is what lets the resolver distinguish unset from invalid — viper.GetInt
// would collapse "banana" and "" to the same 0. The warning text mirrors
// runReferenceCacheRefresh's "invalid X %q, using default" style.
//
// Fallback / clamping rules (each defends a way a bad override could reopen the
// #204 hole or cool the pool):
//   - maxOpen: empty -> defaultMaxOpenConns silently; non-numeric or <= 0 ->
//     defaultMaxOpenConns + warning. A non-positive value would mean "unbounded"
//     to database/sql, the exact bug.
//   - maxIdle: empty -> defaultMaxIdleConns silently; non-numeric or <= 0 ->
//     defaultMaxIdleConns + warning, keeping the pool warm. maxIdle is then
//     clamped DOWN to MaxOpen (with a warning) since more idle than open is
//     meaningless (database/sql would silently clamp anyway).
//   - lifetime: empty -> default silently; non-numeric or non-positive ->
//     default + warning, never disabling recycling silently.
func poolConfig(rawOpen, rawIdle, rawLifetime string) (dbPoolConfig, []string) {
	var warns []string

	cfg := dbPoolConfig{
		MaxOpen:     defaultMaxOpenConns,
		MaxIdle:     defaultMaxIdleConns,
		MaxLifetime: defaultConnMaxLifetime,
	}

	cfg.MaxOpen = resolveInt("DB_MAX_OPEN_CONNS", rawOpen, defaultMaxOpenConns, &warns)
	cfg.MaxIdle = resolveInt("DB_MAX_IDLE_CONNS", rawIdle, defaultMaxIdleConns, &warns)

	// Idle can never usefully exceed open. Clamp explicitly (and announce it)
	// rather than letting database/sql silently reduce it.
	if cfg.MaxIdle > cfg.MaxOpen {
		warns = append(warns, fmt.Sprintf(
			"DB_MAX_IDLE_CONNS %q exceeds DB_MAX_OPEN_CONNS (%d), clamping idle to %d",
			rawIdle, cfg.MaxOpen, cfg.MaxOpen))
		cfg.MaxIdle = cfg.MaxOpen
	}

	if rawLifetime != "" {
		if d, err := time.ParseDuration(rawLifetime); err != nil || d <= 0 {
			warns = append(warns, fmt.Sprintf(
				"invalid DB_CONN_MAX_LIFETIME %q, using default %s", rawLifetime, defaultConnMaxLifetime))
		} else {
			cfg.MaxLifetime = d
		}
	}

	return cfg, warns
}

// resolveInt parses a raw DB_* integer override. An empty string is a clean
// unset -> def with no warning. A non-numeric or non-positive value is rejected
// -> def WITH a warning appended (a non-positive bound means "unbounded" to
// database/sql, the #204 hole).
func resolveInt(envVar, raw string, def int, warns *[]string) int {
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		*warns = append(*warns, fmt.Sprintf("invalid %s %q, using default %d", envVar, raw, def))
		return def
	}
	return v
}
