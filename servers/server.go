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
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/7cav/api/cache"
	"github.com/7cav/api/datastores"
	milpacs "github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
	httpServices "github.com/7cav/api/servers/gateway"
	grpcServices "github.com/7cav/api/servers/grpc"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const version = "2.2.0"

type MicroServer struct {
	addr           string
	httpServer     *http.Server
	grpcServer     *grpc.Server
	cache          *cache.RedisCache
	referenceCache *referencecache.Cache
}

// New initializes a new Backend struct.
func New(addr string) *MicroServer {

	return &MicroServer{
		addr: addr,
	}
}

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

func setupRedis() *cache.RedisCache {
	redisHost := viper.GetString("REDIS_HOST")
	if redisHost == "" {
		Error.Println("no redis host provided")
		os.Exit(1)
	}

	redisPort := viper.GetString("REDIS_PORT")
	if redisPort == "" {
		Error.Println("no redis port provided")
		os.Exit(1)
	}

	redisPassword := viper.GetString("REDIS_PASSWORD")
	if redisPassword == "" {
		Info.Println("REDIS_PASSWORD empty — connecting without AUTH")
	}

	return cache.NewRedisCache(redisHost, redisPort, redisPassword)
}

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
	// Adds gRPC internal logs. This is quite verbose, so adjust as desired!
	grpcLogger := grpclog.NewLoggerV2(io.Discard, os.Stdout, os.Stdout)
	grpclog.SetLoggerV2(grpcLogger)

	Info.Println("Starting 7Cav API version:", version)

	//create TLS listener for TCP connections
	grpcL, err := net.Listen("tcp", "0.0.0.0:10000")
	httpL, err := net.Listen("tcp", "0.0.0.0:11000")

	if err != nil {
		Error.Fatalf("Failed to listen on %s: %v", server.addr, err)
	}

	ds := setupDatasource()
	server.cache = setupRedis()
	server.referenceCache = referencecache.New(ds)
	if err := server.referenceCache.Refresh(context.Background()); err != nil {
		Error.Fatalf("initial reference cache load failed: %v", err)
	}
	go runReferenceCacheRefresh(context.Background(), server.referenceCache)
	go cache.CacheManager(server.cache, ds)

	// relevant Grpc options
	// note: commenting out the creds option, because internally (nginx <-> golang) traffic is not encrypted.
	// 		 If this needed to change in the future, then we will need to refactor this method
	opts := []grpc.ServerOption{
		// Intercept request to check the token.
		grpc.UnaryInterceptor(grpcServices.NewAuthInterceptor(ds)),
		//grpc.Creds(creds),
	}

	// launch goroutines for multiplexed listener
	Info.Println("Starting HTTP listener")
	go servHTTP(server, httpL, ds)
	Info.Println("Starting GRPC listener")
	servGRPC(server, grpcL, opts, ds)
}

func servGRPC(server *MicroServer, lis net.Listener, grpcOpts []grpc.ServerOption, ds datastores.Datastore) {
	// Due to the grpc-gateway setup, the GRPC service is at bottom of the relevant API call.
	// As such, it requires the DB connection. But the HTTP service doesn't
	service := &grpcServices.MilpacsService{Datastore: ds}

	// init gRPC servers instance
	server.grpcServer = grpc.NewServer(grpcOpts...)
	milpacs.RegisterMilpacServiceServer(server.grpcServer, service)

	ticketsService := &grpcServices.TicketsService{
		Datastore:      ds,
		ReferenceCache: server.referenceCache,
	}
	milpacs.RegisterTicketsServiceServer(server.grpcServer, ticketsService)

	if err := server.grpcServer.Serve(lis); err != nil {
		Error.Fatalf("unable to start external gRPC servers: %v", err)
	}
}

func servHTTP(server *MicroServer, lis net.Listener, ds datastores.Datastore) {
	service := httpServices.Service{Address: server.addr, Cache: server.cache, Datastore: ds}
	server.httpServer = service.Server()
	if err := server.httpServer.Serve(lis); err != nil {
		Error.Fatalf("unable to start HTTP servers: %v", err)
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
