package gateway

import (
	"context"
	"fmt"
	milpacs "github.com/7cav/api/proto"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rakyll/statik/fs"
	"google.golang.org/grpc"
	"mime"
	"net/http"
	"strings"
)

type Service struct {
	Address string
}

func getOpenAPIHandler() http.Handler {
	mime.AddExtensionType(".svg", "image/svg+xml")

	statikFs, err := fs.New()
	if err != nil {
		panic("creating OpenAPI filesystem: " + err.Error())
	}

	return http.FileServer(statikFs)
}

func (service *Service) Server() (*http.Server, error) {

	conn, err := grpc.DialContext(
		context.Background(),
		"dns:///" + service.Address,
		grpc.WithInsecure(),
		grpc.WithBlock(),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to dial servers: %w", err)
	}

	gwMux := runtime.NewServeMux()
	err = milpacs.RegisterMilpacsHandler(context.Background(), gwMux, conn)

	if err != nil {
		return nil, fmt.Errorf("failed to register gateway: %w", err)
	}

	openApi := getOpenAPIHandler()

	return &http.Server{
		Addr: service.Address,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				gwMux.ServeHTTP(w, r)
				return
			}
			openApi.ServeHTTP(w, r)
		}),
	}, nil
}
