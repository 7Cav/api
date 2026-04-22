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
	"log"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/7cav/api/cache"
	"github.com/7cav/api/datastores"
	"github.com/7cav/api/middleware"
	"github.com/7cav/api/proto"
	_ "github.com/7cav/api/statik" // static files import - unused in the codebase, but required cuz reasons
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rakyll/statik/fs"
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
	statikFs, err := fs.New()
	if err != nil {
		Error.Println("creating OpenAPI filesystem: ", err)
	}
	return http.FileServer(statikFs)
}

// maxTokenLen is the maximum length of a raw API key we'll accept.
// cav7_ prefix (5) + 64 hex chars = 69; 128 gives generous headroom.
const maxTokenLen = 128

func authMiddleware(ds datastores.Datastore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if token == "" || len(token) > maxTokenLen {
			Warn.Printf("Unauthorized HTTP access attempt from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		key, err := ds.ValidateApiKey(token)
		if err != nil || key == nil || !key.ScopeRead {
			Warn.Printf("Unauthorized HTTP access attempt from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
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

	openApi := getOpenAPIHandler()

	handler := authMiddleware(service.Datastore,
		middleware.CacheMiddleware(service.Cache, compressionMiddleware(gwMux)))

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
