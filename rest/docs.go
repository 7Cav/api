package rest

// Docs UI + OpenAPI spec serving for the single-listener stack (#134). The old
// grpc-gateway served the embedded docs UI for every path outside /api; the
// cutover folds that role into this package so one public listener serves both
// the API (rest.New) and the docs.
//
// The docs UI is Scalar (vendored standalone build, embedded under assets/),
// which renders the hand-owned OpenAPI 3.1 spec natively (#215; the prior
// Swagger UI bundle predated 3.1 and rejected the document). The shell
// (assets/index.html) mounts Scalar against the served spec with the hosted
// "Ask AI" assistant disabled, so nothing on the docs page leaves 7Cav
// infrastructure.
//
// Phase 4 (#135) retired the generated Swagger 2.0 *.swagger.json files. The
// reference document is the hand-owned OpenAPI 3.1 spec (openapi.Spec, from
// openapi/openapi.yaml), validated against the golden corpus by
// contract/spec_test.go and served here at /openapi.yaml. The #117 ruling
// confirmed no consumer codegens from the served spec, so the 3.1 document
// replaces the 2.0 files outright — no frozen 2.0 alias.
//
// info.version templating: the spec ships with the "dev" sentinel
// (openapi/openapi.yaml: `version: dev`). The release workflow used to sed the
// build tag into the proto before make generate; with serving in-process the
// build-time version is stamped onto the spec's info.version at request time
// instead, so a tagged binary reports its tag and a local `dev` build reports
// dev — no codegen step in the loop.

import (
	"bytes"
	"io/fs"
	"net/http"

	"github.com/7cav/api/openapi"
)

// specURLPath is where the OpenAPI 3.1 document is served. The docs shell
// (assets/index.html) loads it by this relative path.
const specURLPath = "/openapi.yaml"

// specVersionSentinel is the info.version literal the hand-owned spec ships
// with (openapi/openapi.yaml: `version: dev`). DocsHandler rewrites it to the
// build-time version when serving the spec.
const specVersionSentinel = "version: dev"

// DocsHandler serves the embedded Scalar docs UI and the OpenAPI 3.1 spec at
// the same URLs the old gateway used (everything outside /api). The spec has
// its info.version stamped from version; all other assets (the Scalar shell,
// its vendored bundle, and the theme) are served verbatim from the embedded
// filesystem.
//
// version is the build-time var (servers.version via -ldflags, "dev" locally),
// threaded in by the composition root.
func DocsHandler(version string) http.Handler {
	sub, err := fs.Sub(openapi.Files, "assets")
	if err != nil {
		// Unreachable: the embed always contains assets/. Loud if it ever isn't.
		Error.Fatalf("creating OpenAPI sub-filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(sub))

	// Stamp the build-time version onto the served spec, replacing the dev
	// sentinel. A dev build serves the spec untouched.
	spec := openapi.Spec
	if version != "" && version != "dev" {
		spec = bytes.Replace(spec, []byte(specVersionSentinel),
			[]byte("version: "+version), 1)
	}

	// GzipMiddleware compresses both the spec and the static assets (the ~3.6 MB
	// Scalar bundle is the one that matters) when the client advertises gzip, the
	// same negotiation the /api stack runs. The docs handler owns its own
	// compression so the public-listener split stays a plain path dispatch and
	// only the docs surface changes. The file-server shapes this surface actually
	// produces — the stale uncompressed Content-Length http.FileServer sets, and
	// HEAD — the middleware already handles. (It is also hardened against the
	// bodyless 304/204 shapes, but this surface never mints them: the embedded FS
	// carries no modtime or ETag, so http.FileServer sends no validators and
	// answers no conditional GET with a 304.) A client that does not negotiate
	// gzip is served the bytes verbatim.
	return GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == specURLPath {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(spec)
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
}
