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
