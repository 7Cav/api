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
	"github.com/7cav/api/proto"
	_ "github.com/7cav/api/statik" // static files import - unused in the codebase, but required cuz reasons
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rakyll/statik/fs"
	"google.golang.org/grpc"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"
)

type Service struct {
	Address string
}

var (
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Warn  = log.New(os.Stdout, "WARNING: ", 0)
	Error = log.New(os.Stdout, "ERROR: ", 0)
)

func getOpenAPIHandler() http.Handler {
	Info.Println("setting up OpenAPI Handler")
	mime.AddExtensionType(".svg", "image/svg+xml")
	statikFs, err := fs.New()
	if err != nil {
		Error.Println("creating OpenAPI filesystem: ", err)
	}
	return http.FileServer(statikFs)
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

	// if requests start with /api then forward it on to the grpc-gateway client
	// otherwise, just serve it as norma (basically the OpenAPI)
	return &http.Server{
		Addr: service.Address,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				gwMux.ServeHTTP(w, r)
				return
			}
			openApi.ServeHTTP(w, r)
		}),
	}
}
