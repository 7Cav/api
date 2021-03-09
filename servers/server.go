package servers

import (
	"github.com/7cav/api/datastore"
	milpacs "github.com/7cav/api/proto"
	httpServices "github.com/7cav/api/servers/gateway"
	grpcServices "github.com/7cav/api/servers/grpc"
	_ "github.com/7cav/api/statik" // static files import
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
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

func setupDatasource() *datastore.Mysql {
	// refer https://github.com/go-sql-driver/mysql#dsn-data-source-name for details
	dsn := "xenforo:password@tcp(127.0.0.1:3306)/xenforo?charset=utf8mb4&parseTime=True&loc=Local"
	conn, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})

	ds := &datastore.Mysql{Db: conn}
	return ds
}

func (server *MicroServer) Start() {
	// Adds gRPC internal logs. This is quite verbose, so adjust as desired!
	log := grpclog.NewLoggerV2(os.Stdout, ioutil.Discard, ioutil.Discard)
	grpclog.SetLoggerV2(log)

	//cer, err := tls.LoadX509KeyPair("out/localhost.crt", "out/localhost.key")
	//if err != nil {
	//	log.Info(err)
	//	return
	//}
	//config := &tls.Config{Certificates: []tls.Certificate{cer}}

	// create TLS listener for TCP connections
	lis, err := net.Listen("tcp", server.addr)

	if err != nil {
		log.Fatalf("Failed to listen on %s: %w", server.addr, err)
	}

	tcpMux := cmux.New(lis)

	// Connection dispatcher rules
	httpL := tcpMux.Match(cmux.HTTP1Fast())
	grpcL := tcpMux.MatchWithWriters(cmux.HTTP2MatchHeaderFieldPrefixSendSettings("content-type", "application/grpc"))

	var ds datastore.Datastore

	ds = setupDatasource()


	opts := []grpc.ServerOption{
		// Intercept request to check the token.
		grpc.UnaryInterceptor(grpcServices.ValidateToken),
	}

	// launch goroutines for multiplexed listener
	go servGRPC(server, grpcL, opts, ds)
	go servHTTP(server, httpL)

	err = tcpMux.Serve()

	if err != nil {
		log.Fatalf("Error with TCPmux Serving: %w", err)
	}
}

func servGRPC(server *MicroServer, lis net.Listener, grpcOpts []grpc.ServerOption, ds datastore.Datastore) {
	service := &grpcServices.MilpacsService{Datastore: ds}

	// init gRPC servers instance
	server.grpcServer = grpc.NewServer(grpcOpts...)
	milpacs.RegisterMilpacsServer(server.grpcServer, service)

	if err := server.grpcServer.Serve(lis); err != nil {
		log.Fatalf("Unable to start external gRPC servers: %s", err.Error())
	}
}

func servHTTP(server *MicroServer, lis net.Listener) {
	service := httpServices.Service{Address: server.addr}
	var err error
	server.httpServer, err = service.Server()

	if err = server.httpServer.Serve(lis); err != nil {
		log.Fatalf("Unable to start HTTP servers: %s", err.Error())
	}
}







