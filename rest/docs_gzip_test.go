package rest_test

// Behavioral tests for the docs surface's gzip negotiation (#218). The docs
// handler serves the embedded Scalar bundle (~3.6 MB) and the OpenAPI spec; it
// must compress them when the client advertises Accept-Encoding: gzip and serve
// them verbatim when it does not — reusing the same GzipMiddleware the /api
// stack uses, not a second compression path. The recorder-based tests below
// cover gzip negotiation and decode-equality against the verbatim response (with
// a Content-Length header pin); the deferred gz.Close runs before ServeHTTP
// returns, so the recorder body holds the complete gzip stream. A recorder
// cannot observe the wire Content-Length, though, so the docs surface's
// stale-Content-Length behavior is pinned by the httptest.NewServer test in this
// file, on a length-enforcing transport; gzip_test.go covers the generic
// GzipMiddleware composition.

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/7cav/api/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docsGetGzip issues a docs request advertising Accept-Encoding: gzip. A
// recorder does not transparently decompress (only a real transport does), so
// the body holds the gzip stream directly for the test to decode. It is not a
// faithful wire image, though: a recorder enforces no Content-Length, so the
// genuine-transport check lives in the httptest.NewServer test below.
func docsGetGzip(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rr, req)
	return rr
}

// gunzip decodes a gzip stream whole, asserting an intact trailer (CRC + size).
func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err, "body must open as a gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err, "compressed stream must arrive whole, not truncated")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")
	return decoded
}

// A docs-asset request advertising gzip gets Content-Encoding: gzip and a body
// that decodes back to the exact bytes the verbatim (no-Accept-Encoding)
// response serves. The Scalar bundle is the large asset this issue is about.
func TestDocsHandler_GzipsBundleWhenNegotiated(t *testing.T) {
	h := rest.DocsHandler("dev")

	plain := docsGet(t, h, "/scalar.standalone.js")
	require.Equal(t, http.StatusOK, plain.Code)
	require.Empty(t, plain.Header().Get("Content-Encoding"),
		"the verbatim request advertises no encoding, so the response carries none")

	gz := docsGetGzip(t, h, "/scalar.standalone.js")
	require.Equal(t, http.StatusOK, gz.Code)
	require.Equal(t, "gzip", gz.Header().Get("Content-Encoding"),
		"a gzip-negotiated docs asset must be served compressed")
	require.Empty(t, gz.Header().Get("Content-Length"),
		"the stale uncompressed Content-Length http.FileServer sets must be stripped on first write")

	assert.Less(t, gz.Body.Len(), plain.Body.Len(),
		"the compressed bundle must be smaller than the verbatim bundle")
	assert.Equal(t, plain.Body.Bytes(), gunzip(t, gz.Body.Bytes()),
		"the compressed bundle must decode byte-for-byte to the verbatim asset")
}

// The served OpenAPI spec compresses on the same terms, and the build-time
// info.version stamping survives the compression: a gzip-negotiated spec
// request decodes to exactly the stamped verbatim spec, dev sentinel gone.
func TestDocsHandler_GzipsSpecWithVersionStampIntact(t *testing.T) {
	h := rest.DocsHandler("v2.5.0")

	plain := docsGet(t, h, "/openapi.yaml")
	require.Equal(t, http.StatusOK, plain.Code)
	require.Empty(t, plain.Header().Get("Content-Encoding"))

	gz := docsGetGzip(t, h, "/openapi.yaml")
	require.Equal(t, http.StatusOK, gz.Code)
	require.Equal(t, "gzip", gz.Header().Get("Content-Encoding"),
		"the served spec must compress when gzip is negotiated")
	require.Empty(t, gz.Header().Get("Content-Length"),
		"the compressed spec must carry no stale uncompressed Content-Length")

	decoded := gunzip(t, gz.Body.Bytes())
	assert.Equal(t, plain.Body.Bytes(), decoded,
		"the compressed spec must decode byte-for-byte to the verbatim spec")
	assert.Contains(t, string(decoded), "version: v2.5.0",
		"the build-time version stamp must survive compression")
	assert.NotContains(t, string(decoded), "version: dev",
		"the dev sentinel must stay replaced under compression")
}

// A docs request that does not advertise gzip is served verbatim: no
// Content-Encoding, and the body is the raw asset bytes, not a gzip stream.
// This is the half of the negotiation the existing docs tests already exercise
// implicitly (they send no Accept-Encoding); pin it explicitly for both the
// asset and the spec path so a regression that compresses unconditionally is
// caught.
func TestDocsHandler_ServesVerbatimWhenGzipNotNegotiated(t *testing.T) {
	h := rest.DocsHandler("dev")

	for _, path := range []string{"/scalar.standalone.js", "/openapi.yaml", "/"} {
		t.Run(path, func(t *testing.T) {
			rr := docsGet(t, h, path)
			require.Equal(t, http.StatusOK, rr.Code)
			assert.Empty(t, rr.Header().Get("Content-Encoding"),
				"an un-negotiated docs response must not be compressed")
			// The body must not be a gzip stream — its first bytes must not be
			// the gzip magic number (0x1f 0x8b). Assert the body is non-trivial
			// first, so a short/empty response fails loudly instead of skipping
			// the magic-byte check.
			require.GreaterOrEqual(t, rr.Body.Len(), 2,
				"the docs body must be non-trivial, not empty or truncated")
			b := rr.Body.Bytes()
			assert.False(t, b[0] == 0x1f && b[1] == 0x8b,
				"an un-negotiated body must be raw, not a gzip stream")
		})
	}
}

// The genuine wire check for acceptance criterion 4 (#218): over a real
// transport — which, unlike a recorder, enforces a declared Content-Length — a
// gzip-negotiated bundle request must carry no stale uncompressed
// Content-Length. The deterministic guard is the direct Content-Length header
// assertion below (require.Empty): http.FileServer sets Content-Length to the
// UNCOMPRESSED size, and this test fails the moment that stale value reaches the
// wire. The gunzip round-trip that follows is additional integrity
// verification — it confirms the asset decodes byte-for-byte — and only
// contingently doubles as a truncation catch: were the stale length left on,
// net/http would also close the connection short of the declared length and the
// client would hit unexpected EOF. This is the only construction that exercises
// the real GzipMiddleware+http.FileServer composition on a length-enforcing
// transport. Setting Accept-Encoding: gzip by hand suppresses the transport's
// transparent decompression, so the test reads the raw gzip stream with
// Content-Encoding intact; the verbatim asset is fetched over the same server
// WITHOUT the header, where the transport transparently yields the decoded
// bytes.
func TestDocsHandler_GzipBundleCarriesNoStaleContentLengthOnTheWire(t *testing.T) {
	srv := httptest.NewServer(rest.DocsHandler("dev"))
	defer srv.Close()

	// Compressed fetch: Accept-Encoding set by hand, so the transport leaves the
	// gzip stream raw and preserves Content-Encoding.
	gzReq, err := http.NewRequest(http.MethodGet, srv.URL+"/scalar.standalone.js", nil)
	require.NoError(t, err)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	gzRes, err := srv.Client().Do(gzReq)
	require.NoError(t, err)
	defer gzRes.Body.Close()

	require.Equal(t, http.StatusOK, gzRes.StatusCode)
	require.Equal(t, "gzip", gzRes.Header.Get("Content-Encoding"),
		"a gzip-negotiated bundle must be served compressed")
	require.Empty(t, gzRes.Header.Get("Content-Length"),
		"the stale uncompressed Content-Length must never reach the wire")

	zr, err := gzip.NewReader(gzRes.Body)
	require.NoError(t, err, "body must open as a gzip stream")
	decoded, err := io.ReadAll(zr)
	require.NoError(t, err,
		"compressed bundle must decode whole (byte-for-byte integrity check; the Content-Length header pin above is the primary stale-length guard)")
	require.NoError(t, zr.Close(), "gzip trailer (CRC + size) must be intact")

	// Verbatim fetch over the same server: no Accept-Encoding, so the transport
	// negotiates gzip and transparently decodes, yielding the raw asset bytes.
	verbatimRes, err := srv.Client().Get(srv.URL + "/scalar.standalone.js")
	require.NoError(t, err)
	defer verbatimRes.Body.Close()
	require.Equal(t, http.StatusOK, verbatimRes.StatusCode)
	verbatim, err := io.ReadAll(verbatimRes.Body)
	require.NoError(t, err)

	assert.Equal(t, verbatim, decoded,
		"the compressed bundle must decode byte-for-byte to the verbatim asset")
}
