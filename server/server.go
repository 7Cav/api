package server

import (
	"context"
	"fmt"
	milpacs "github.com/7cav/api/proto"
	"github.com/7cav/api/service"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rakyll/statik/fs"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"io/ioutil"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	_ "github.com/7cav/api/statik" // static files import
)

type MicroServer struct {
	addr string
	httpServer *http.Server
	grpcServer *grpc.Server
}

// New initializes a new Backend struct.
func New(addr string) *MicroServer {
	return &MicroServer{
		addr: addr,
	}
}

func getOpenAPIHandler() http.Handler {
	mime.AddExtensionType(".svg", "image/svg+xml")

	statikFs, err := fs.New()
	if err != nil {
		panic("creating OpenAPI filesystem: " + err.Error())
	}

	return http.FileServer(statikFs)
}

func (server *MicroServer) prepareHttp() (*http.Server, error) {
	conn, err := grpc.DialContext(
		context.Background(),
		server.addr,
		grpc.WithInsecure(),
		grpc.WithBlock(),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to dial server: %w", err)
	}

	gwMux := runtime.NewServeMux()
	err = milpacs.RegisterMilpacsHandler(context.Background(), gwMux, conn)

	if err != nil {
		return nil, fmt.Errorf("failed to register gateway: %w", err)
	}

	openApi := getOpenAPIHandler()

	return &http.Server{
		Addr: server.addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				gwMux.ServeHTTP(w, r)
				return
			}
			openApi.ServeHTTP(w, r)
		}),
	}, nil
}

func (server *MicroServer) Start() {

	// Adds gRPC internal logs. This is quite verbose, so adjust as desired!
	log := grpclog.NewLoggerV2(os.Stdout, ioutil.Discard, ioutil.Discard)
	grpclog.SetLoggerV2(log)

	// create listener for TCP connections
	lis, err := net.Listen("tcp", server.addr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %w", server.addr, err)
	}

	tcpMux := cmux.New(lis)

	// Connection dispatcher rules
	grpcL := tcpMux.MatchWithWriters(cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"))
	httpL := tcpMux.Match(cmux.HTTP1Fast())

	go func(serv *MicroServer, grpcL net.Listener) {
		// init gRPC server instance
		serv.grpcServer = grpc.NewServer()
		milpacs.RegisterMilpacsServer(serv.grpcServer, service.New())

		if err = serv.grpcServer.Serve(grpcL); err != nil {
			log.Fatalf("Unable to start external gRPC server: %s", err.Error())
		}
	}(server, grpcL)

	go func(serv *MicroServer, httpl net.Listener) {
		serv.httpServer, err = serv.prepareHttp()

		if err = serv.httpServer.Serve(httpL); err != nil {
			log.Fatalf("Unable to start HTTP server: %s", err.Error())
		}
	}(server, httpL)

	err = tcpMux.Serve()

	if err != nil {
		log.Fatalf("Error with TCPmux Serving: %w", err)
	}
}





