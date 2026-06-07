package rest

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Clean-path 307 normalization (ruled, #128 round 3 — enumerated deliberate
// break, PRD #112): net/http's ServeMux cleans every non-CONNECT request path
// before matching (findHandler: cleanPath — path.Clean plus
// preserve-trailing-slash) and answers an unclean path (A//B, A/../B) with a
// 307 to the cleaned form. The legacy gateway's {position_query=**} glob
// served those forms as 200s; the ruling ACCEPTS the redirect but not its
// incidental warts: net/http's RedirectHandler carries a default HTML body —
// off-contract for an all-JSON API — and bypasses routeLabel, metering under
// the empty label rest.go documents as "auth rejected before routing".
//
// cleanPathRedirect answers the mux's would-be clean-path redirect IN FRONT of
// the mux instead: same detection (the mux's own cleanPath semantics, copied
// below), same 307 status, same Location (computed through http.Redirect, the
// exact function the mux's RedirectHandler calls) — but the house JSON body
// and the bounded catch-all metering label. Every CLEAN path passes through
// untouched, so the mux's built-in clean-path redirect is unreachable behind
// this layer and nothing else changes: trailing-slash semantics are preserved
// by cleanPath itself (the search trailing-slash form /search/ stays the
// empty-query 400, the slashless /search registration still matches directly),
// and the fallback's 404/405 surfaces only ever see clean paths anyway.
func cleanPathRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CONNECT requests are not canonicalized — mux parity (findHandler
		// skips cleaning for CONNECT, so this layer must too).
		if r.Method != http.MethodConnect {
			escaped := r.URL.EscapedPath()
			if cleaned := cleanPath(escaped); cleaned != escaped {
				writeCleanPathRedirect(w, r, cleaned)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// writeCleanPathRedirect writes the clean-path 307 with the contract JSON
// body. Status and Location are exactly the mux's: the URL is built the way
// findHandler builds it (cleaned escaped path + original RawQuery) and handed
// to http.Redirect — the same function the mux's RedirectHandler uses — so the
// Location normalization (re-clean, non-ASCII hex-escaping) cannot diverge.
// Pre-setting Content-Type makes http.Redirect skip its HTML body and
// text/html header (it only writes those when the handler set no
// Content-Type), leaving the status line and Location untouched; the JSON body
// follows.
//
// Body shape: the house statusBody via the write.go marshal seam. Code chosen
// deliberately (mirrors the methodNotAllowed precedent): the frozen code→HTTP
// table has no code that yields a 3xx, and unlike the 405 there is no old
// wrong-method body to keep — the old stack served a 200 here. codeUnknown (2)
// is gRPC's conventional code for HTTP statuses outside its mapping; the
// message is net/http's status text for 307, the same words the default HTML
// body carried.
func writeCleanPathRedirect(w http.ResponseWriter, r *http.Request, cleaned string) {
	// Meter like the fallback meters its 404s/405s: the bounded catch-all
	// "/" route label — never the raw unclean path (attacker-controlled
	// cardinality) and never "" (that label means auth rejected the request
	// before routing; this request authenticated and reached routing's
	// doorstep). The key-id slot is already filled — auth runs outside this
	// layer.
	if labels := metricLabelsFromContext(r.Context()); labels != nil {
		labels.route = "/"
	}

	body, err := marshalJSON(statusBody{
		Code:    codeUnknown,
		Message: http.StatusText(http.StatusTemporaryRedirect),
		Details: []any{},
	})
	if err != nil {
		// statusBody cannot fail to marshal; guard against future edits
		// (same insurance as writeStatusJSON). The redirect still happens —
		// only the body falls back.
		Error.Printf("%s %s: marshaling redirect body failed: %v", r.Method, r.URL.Path, err)
		body = []byte(`{"code":2,"message":"Temporary Redirect","details":[]}`)
	}

	u := &url.URL{Path: cleaned, RawQuery: r.URL.RawQuery}
	w.Header().Set("Content-Type", "application/json")
	http.Redirect(w, r, u.String(), http.StatusTemporaryRedirect)
	if _, err := w.Write(body); err != nil {
		Error.Printf("%s %s: writing redirect response: %v", r.Method, r.URL.Path, err)
	}
}

// cleanPath returns the canonical path for p, eliminating . and .. elements —
// copied verbatim from net/http (server.go), where it is unexported. It MUST
// track the mux's cleaning exactly: cleanPathRedirect fires precisely when
// the mux's findHandler would have redirected, never otherwise.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	// path.Clean removes trailing slash except for root;
	// put the trailing slash back if necessary.
	if p[len(p)-1] == '/' && np != "/" {
		// Fast path for common case of p being the string we want:
		if len(p) == len(np)+1 && strings.HasPrefix(p, np) {
			np = p
		} else {
			np += "/"
		}
	}
	return np
}
