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
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/referencecache"
	"github.com/7cav/api/rest"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// version is overridden at build time via -ldflags
// "-X github.com/7cav/api/servers.version=<tag>" in the release workflow.
// Local dev builds report "dev". It stamps the Sentry release and the served
// OpenAPI spec's info.version.
var version = "dev"

// Public and internal listen addresses. The public listener (:11000) is the
// one nginx fronts; the metrics listener (:9090) is internal-only (kept off
// the published ports / firewalled at the compose+nginx layer, not in code).
const (
	publicAddr  = "0.0.0.0:11000"
	metricsAddr = "0.0.0.0:9090"
)

type MicroServer struct {
	publicServer   *http.Server
	metricsServer  *http.Server
	referenceCache *referencecache.Cache
}

// New initializes a new MicroServer. Since the single-listener cutover (#134)
// it takes no address: the public and metrics listen ports are constants, and
// the old gRPC dial target ($PORT) is gone with the gRPC server.
func New() *MicroServer {
	return &MicroServer{}
}

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

func setupDatasource() *datastores.Mysql {

	dbUser := viper.GetString("db_username")
	if dbUser == "" {
		Error.Println("no database username provided")
		os.Exit(1)
	}

	dbPass := viper.GetString("db_password")
	if dbPass == "" {
		Error.Println("no database password provided")
		os.Exit(1)
	}

	dbHost := viper.GetString("db_host")
	if dbHost == "" {
		Error.Println("no database host provided")
		os.Exit(1)
	}

	dbPort := viper.GetString("db_port")
	if dbPort == "" {
		Error.Println("no database port provided")
		os.Exit(1)
	}

	// refer https://github.com/go-sql-driver/mysql#dsn-data-source-name for details
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/xenforo?charset=utf8mb4&parseTime=True&loc=Local", dbUser, dbPass, dbHost, dbPort)
	conn, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		Error.Println("issue connecting to database", err)
		os.Exit(1)
	}

	return &datastores.Mysql{Db: conn}
}

func (server *MicroServer) Start() {
	Info.Println("Starting 7Cav API version:", version)

	// Resolve and cache the trusted-proxy set (TRUSTED_PROXIES, ADR 0005) ONCE
	// before any listener opens: rest.AuthMiddleware reads this cache to resolve
	// the client IP for its 401 log lines. A non-empty-but-malformed value is
	// fatal here — a misconfigured trust set must not start silently trusting
	// nothing.
	if err := rest.InitTrustedProxies(); err != nil {
		Error.Fatalf("invalid TRUSTED_PROXIES: %v", err)
	}

	// Observability (PRD #112): errors-only Sentry capture, gated on SENTRY_DSN.
	// Disabled (local/dev) nothing is initialised — one Info line, no client, no
	// signal handler, so shutdown behaves exactly as before. Enabled, this
	// BLOCKS boot before any listener opens: the dial pre-check (≤3s on an
	// unreachable host) plus the startup-probe flush window (≤5s) — a worst-case
	// ~8s delay on a degraded network, by design, so a broken pipeline is
	// visible before traffic flows. The shutdown flush handler is installed only
	// when capture is enabled.
	if rest.SetupSentry(version) {
		rest.FlushSentryOnShutdown()
	}

	// plain-TCP listeners (no TLS — nginx terminates the public one; the metrics
	// listener is internal-only).
	publicL, err := net.Listen("tcp", publicAddr)
	if err != nil {
		Error.Fatalf("Failed to listen on %s: %v", publicAddr, err)
	}
	metricsL, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		Error.Fatalf("Failed to listen on %s: %v", metricsAddr, err)
	}

	ds := setupDatasource()

	// Warm the reference cache before serving: rest.New panics on a nil/cold
	// cache, and the tickets routes resolve reference names through it.
	server.referenceCache = referencecache.New(ds)
	if err := server.referenceCache.Refresh(context.Background()); err != nil {
		Error.Fatalf("initial reference cache load failed: %v", err)
	}
	go runReferenceCacheRefresh(context.Background(), server.referenceCache)

	Info.Println("Starting metrics listener on", metricsAddr)
	go servMetrics(server, metricsL)
	Info.Println("Starting public listener on", publicAddr)
	servPublic(server, publicL, ds)
}

// servPublic serves the single public listener: the REST API under /api and
// the Swagger UI + OpenAPI specs everywhere else, at the same URLs the old
// grpc-gateway used. rest.New is the API handler; rest.DocsHandler serves the
// docs with the spec's info.version stamped from the build-time version.
func servPublic(server *MicroServer, lis net.Listener, ds datastores.Datastore) {
	apiHandler := rest.New(ds, server.referenceCache)
	docsHandler := rest.DocsHandler(version)

	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		docsHandler.ServeHTTP(w, r)
	})

	server.publicServer = &http.Server{Handler: root}
	if err := server.publicServer.Serve(lis); err != nil {
		Error.Fatalf("unable to start public HTTP server: %v", err)
	}
}

// servMetrics serves the internal Prometheus metrics endpoint on its own
// listener. The handler is mounted at the listener root; the
// compose/nginx non-exposure keeps it off the public network (deploy config,
// not code).
func servMetrics(server *MicroServer, lis net.Listener) {
	server.metricsServer = &http.Server{Handler: rest.MetricsHandler()}
	if err := server.metricsServer.Serve(lis); err != nil {
		Error.Fatalf("unable to start metrics HTTP server: %v", err)
	}
}

func runReferenceCacheRefresh(ctx context.Context, rc *referencecache.Cache) {
	interval := 15 * time.Minute
	if v := viper.GetString("REFERENCE_CACHE_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		} else {
			Warn.Printf("invalid REFERENCE_CACHE_REFRESH_INTERVAL %q, using default %s", v, interval)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := rc.Refresh(ctx); err != nil {
				Warn.Printf("reference cache refresh failed (keeping previous data): %v", err)
			}
		}
	}
}
