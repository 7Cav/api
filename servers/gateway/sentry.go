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

package gateway

import (
	"fmt"
	"net/http"
	"strconv"

	grpcServices "github.com/7cav/api/servers/grpc"
	"github.com/getsentry/sentry-go"
)

// sentryMiddleware reports handler panics and 5xx responses to Sentry, tagged
// with the route and the calling API key id (never the bearer token). Wired
// inside authMiddleware so the validated key is already on the request ctx.
//
// Without an initialised Sentry client (no SENTRY_DSN) it is a pass-through:
// responses flow unchanged and panics propagate exactly as they do today
// (net/http recovers them per-connection). Panics are re-raised after capture
// either way — Phase 0 observes, it does not change crash semantics (PRD #112).
func sentryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sentry.CurrentHub().Client() == nil {
			next.ServeHTTP(w, r)
			return
		}

		hub := sentry.CurrentHub().Clone()
		scope := hub.Scope()
		scope.SetTag("transport", "http")
		scope.SetTag("route", r.URL.Path)
		if key := grpcServices.KeyFromContext(r.Context()); key != nil {
			scope.SetTag("key_id", strconv.FormatUint(uint64(key.KeyId), 10))
		}

		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rec := recover(); rec != nil {
				hub.RecoverWithContext(r.Context(), rec)
				// Re-raise: net/http's per-connection recovery handles it
				// exactly as it does today. The process survives, so the
				// async transport delivers the event — no flush needed.
				panic(rec)
			}
			if sw.status >= http.StatusInternalServerError {
				scope.SetTag("http_status", strconv.Itoa(sw.status))
				scope.SetLevel(sentry.LevelError)
				// Group by method+status, not URL: parameterized paths
				// (/profile/{id}) must not fan one failure into N Sentry
				// issues. The raw path stays available on the route tag.
				scope.SetFingerprint([]string{"http-5xx", r.Method, strconv.Itoa(sw.status)})
				hub.CaptureMessage(fmt.Sprintf("HTTP %d %s %s", sw.status, r.Method, r.URL.Path)) // event queued; ID unused — async transport
			}
		}()

		next.ServeHTTP(sw, r)
	})
}

// statusRecorder captures the status the client actually saw for the
// deferred 5xx check: the first explicit WriteHeader wins, and a body Write
// without one latches the implicit 200 — mirroring net/http, where any later
// WriteHeader is a superfluous no-op.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.wroteHeader {
		sr.status = code
		sr.wroteHeader = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

// Write latches the implicit 200 (status already defaults to it) so a buggy
// Write-then-WriteHeader(500) handler cannot record a status the client
// never received.
func (sr *statusRecorder) Write(b []byte) (int, error) {
	sr.wroteHeader = true
	return sr.ResponseWriter.Write(b)
}
