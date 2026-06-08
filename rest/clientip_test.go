package rest

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

// withTrustedProxiesEnv sets the TRUSTED_PROXIES viper key for one test and
// restores it (plus the cached set) afterwards, matching the ambient
// viper.Set + Cleanup idiom the sentry tests use.
func withTrustedProxiesEnv(t *testing.T, value string) {
	t.Helper()
	prior := viper.GetString("TRUSTED_PROXIES")
	priorSet := trustedProxies
	viper.Set("TRUSTED_PROXIES", value)
	t.Cleanup(func() {
		viper.Set("TRUSTED_PROXIES", prior)
		trustedProxies = priorSet
	})
}

// mustCIDRs parses the given CIDR / bare-IP strings into the cached
// trusted-set shape resolveClientIP consumes. Bare IPs are normalized to a
// host route (/32 or /128) — the same normalization InitTrustedProxies does.
func mustCIDRs(t *testing.T, specs ...string) []*net.IPNet {
	t.Helper()
	nets, err := parseTrustedProxies(specs)
	require.NoError(t, err)
	return nets
}

// reqWith builds a request with the given RemoteAddr and optional headers
// (key/value pairs) so resolveClientIP can be table-tested without a server.
func reqWith(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestResolveClientIP_UntrustedPeer_IgnoresHeaders(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	r := reqWith("203.0.113.7:54321", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
		"X-Real-IP":       "5.6.7.8",
	})
	require.Equal(t, "203.0.113.7", resolveClientIP(r, trusted))
}

func TestResolveClientIP_TrustedPeer_ValidXFF_RightmostUntrusted(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// peer is a trusted hop; XFF = <client>, <trusted hop appended by proxy>.
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "203.0.113.9, 10.0.0.4",
	})
	require.Equal(t, "203.0.113.9", resolveClientIP(r, trusted))
}

func TestResolveClientIP_TrustedPeer_SpoofedLeftmost_ReturnsRightmostUntrusted(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// Attacker pre-seeds a spoofed left-most entry; the real client and the
	// trusted hop are appended to the right by our proxies. The right-most
	// untrusted entry (the proxy-observed client) wins, not the spoof.
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "9.9.9.9, 203.0.113.9, 10.0.0.4",
	})
	require.Equal(t, "203.0.113.9", resolveClientIP(r, trusted))
}

func TestResolveClientIP_TrustedPeer_MalformedXFF_FallsBackToRealIP(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// A malformed entry stops the walk and marks XFF unusable → X-Real-IP.
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "not-an-ip, 10.0.0.4",
		"X-Real-IP":       "198.51.100.7",
	})
	require.Equal(t, "198.51.100.7", resolveClientIP(r, trusted))
}

func TestResolveClientIP_TrustedPeer_AllTrustedXFF_ReturnsRealIP(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// Every XFF entry is a trusted hop → XFF yields nothing → X-Real-IP.
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "10.0.0.3, 10.0.0.4",
		"X-Real-IP":       "198.51.100.7",
	})
	require.Equal(t, "198.51.100.7", resolveClientIP(r, trusted))
}

func TestResolveClientIP_TrustedPeer_NoUsableXFFNoRealIP_FallsBackToPeer(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// XFF all-trusted (nothing usable) and no X-Real-IP → bare peer IP.
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "10.0.0.3",
	})
	require.Equal(t, "10.0.0.5", resolveClientIP(r, trusted))
}

func TestResolveClientIP_TrustedPeer_InvalidRealIP_FallsBackToPeer(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// No XFF, X-Real-IP is garbage → bare peer IP.
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Real-IP": "garbage",
	})
	require.Equal(t, "10.0.0.5", resolveClientIP(r, trusted))
}

func TestResolveClientIP_IPv6PeerAndCIDR_ResolvesXFF(t *testing.T) {
	// Trusted proxy hops live in 2001:db8::/64; the client (2001:db9::1234) is
	// outside it, so the right-most untrusted XFF entry wins.
	trusted := mustCIDRs(t, "2001:db8::/64")
	r := reqWith("[2001:db8::5]:443", map[string]string{
		"X-Forwarded-For": "2001:db9::1234, 2001:db8::4",
	})
	require.Equal(t, "2001:db9::1234", resolveClientIP(r, trusted))
}

func TestResolveClientIP_BareV6PeerNoPort_YieldsBareIP(t *testing.T) {
	trusted := mustCIDRs(t, "10.0.0.0/8")
	// RemoteAddr without a port can't be SplitHostPort'd; the original string
	// (already a bare IP) is returned. Untrusted → headers ignored.
	r := reqWith("203.0.113.7", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
	})
	require.Equal(t, "203.0.113.7", resolveClientIP(r, trusted))
}

func TestResolveClientIP_EmptyTrustedSet_HonorsNoHeader(t *testing.T) {
	// Empty/unset TRUSTED_PROXIES trusts nothing — every peer is untrusted,
	// so headers are never honored (prior proxy-address behavior).
	r := reqWith("10.0.0.5:443", map[string]string{
		"X-Forwarded-For": "203.0.113.9",
		"X-Real-IP":       "198.51.100.7",
	})
	require.Equal(t, "10.0.0.5", resolveClientIP(r, nil))
}

func TestParseTrustedProxies_BareIPsNormalizedToHostRoutes(t *testing.T) {
	nets, err := parseTrustedProxies([]string{"10.0.0.4", "2001:db8::4"})
	require.NoError(t, err)
	require.Len(t, nets, 2)
	require.Equal(t, "10.0.0.4/32", nets[0].String())
	require.Equal(t, "2001:db8::4/128", nets[1].String())
}

func TestParseTrustedProxies_SkipsEmptyEntries(t *testing.T) {
	// A trailing/leading comma or whitespace-only entry is harmless.
	nets, err := parseTrustedProxies([]string{"10.0.0.0/8", " ", ""})
	require.NoError(t, err)
	require.Len(t, nets, 1)
}

func TestParseTrustedProxies_MalformedEntryErrors(t *testing.T) {
	_, err := parseTrustedProxies([]string{"10.0.0.0/8", "not-a-cidr"})
	require.Error(t, err)
}

func TestInitTrustedProxies_EmptyTrustsNothing(t *testing.T) {
	withTrustedProxiesEnv(t, "")
	require.NoError(t, InitTrustedProxies())
	require.Empty(t, trustedProxies)
}

func TestInitTrustedProxies_NonEmptyMalformedReturnsError(t *testing.T) {
	withTrustedProxiesEnv(t, "10.0.0.0/8,bogus")
	require.Error(t, InitTrustedProxies())
}

func TestInitTrustedProxies_ParsesAndCaches(t *testing.T) {
	withTrustedProxiesEnv(t, "10.0.0.0/8, 192.168.1.1")
	require.NoError(t, InitTrustedProxies())
	require.Len(t, trustedProxies, 2)
}
