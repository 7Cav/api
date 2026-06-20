package rest

// Docs UI + OpenAPI spec serving for the single-listener stack (#134). The old
// grpc-gateway served the embedded Swagger UI for every path outside /api; the
// cutover folds that role into this package so one public listener serves both
// the API (rest.New) and the docs.
//
// Phase 4 (#135) retired the generated Swagger 2.0 *.swagger.json files. The
// reference document is now the hand-owned OpenAPI 3.1 spec (openapi.Spec, from
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
	"mime"
	"net/http"

	"github.com/7cav/api/openapi"
)

// specURLPath is where the OpenAPI 3.1 document is served. The Swagger UI shell
// (assets/index.html) loads it by this relative path.
const specURLPath = "/openapi.yaml"

// specVersionSentinel is the info.version literal the hand-owned spec ships
// with (openapi/openapi.yaml: `version: dev`). DocsHandler rewrites it to the
// build-time version when serving the spec.
const specVersionSentinel = "version: dev"

// DocsHandler serves the embedded Swagger UI and the OpenAPI 3.1 spec at the
// same URLs the old gateway used (everything outside /api). The spec has its
// info.version stamped from version; all other assets are served verbatim from
// the embedded filesystem.
//
// version is the build-time var (servers.version via -ldflags, "dev" locally),
// threaded in by the composition root.
func DocsHandler(version string) http.Handler {
	// Match the old gateway: register the .svg MIME type so the favicons and
	// any inline SVG assets serve with the right content type.
	if err := mime.AddExtensionType(".svg", "image/svg+xml"); err != nil {
		Error.Println("failed to add MIME extension type for .svg: ", err)
	}
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

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == specURLPath {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(spec)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
