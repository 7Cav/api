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

package gateway

import (
	"compress/gzip"
	"context"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/7cav/api/cache"
	"github.com/7cav/api/datastores"
	"github.com/7cav/api/openapi"
	"github.com/7cav/api/proto"
	grpcServices "github.com/7cav/api/servers/grpc"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

type Service struct {
	Address   string
	Cache     *cache.RedisCache
	Datastore datastores.Datastore
}

var (
	Info  = log.New(os.Stdout, "INFO: ", log.LstdFlags)
	Warn  = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
	Error = log.New(os.Stdout, "ERROR: ", log.LstdFlags)
)

func getOpenAPIHandler() http.Handler {
	Info.Println("setting up OpenAPI Handler")
	if err := mime.AddExtensionType(".svg", "image/svg+xml"); err != nil {
		Error.Println("failed to add MIME extension type for .svg: ", err)
	}
	sub, err := fs.Sub(openapi.Files, "assets")
	if err != nil {
		Error.Println("creating OpenAPI sub-filesystem: ", err)
	}
	return http.FileServer(http.FS(sub))
}

// maxTokenLen is the maximum length of a raw API key we'll accept.
// cav7_ prefix (5) + 64 hex chars = 69; 128 gives generous headroom.
const maxTokenLen = 128

// errBearerScheme is the 401 body returned when the Authorization header is
// missing or doesn't carry a usable Bearer token (no/empty/oversized token).
// It names the expected format so callers who paste a raw key without the
// "Bearer " prefix get a self-explanatory error. The key-validation-failure
// branch stays the generic "Unauthorized" so it leaks nothing about whether a
// key exists, is expired, or lacks scopes.
const errBearerScheme = "Unauthorized: expected 'Authorization: Bearer <key>' header"

func authMiddleware(ds datastores.Datastore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := datastores.ParseBearerToken(r.Header.Get("Authorization"), maxTokenLen)
		if token == "" {
			Warn.Printf("Unauthorized HTTP access attempt (bad bearer scheme) from %s", r.RemoteAddr)
			http.Error(w, errBearerScheme, http.StatusUnauthorized)
			return
		}

		key, err := ds.ValidateApiKey(token)
		if err != nil || key == nil {
			Warn.Printf("Unauthorized HTTP access attempt from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Attach the validated key to the request ctx (mirrors the gRPC auth
		// interceptor) so downstream consumers — e.g. Sentry key-id tagging —
		// can identify the caller without ever seeing the bearer token.
		next.ServeHTTP(w, r.WithContext(grpcServices.ContextWithKey(r.Context(), key)))
	})
}

func compressionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer func() {
				if err := gz.Close(); err != nil {
					fmt.Printf("Failed to close gzip writer: %v\n", err)
				}
			}()
			gzw := &gzipResponseWriter{ResponseWriter: w, Writer: gz}
			next.ServeHTTP(gzw, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	Writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	w.Header().Del("Content-Length") // This is necessary as otherwise it will have the uncompressed length
	return w.Writer.Write(b)
}

// buildAPIHandler assembles the /api middleware chain:
// auth(sentry(compression(inner))). Sentry sits inside auth so it only
// sees authenticated requests, with the API key already on ctx for key-id
// tagging, and outside the compression layer so it observes the final
// response status. No SENTRY_DSN → it is a pass-through.
//
// Phase 2 de-cache (#123): the response-cache middleware is out of the chain
// — with it, the X-Cache header (enumerated break). The unused c parameter is
// deliberate: the cache package, CacheManager poller, and Redis keep running
// through the soak (#124 deletes them), and keeping the signature makes the
// revert a one-liner — restore the chain line to:
//
//	sentryMiddleware(middleware.CacheMiddleware(c, compressionMiddleware(inner)))
//
// (re-adding the github.com/7cav/api/middleware import).
//
// Sentry-inside-auth also means auth-layer infrastructure failures (e.g. a
// datastore outage producing mass 401s) generate no Sentry events by design —
// accepted for Phase 0, revisit in the Phase 3 observability slices
// (#130–#132).
//
// Package-level (not inlined in Server) so the chain order is a tested
// contract — see the buildAPIHandler tests — rather than an untestable
// expression inside a dialing function.
func buildAPIHandler(ds datastores.Datastore, c *cache.RedisCache, inner http.Handler) http.Handler {
	_ = c // kept for the one-line revert; see the doc comment above
	return authMiddleware(ds,
		sentryMiddleware(compressionMiddleware(inner)))
}

func (service *Service) Server() *http.Server {
	// relevant Grpc _dialing_ options
	// note: commenting out the TransportCredentials option, because internally (nginx <-> golang) traffic is not encrypted.
	// 		 If this needed to change in the future, then we will need to refactor this method

	var sendMessageInMB = 20

	conn, err := grpc.DialContext(
		context.Background(),
		"dns:///"+service.Address,
		grpc.WithBlock(),
		grpc.WithInsecure(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*sendMessageInMB)),
		//grpc.WithTransportCredentials(creds),
	)

	if err != nil {
		Error.Println("failed to dial servers: ", err)
		return nil
	}

	gwMux := runtime.NewServeMux()
	err = proto.RegisterMilpacServiceHandler(context.Background(), gwMux, conn)

	if err != nil {
		Error.Println("failed to register gateway: ", err)
		return nil
	}

	err = proto.RegisterTicketsServiceHandler(context.Background(), gwMux, conn)
	if err != nil {
		Error.Println("failed to register tickets gateway: ", err)
		return nil
	}

	openApi := getOpenAPIHandler()

	handler := buildAPIHandler(service.Datastore, service.Cache, gwMux)

	// if requests start with /api then forward it on to the grpc-gateway client
	// otherwise, just serve it as norma (basically the OpenAPI)
	return &http.Server{
		Addr: service.Address,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				handler.ServeHTTP(w, r)
				return
			}
			openApi.ServeHTTP(w, r)
		}),
	}
}
