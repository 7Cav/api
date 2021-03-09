/*
Copyright © 2021 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/7cav/api/proto"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/grpclog"
	"strconv"
)

// exampleCmd represents the example command
var exampleCmd = &cobra.Command{
	Use:   "example",
	Short: "A brief description of your command",
	Run: func(cmd *cobra.Command, args []string) {
		//rpcCreds := oauth.NewOauthAccess(&oauth2.Token{AccessToken: "<token>"})

		//b, _ := ioutil.ReadFile("out/localhost.crt")
		//cp := x509.NewCertPool()
		//if !cp.AppendCertsFromPEM(b) {
		//	log.Fatal("cannot load TLS credentials")
		//}

		fmt.Println("gathering creds grpc")
		opts := []grpc.DialOption{
			//grpc.WithTransportCredentials(credentials.NewClientTLSFromCert(cp, "")),
			//grpc.WithPerRPCCredentials(rpcCreds),
			grpc.WithInsecure(),
		}
		fmt.Println("dialing grpc")
		conn, err := grpc.Dial("0.0.0.0:10000", opts...)
		fmt.Println("dialed...")
		if err != nil {
			grpclog.Fatalf("fail to dial: %v", err)
		}
		defer conn.Close()

		fmt.Println("creating client")
		client := proto.NewMilpacsClient(conn)

		if len(args) != 1{
			grpclog.Fatalln("must supply id to request as argument")
		}

		id, _ := strconv.ParseUint(args[0], 10, 64)

		fmt.Println("Searching for client with ID:", id)
		msg, err := client.GetProfile(context.Background(), &proto.ProfileRequest{UserId: id})
		if err != nil {
			grpclog.Fatalf("fail to get profile: %v", err)
		}
		fmt.Println(msg)
	},
}

func loadTLSCredentials() (credentials.TransportCredentials, error) {
	// Load server's certificate and private key
	serverCert, err := tls.LoadX509KeyPair("certs/server-cert.pem", "certs/server-key.pem")
	if err != nil {
		return nil, err
	}

	// Create the credentials and return it
	config := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.NoClientCert,
	}

	return credentials.NewTLS(config), nil
}

func init() {
	rootCmd.AddCommand(exampleCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// exampleCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// exampleCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
