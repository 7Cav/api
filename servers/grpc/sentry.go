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
	"time"

	"github.com/getsentry/sentry-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sentryFlushTimeout bounds how long a panicking request waits for its event
// to reach Sentry before the panic resumes (and, for gRPC, the process dies).
const sentryFlushTimeout = 2 * time.Second

// NewSentryInterceptor reports handler panics and Internal-class errors to
// Sentry, tagged with the route and the calling API key id (never the bearer
// token). Chained inside the auth interceptor so the key is already on ctx.
//
// Without an initialised Sentry client (no SENTRY_DSN) it is a pass-through:
// errors flow unchanged and panics propagate exactly as they do today.
// Panics are re-raised after capture either way — Phase 0 observes, it does
// not change crash semantics (PRD #112).
func NewSentryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		if sentry.CurrentHub().Client() == nil {
			return handler(ctx, req)
		}

		hub := sentry.CurrentHub().Clone()
		scope := hub.Scope()
		scope.SetTag("transport", "grpc")
		scope.SetTag("route", info.FullMethod)
		if key := KeyFromContext(ctx); key != nil {
			scope.SetTag("key_id", strconv.FormatUint(uint64(key.KeyId), 10))
		}

		defer func() {
			if r := recover(); r != nil {
				hub.RecoverWithContext(ctx, r) // event queued; ID unused — async transport
				// The panic will take the process down (grpc-go has no
				// recovery layer) — flush synchronously so the event
				// survives the crash.
				if !hub.Flush(sentryFlushTimeout) {
					Warn.Printf("sentry flush timed out — panic event for %s was likely dropped", info.FullMethod)
				}
				panic(r)
			}
		}()

		resp, err = handler(ctx, req)
		if err != nil {
			if code := status.Code(err); isServerErrorCode(code) {
				scope.SetTag("grpc_code", code.String())
				hub.CaptureException(err)
			}
		}
		return resp, err
	}
}

// isServerErrorCode reports whether a gRPC status code is Internal-class —
// i.e. it maps to an HTTP 5xx under the grpc-gateway translation. Client-side
// codes (NotFound, Unauthenticated, InvalidArgument, ...) are expected
// behavior, not errors worth a Sentry event.
func isServerErrorCode(code codes.Code) bool {
	switch code {
	case codes.Unknown, codes.DeadlineExceeded, codes.Unimplemented,
		codes.Internal, codes.Unavailable, codes.DataLoss:
		return true
	default:
		return false
	}
}
