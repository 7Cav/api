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

import "time"

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
// DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS, DB_CONN_MAX_LIFETIME. See poolConfig.
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

// poolConfig maps raw env values to the connection-pool settings, applying the
// conservative #204 defaults whenever an override is unset or invalid. It is a
// PURE function (no viper, no DB, no os.Exit) so it is unit-testable directly:
// setupDatasource reads the env via viper and passes the raw values in.
//
// Fallback / clamping rules (each defends a way a bad override could reopen the
// #204 hole or cool the pool):
//   - maxOpen <= 0 (unset, zero, or negative) -> defaultMaxOpenConns. A
//     non-positive value would mean "unbounded" to database/sql, the exact bug.
//   - maxIdle <= 0 (unset, zero, or negative) -> defaultMaxIdleConns, keeping
//     the pool warm. maxIdle is then clamped DOWN to MaxOpen, since more idle
//     than open is meaningless (database/sql would silently clamp anyway).
//   - lifetime: parsed as a Go duration; empty OR unparseable -> the default,
//     never disabling recycling silently.
func poolConfig(maxOpen, maxIdle int, lifetime string) dbPoolConfig {
	cfg := dbPoolConfig{
		MaxOpen:     maxOpen,
		MaxIdle:     maxIdle,
		MaxLifetime: defaultConnMaxLifetime,
	}

	if cfg.MaxOpen <= 0 {
		cfg.MaxOpen = defaultMaxOpenConns
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = defaultMaxIdleConns
	}
	// Idle can never usefully exceed open.
	if cfg.MaxIdle > cfg.MaxOpen {
		cfg.MaxIdle = cfg.MaxOpen
	}

	if lifetime != "" {
		if d, err := time.ParseDuration(lifetime); err == nil && d > 0 {
			cfg.MaxLifetime = d
		}
	}

	return cfg
}
