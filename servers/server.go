package servers

import (
	"fmt"
	"github.com/7cav/api/datastores"
	milpacs "github.com/7cav/api/proto"
	httpServices "github.com/7cav/api/servers/gateway"
	grpcServices "github.com/7cav/api/servers/grpc"
	"github.com/spf13/viper"
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

var (
	Info  = log.New(os.Stdout, "INFO: ", 0)
	Warn  = log.New(os.Stdout, "WARNING: ", 0)
	Error = log.New(os.Stdout, "ERROR: ", 0)
)

func setupDatasource() *datastores.Mysql {

	dbUser := viper.GetString("db_username"); if dbUser == "" {
		Error.Println("no database username provided")
	}

	dbPass := viper.GetString("db_password"); if dbPass == "" {
		Error.Println("no database password provided")
	}

	dbHost := viper.GetString("db_host"); if dbHost == "" {
		Error.Println("no database host provided")
	}

	dbPort := viper.GetString("db_port"); if dbPort == "" {
		Error.Println("no database port provided")
	}

	// refer https://github.com/go-sql-driver/mysql#dsn-data-source-name for details
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/xenforo?charset=utf8mb4&parseTime=True&loc=Local", dbUser, dbPass, dbHost, dbPort)
	conn, err := gorm.Open(mysql.Open(dsn), &gorm.Config{}); if  err != nil {
		Error.Println("issue connecting to database", err)
	}

	return &datastores.Mysql{Db: conn}
}

func (server *MicroServer) Start() {
	// Adds gRPC internal logs. This is quite verbose, so adjust as desired!
	grpcLogger :=grpclog.NewLoggerV2(ioutil.Discard, os.Stdout, os.Stdout)
	grpclog.SetLoggerV2(grpcLogger)

	//create TLS listener for TCP connections
	grpcL, err := net.Listen("tcp", "0.0.0.0:10000")
	httpL, err := net.Listen("tcp","0.0.0.0:11000")

	if err != nil {
		Error.Fatalf("Failed to listen on %s: %w", server.addr, err)
	}

	ds := setupDatasource()

	// relevant Grpc options
	// note: commenting out the creds option, because internally (nginx <-> golang) traffic is not encrypted.
	// 		 If this needed to change in the future, then we will need to refactor this method
	opts := []grpc.ServerOption{
		// Intercept request to check the token.
		grpc.UnaryInterceptor(grpcServices.ValidateToken),
		//grpc.Creds(creds),
	}

	// launch goroutines for multiplexed listener
	Info.Println("Starting HTTP listener")
	go servHTTP(server, httpL)
	Info.Println("Starting GRPC listener")
	servGRPC(server, grpcL, opts, ds)
}

func servGRPC(server *MicroServer, lis net.Listener, grpcOpts []grpc.ServerOption, ds datastores.Datastore) {
	// Due to the grpc-gateway setup, the GRPC service is at bottom of the relevant API call.
	// As such, it requires the DB connection. But the HTTP service doesn't
	service := &grpcServices.MilpacsService{Datastore: ds}

	// init gRPC servers instance
	server.grpcServer = grpc.NewServer(grpcOpts...)
	milpacs.RegisterMilpacsServer(server.grpcServer, service)

	if err := server.grpcServer.Serve(lis); err != nil {
		Error.Fatalf("unable to start external gRPC servers: ", err)
	}
}

func servHTTP(server *MicroServer, lis net.Listener) {
	service := httpServices.Service{Address: server.addr}
	server.httpServer = service.Server()

	if err := server.httpServer.Serve(lis); err != nil {
		Error.Fatalf("unable to start HTTP servers: ", err)
	}
}







