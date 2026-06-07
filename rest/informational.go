package rest

import "net/http"

// informational reports whether code is an informational status that
// net/http forwards WITHOUT committing the response.
//
// That set is 1xx minus 101: net/http's own response writer routes
// `code >= 100 && code <= 199 && code != StatusSwitchingProtocols` through
// its informational path, leaving its wroteHeader latch false — but on a 101
// it SETS wroteHeader and commits ("We shouldn't send any further headers
// after 101", per the stdlib comment), dropping everything after from the
// wire — wire-silent only: the server logs each superfluous WriteHeader, and
// Write returns http.ErrBodyNotAllowed.
// This predicate mirrors that exactly, 101 carve-out included: excluding 101
// here means the wrappers latch on it just as the stdlib does, so e.g. a
// post-101 panic re-panics instead of "writing" a contract 500 the stdlib
// would swallow. Every writer wrapper in the new-stack rest chain with
// commit-time state (statusWriter's status capture, commitWriter's commit
// latch, cacheControlWriter's header stamp, gzipResponseWriter's
// Content-Length strip and status latch — #175) must mirror that: forward a
// non-latching 1xx WriteHeader to the delegate and latch nothing, so the
// subsequent final WriteHeader behaves exactly as a first call (#165). The
// legacy gateway's statusRecorder still latches on all 1xx — known, tracked
// in #176, dies at cutover. A new rest-chain wrapper with WriteHeader state
// starts here — and joins the shared table in informational_internal_test.go.
//
// (HTTP/2 would treat all 1xx informationally, but RFC 9113 removes 101 from
// HTTP/2 entirely, and this server is plain HTTP/1.1 — latching on 101 is
// safe on both.)
//
// No handler calls WriteHeader with a 1xx today (net/http's automatic
// Expect:100-continue reply bypasses the wrapper chain entirely); the guard
// exists so the wrappers stay faithful to net/http's commit semantics rather
// than to current traffic.
func informational(code int) bool {
	return code >= 100 && code <= 199 && code != http.StatusSwitchingProtocols
}
