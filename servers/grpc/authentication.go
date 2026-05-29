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
	"strconv"

	"github.com/7cav/api/datastores"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const maxTokenLen = 128

func NewAuthInterceptor(ds datastores.Datastore) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		peerAddr := "unknown"
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			peerAddr = p.Addr.String()
		}
		keyID := "none"
		defer func() {
			Info.Printf("[REQ] transport=grpc method=%s peer=%s key_id=%s", info.FullMethod, peerAddr, keyID)
		}()

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Errorf(codes.Unauthenticated, "missing metadata")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) < 1 {
			return nil, status.Errorf(codes.Unauthenticated, "missing authorization token")
		}

		token := datastores.ParseBearerToken(authHeaders[0], maxTokenLen)
		if token == "" {
			return nil, status.Errorf(codes.Unauthenticated, "missing authorization token")
		}

		key, err := ds.ValidateApiKey(token)
		if err != nil || key == nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid api key")
		}

		keyID = strconv.FormatUint(uint64(key.KeyId), 10)
		return handler(ContextWithKey(ctx, key), req)
	}
}
