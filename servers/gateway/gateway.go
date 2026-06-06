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
	"context"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/openapi"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
)

type Service struct {
	Address   string
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

// buildAPIHandler assembles the /api middleware chain:
// auth(sentry(compression(inner))). Sentry sits inside auth so it only
// sees authenticated requests, with the API key already on ctx for key-id
// tagging, and outside the compression layer so it observes the final
// response status. No SENTRY_DSN → it is a pass-through.
//
// The auth and compression layers are the rest package's middleware (the
// Phase 3 stack, #125): they moved there verbatim — single source, so the
// two stacks cannot diverge while both are in-tree — and this gateway
// delegates until cutover deletes it. rest.AuthMiddleware attaches the
// validated key with rest.ContextWithKey; grpcServices.KeyFromContext
// delegates to the same context key, so the Sentry key-id tagging below
// keeps seeing it.
//
// Phase 2 de-cache (#123/#124): the response cache is gone — middleware out
// of the chain at #123 (taking the X-Cache header with it — the PRD's
// enumerated break), the cache package and Redis deleted at #124. See ADR
// 0003 (superseded).
//
// Sentry-inside-auth also means auth-layer infrastructure failures (e.g. a
// datastore outage producing mass 401s) generate no Sentry events by design —
// accepted for Phase 0, revisit in the Phase 3 observability slices
// (#130–#132).
//
// Package-level (not inlined in Server) so the chain order is a tested
// contract — see the buildAPIHandler tests — rather than an untestable
// expression inside a dialing function.
func buildAPIHandler(ds datastores.Datastore, inner http.Handler) http.Handler {
	return rest.AuthMiddleware(ds,
		sentryMiddleware(rest.GzipMiddleware(inner)))
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

	handler := buildAPIHandler(service.Datastore, gwMux)

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
