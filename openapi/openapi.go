package openapi

import "embed"

// Files holds the Scalar docs shell and its static assets served at the docs
// URLs: the shell (assets/index.html), the vendored Scalar standalone bundle
// (assets/scalar.standalone.js), and the 7Cav theme (assets/theme.css). The
// renderer is pinned and vendored — see assets/VENDOR.md for the version,
// source URL, and re-vendor command; there is no runtime CDN request.
//
// Phase 4 (#135) retired the generated Swagger 2.0 *.swagger.json files; the
// reference document is now the hand-owned OpenAPI 3.1 spec (Spec, below),
// served by rest.DocsHandler. The shell in assets/index.html renders it with
// Scalar (#215), which supports OpenAPI 3.1 natively.
//
//go:embed assets
var Files embed.FS

// Spec is the hand-owned OpenAPI 3.1 document (PRD #112, issue #121). It is the
// reference contract, validated against the golden corpus by
// contract/spec_test.go and served live by rest.DocsHandler. The embed is
// package-local (the file lives alongside this source), so the docs serving
// needs no codegen step.
//
//go:embed openapi.yaml
var Spec []byte
