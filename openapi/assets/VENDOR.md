# Vendored docs renderer

The docs page (served at `/`) renders the OpenAPI 3.1 spec with
[Scalar](https://scalar.com). The renderer is vendored here as a single
standalone bundle and embedded via `//go:embed` (see `../openapi.go`); the docs
page makes no runtime CDN request.

| | |
|---|---|
| Package | `@scalar/api-reference` (standalone build) |
| Pinned version | `1.60.0` |
| File | `scalar.standalone.js` |
| License | MIT (see `LICENSE`) |

## Source URL

```
https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.60.0/dist/browser/standalone.js
```

## Re-vendor

Bump the pinned version in all three places it appears (the table above, the
Source URL, and the curl command below), then re-download:

```sh
curl -fsSL "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.60.0/dist/browser/standalone.js" -o scalar.standalone.js
```

After re-vendoring, refresh `LICENSE` from upstream if it changed, run
`go test ./...`, and smoke `/` against the served spec. The shell pins
`agent: { disabled: true }` so there is no third-party AI egress. Keep it.
