package rest_test

// Behavioral tests for the docs surface's gzip negotiation (#218). The docs
// handler serves the embedded Scalar bundle (~3.6 MB) and the OpenAPI spec; it
// must compress them when the client advertises Accept-Encoding: gzip and serve
// them verbatim when it does not — reusing the same GzipMiddleware the /api
// stack uses, not a second compression path. A ResponseRecorder is enough here:
// the deferred gz.Close runs before ServeHTTP returns, so the recorder body
// holds the complete gzip stream, and decode-equality against the verbatim
// response is the acceptance check (the middleware's stale-Content-Length wire
// behavior is pinned crisply in gzip_test.go).

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

// docsGetGzip issues a docs request advertising Accept-Encoding: gzip. The
// recorder does not transparently decompress (only a real transport does), so
// the body is the raw response bytes exactly as the wire would carry them.
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

	decoded := gunzip(t, gz.Body.Bytes())
	assert.Equal(t, plain.Body.Bytes(), decoded,
		"the compressed spec must decode byte-for-byte to the verbatim spec")
	assert.Contains(t, string(decoded), "version: v2.5.0",
		"the build-time version stamp must survive compression")
	assert.NotContains(t, string(decoded), "version: dev",
		"the dev sentinel must stay replaced under compression")
}

// A docs request that does not advertise gzip is served verbatim — no
// Content-Encoding, byte-for-byte as before #218. This is the half of the
// negotiation the existing docs tests already exercise implicitly (they send no
// Accept-Encoding); pin it explicitly for both the asset and the spec path so a
// regression that compresses unconditionally is caught.
func TestDocsHandler_ServesVerbatimWhenGzipNotNegotiated(t *testing.T) {
	h := rest.DocsHandler("dev")

	for _, path := range []string{"/scalar.standalone.js", "/openapi.yaml", "/"} {
		t.Run(path, func(t *testing.T) {
			rr := docsGet(t, h, path)
			require.Equal(t, http.StatusOK, rr.Code)
			assert.Empty(t, rr.Header().Get("Content-Encoding"),
				"an un-negotiated docs response must not be compressed")
			// The body must not be a gzip stream — its first bytes must not be
			// the gzip magic number (0x1f 0x8b).
			if rr.Body.Len() >= 2 {
				b := rr.Body.Bytes()
				assert.False(t, b[0] == 0x1f && b[1] == 0x8b,
					"an un-negotiated body must be raw, not a gzip stream")
			}
		})
	}
}
