package rest

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/spf13/viper"
)

// Client-IP resolution for the two 401 log sites (ADR 0005). Behind the prod
// reverse proxy (Cloudflare → nginx-proxy-manager) r.RemoteAddr is always the
// proxy, so the forwarding headers carry the real caller — but only when the
// socket peer is a configured trusted proxy. resolveClientIP is pure (no
// globals) so the full trust rule is exhaustively table-testable; clientIP is
// the thin ambient wrapper the log sites call over the cached set.

// trustedProxies is the parsed TRUSTED_PROXIES set, populated ONCE at startup
// by InitTrustedProxies and read by clientIP. nil/empty = trust nothing, which
// makes resolveClientIP ignore every forwarding header (the prior
// proxy-address logging behavior).
var trustedProxies []*net.IPNet

// parseTrustedProxies turns the comma-split TRUSTED_PROXIES specs into CIDR
// networks. A bare IP is normalized to a host route (/32 for v4, /128 for v6).
// Empty/whitespace-only entries are skipped so a trailing comma is harmless.
// Any non-empty malformed entry is an error — the caller makes it fatal.
func parseTrustedProxies(specs []string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, raw := range specs {
		spec := strings.TrimSpace(raw)
		if spec == "" {
			continue
		}
		if !strings.Contains(spec, "/") {
			ip := net.ParseIP(spec)
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy %q", spec)
			}
			if ip.To4() != nil {
				spec += "/32"
			} else {
				spec += "/128"
			}
		}
		_, n, err := net.ParseCIDR(spec)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", spec, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// isTrusted reports whether ip falls inside any trusted-proxy network.
func isTrusted(ip net.IP, trusted []*net.IPNet) bool {
	for _, n := range trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// peerIP extracts the bare IP from r.RemoteAddr, which is host:port (or
// [v6]:port) in production. It falls back to the original string only when
// splitting/parsing is impossible.
func peerIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	return host
}

// resolveClientIP implements the full ADR 0005 rule. Pure: no globals, the
// trusted set is passed in.
//
//   - Peer NOT trusted → forwarding headers are ignored, bare peer IP used
//     (an untrusted peer could forge them).
//   - Peer trusted → take the right-most X-Forwarded-For entry NOT in the
//     trusted set (walk right→left, skipping trusted hops). A malformed entry
//     stops the walk and marks XFF unusable. XFF absent/unusable → X-Real-IP.
//     That absent/invalid → bare peer IP.
//
// A single header value is read via Header.Get (no multi-header merge).
func resolveClientIP(r *http.Request, trusted []*net.IPNet) string {
	peer := peerIP(r.RemoteAddr)

	peerAddr := net.ParseIP(peer)
	if peerAddr == nil || !isTrusted(peerAddr, trusted) {
		return peer
	}

	if ip, ok := clientFromXFF(r.Header.Get("X-Forwarded-For"), trusted); ok {
		return ip
	}

	if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
		if net.ParseIP(real) != nil {
			return real
		}
	}

	return peer
}

// clientFromXFF walks an X-Forwarded-For value right→left, skipping entries in
// the trusted set, and returns the first (right-most) untrusted address. A
// malformed entry stops the walk and makes the whole header unusable (ok =
// false): the trail can't be reasoned about past a value we can't parse. ok is
// also false when the header is empty or every entry is a trusted hop.
func clientFromXFF(header string, trusted []*net.IPNet) (string, bool) {
	if strings.TrimSpace(header) == "" {
		return "", false
	}
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		entry := strings.TrimSpace(parts[i])
		ip := net.ParseIP(entry)
		if ip == nil {
			return "", false // malformed entry: XFF unusable
		}
		if isTrusted(ip, trusted) {
			continue // trusted hop appended by our own proxies
		}
		return entry, true
	}
	return "", false
}

// InitTrustedProxies reads/parses/caches TRUSTED_PROXIES once at startup.
// Returns an error on a non-empty-but-malformed value (the caller makes it
// fatal). Empty/unset trusts nothing.
func InitTrustedProxies() error {
	raw := viper.GetString("TRUSTED_PROXIES")
	nets, err := parseTrustedProxies(strings.Split(raw, ","))
	if err != nil {
		return err
	}
	trustedProxies = nets
	return nil
}

// clientIP is the thin ambient wrapper the two 401 log sites call. It resolves
// against the cached trusted set populated by InitTrustedProxies.
func clientIP(r *http.Request) string {
	return resolveClientIP(r, trustedProxies)
}
