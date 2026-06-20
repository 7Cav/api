package rest

// Docs UI + OpenAPI spec serving for the single-listener stack (#134). The old
// grpc-gateway served the embedded Swagger UI for every path outside /api; the
// cutover folds that role into this package so one public listener serves both
// the API (rest.New) and the docs.
//
// info.version templating: the generated specs ship with the "dev" sentinel
// (proto/*.proto's `version: "dev";`). The release workflow used to sed the
// build tag into the proto before make generate; with serving in-process the
// build-time version is stamped onto the spec's info.version at request time
// instead, so a tagged binary reports its tag and a local `dev` build reports
// dev — no codegen step in the loop.

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"strings"

	"github.com/7cav/api/openapi"
)

// specVersionSentinel is the info.version literal the generated swagger specs
// ship with (proto/*.proto: `version: "dev";`). DocsHandler rewrites it to the
// build-time version on the two *.swagger.json files.
const specVersionSentinel = `"version": "dev"`

// DocsHandler serves the embedded Swagger UI and OpenAPI specs at the same URLs
// the old gateway used (everything outside /api). The two *.swagger.json specs
// have their info.version stamped from version; all other assets are served
// verbatim from the embedded filesystem.
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

	replacement := []byte(`"version": "` + version + `"`)
	stamp := version != "" && version != "dev"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if stamp && strings.HasSuffix(r.URL.Path, ".swagger.json") {
			name := strings.TrimPrefix(r.URL.Path, "/")
			raw, err := fs.ReadFile(sub, name)
			if err == nil {
				body := bytes.Replace(raw, []byte(specVersionSentinel), replacement, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
				return
			}
			// Fall through to the file server on any read miss (it produces the
			// canonical 404), rather than masking a routing change.
		}
		fileServer.ServeHTTP(w, r)
	})
}
