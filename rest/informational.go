package rest

// informational reports whether code is an informational (1xx) status.
//
// Informational responses precede the final response on the wire and never
// commit it — net/http's own response writer special-cases 1xx and leaves its
// wroteHeader latch false. Every writer wrapper in the chain with commit-time
// state (statusWriter's status capture, commitWriter's commit latch,
// cacheControlWriter's header stamp) must mirror that: forward a 1xx
// WriteHeader to the delegate and latch nothing, so the subsequent final
// WriteHeader behaves exactly as a first call (#165). A new wrapper with
// WriteHeader state starts here — and joins the shared table in
// informational_internal_test.go.
//
// The API itself emits no 1xx today; the guard exists so the wrappers stay
// faithful to net/http's commit semantics rather than to current traffic.
func informational(code int) bool {
	return code >= 100 && code <= 199
}
