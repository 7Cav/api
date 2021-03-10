package grpc

import (
	"context"
	"crypto/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"net/http"
	"strings"
)

func ValidateToken(ctx context.Context, req interface{}, grpc *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	Info.Println("checking metadata")

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		Warn.Println("no metadata found on request.. this could be bad")
		return nil, status.Errorf(codes.InvalidArgument, "missing metadata")
	}

	if !valid(md.Get("authorization")) {
		Warn.Println("unauthorized attempt on ", grpc.FullMethod)
		return nil, status.Errorf(codes.Unauthenticated, "invalid token")
	}

	return handler(ctx, req)
}

func valid(authorization []string) bool {
	if len(authorization) < 1 {
		return false
	}

	token := strings.TrimPrefix(authorization[0], "Bearer ")

	// don't worry about insecure cert on auth host
	config := &tls.Config{
		InsecureSkipVerify: true,
	}
	tr := &http.Transport{TLSClientConfig: config}
	client := &http.Client{Transport: tr}
	res, err := client.Get("https://auth.7cav.us/auth/realms/7cav/check?apiKey=" + token)

	if err != nil {
		Warn.Println("error when calling auth server for token ", err)
		return false
	}

	return res.StatusCode == 200
}