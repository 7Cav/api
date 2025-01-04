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

package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const authServerURL = "https://auth.7cav.us/auth/realms/7cav/check?apiKey="

func ValidateToken(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	Info.Println("Checking metadata")

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		Warn.Println("Unauthorized: No metadata found")
		return nil, status.Errorf(codes.Unauthenticated, "missing metadata")
	}

	authHeaders := md.Get("authorization")
	if len(authHeaders) < 1 {
		Warn.Println("Unauthorized: Missing authorization header")
		return nil, status.Errorf(codes.Unauthenticated, "missing authorization token")
	}

	authHeader := strings.TrimSpace(authHeaders[0])
	if !isValidToken(authHeader) {
		Warn.Printf("Unauthorized attempt on method %s", info.FullMethod)
		return nil, status.Errorf(codes.Unauthenticated, "invalid token")
	}

	return handler(ctx, req)
}

func isValidToken(authHeader string) bool {
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		Warn.Println("Empty token provided")
		return false
	}
	if !isPrintableASCII(token) {
		Warn.Println("Token contains non-printable ASCII")
		return false
	}

	config, err := loadTLSConfig()
	if err != nil {
		Warn.Printf("Failed to load TLS config: %v", err)
		return false
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: config},
		Timeout:   5 * time.Second,
	}
	res, err := client.Get(authServerURL + token)
	if err != nil {
		Warn.Printf("Auth server unreachable: %v", err)
		return false
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			Warn.Printf("Error closing response body: %v", err)
		}
	}()

	return res.StatusCode == http.StatusOK
}

func loadTLSConfig() (*tls.Config, error) {
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		Warn.Printf("Could not load system CA pool: %v", err)
		return nil, err
	}
	return &tls.Config{RootCAs: rootCAs}, nil
}

func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 32 || r > 126 {
			return false
		}
	}
	return true
}
