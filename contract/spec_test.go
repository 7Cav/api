package contract

// The hand-owned OpenAPI 3.1 reference spec (PRD #112, issue #121) is an
// executable artifact: every committed golden is replayed against it and the
// battery/spec route coverage is asserted in both directions. These tests are
// the enforcement loop — spec drift fails CI with a message naming the
// operation and field.
//
// The spec lives at openapi/openapi.yaml. Validation uses
// pb33f/libopenapi-validator (test-only dependency), chosen because it is the
// maintained Go validator with real OpenAPI 3.1 support (kin-openapi remains
// 3.0-only).

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	validator "github.com/pb33f/libopenapi-validator"
	"github.com/pb33f/libopenapi-validator/errors"
	"github.com/pb33f/libopenapi-validator/paths"
	"github.com/pb33f/libopenapi-validator/responses"
	"github.com/pb33f/libopenapi-validator/schema_validation"
)

const specPath = "../openapi/openapi.yaml"

// loadSpec parses the hand-owned OpenAPI document and returns its v3 model.
func loadSpec(t *testing.T) (libopenapi.Document, *v3.Document) {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err, "hand-owned OpenAPI spec missing at %s", specPath)

	doc, err := libopenapi.NewDocument(raw)
	require.NoError(t, err, "spec does not parse as an OpenAPI document")

	model, errs := doc.BuildV3Model()
	require.Empty(t, errs, "spec does not build as an OpenAPI 3.x model")
	return doc, &model.Model
}

// TestSpec_IsOpenAPI31AndMetaValid pins the document version (3.1 — the
// http/bearer scheme #91 wanted is inexpressible in Swagger 2.0, and 3.1 is
// what the chosen validator natively supports) and validates the document
// against the OpenAPI meta-schema itself.
func TestSpec_IsOpenAPI31AndMetaValid(t *testing.T) {
	doc, model := loadSpec(t)

	assert.True(t, strings.HasPrefix(model.Version, "3.1"),
		"spec must be OpenAPI 3.1, got %q", model.Version)

	valid, errs := schema_validation.ValidateOpenAPIDocument(doc)
	for _, e := range errs {
		t.Errorf("meta-schema violation: %s — %s", e.Message, e.Reason)
		for _, s := range e.SchemaValidationErrors {
			t.Errorf("  at %s: %s", s.FieldPath, s.Reason)
		}
	}
	assert.True(t, valid, "spec must validate against the OpenAPI 3.1 meta-schema")
}

// TestSpec_BearerSecurityScheme is the #91 fix: the security scheme is
// http/bearer (Swagger UI auto-prepends "Bearer "), declared once and
// required globally.
func TestSpec_BearerSecurityScheme(t *testing.T) {
	_, model := loadSpec(t)

	require.NotNil(t, model.Components, "spec must declare components")
	schemes := model.Components.SecuritySchemes
	require.Equal(t, 1, orderedmap.Len(schemes), "exactly one security scheme")

	bearer := schemes.GetOrZero("bearer")
	require.NotNil(t, bearer, "security scheme must be named 'bearer'")
	assert.Equal(t, "http", bearer.Type, "scheme type must be http (not apiKey)")
	assert.Equal(t, "bearer", bearer.Scheme, "scheme must be bearer")

	require.Len(t, model.Security, 1, "one global security requirement")
	req := model.Security[0].Requirements
	first := orderedmap.First(req)
	require.NotNil(t, first, "global security requirement must not be empty")
	assert.Equal(t, "bearer", first.Key(), "global security must reference the bearer scheme")
}

// TestSpec_KeycloakSurfaceAbsent: the keycloak lookup route and the
// keycloakId profile fields die at cutover; the reference spec must not
// mention them anywhere.
func TestSpec_KeycloakSurfaceAbsent(t *testing.T) {
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(raw)), "keycloak",
		"keycloak surface must be absent from the hand-owned spec")
}

// TestSpec_WireConventions pins the documented house style structurally:
// every schema property is lowerCamelCase (protobufAny's "@type" is the one
// historical exception), and every enum schema leads with its _UNSPECIFIED
// zero name (protojson zero-safe enum-name convention).
func TestSpec_WireConventions(t *testing.T) {
	_, model := loadSpec(t)
	require.NotNil(t, model.Components)

	lowerCamel := regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

	var walk func(path string, sp *base.SchemaProxy)
	walk = func(path string, sp *base.SchemaProxy) {
		if sp == nil || sp.IsReference() {
			return // referenced schemas are checked at their definition site
		}
		s := sp.Schema()
		if s == nil {
			return
		}
		for pair := orderedmap.First(s.Properties); pair != nil; pair = pair.Next() {
			name := pair.Key()
			if name != "@type" {
				assert.True(t, lowerCamel.MatchString(name),
					"%s: property %q is not lowerCamelCase", path, name)
			}
			walk(path+"."+name, pair.Value())
		}
		if len(s.Enum) > 0 {
			first := fmt.Sprintf("%v", s.Enum[0].Value)
			assert.True(t, strings.HasSuffix(first, "_UNSPECIFIED"),
				"%s: enum must lead with its _UNSPECIFIED zero name, got %q", path, first)
		}
		if s.Items != nil && s.Items.IsA() {
			walk(path+"[]", s.Items.A)
		}
		if s.AdditionalProperties != nil && s.AdditionalProperties.IsA() {
			walk(path+".*", s.AdditionalProperties.A)
		}
		for i, sub := range s.OneOf {
			walk(fmt.Sprintf("%s.oneOf[%d]", path, i), sub)
		}
		for i, sub := range s.AnyOf {
			walk(fmt.Sprintf("%s.anyOf[%d]", path, i), sub)
		}
	}

	for pair := orderedmap.First(model.Components.Schemas); pair != nil; pair = pair.Next() {
		walk("components.schemas."+pair.Key(), pair.Value())
	}
}

// --- Golden replay against the spec --------------------------------------

// classifySpecPath maps a battery case path (query string stripped) to its
// spec path template (publicRoutes in routes_test.go), or "" when the path
// belongs to no spec route.
func classifySpecPath(casePath string) string {
	if i := strings.IndexByte(casePath, '?'); i >= 0 {
		casePath = casePath[:i]
	}
	for _, r := range publicRoutes {
		if r.pattern.MatchString(casePath) {
			return r.specPath
		}
	}
	return ""
}

// offSpecCases record behavior of paths that deliberately do NOT exist in
// the spec: the unknown-path 404/401 tier. OpenAPI describes operations;
// non-operation surface stays corpus-only. The replay loop asserts these
// paths stay unmatched, so the spec cannot silently grow a route that
// swallows them.
var offSpecCases = map[string]string{
	"auth/unknown_path_authenticated":   "unknown path under the API prefix: JSON 404 is mux behavior, not an operation",
	"auth/unknown_path_unauthenticated": "unknown path without auth: 401 tier fires before routing",
}

// templateUnmatchableCases are goldens whose concrete request path cannot be
// matched by any OpenAPI path template — the gateway's {positionQuery=**}
// glob swallows slashes and empty segments, which OpenAPI cannot express.
// Their operation is resolved by name instead; request-side validation is
// skipped (the spec documents the canonical single-segment form), response
// validation still runs against the operation.
var templateUnmatchableCases = map[string]string{
	"position/search_multi_segment":  "/api/v1/milpacs/position/search/{positionQuery}",
	"position/search_trailing_slash": "/api/v1/milpacs/position/search/{positionQuery}",
}

// specViolatingRequests are goldens whose request deliberately breaks the
// documented request contract; the replay loop asserts request validation
// FAILS for them — proving the spec's parameter and security constraints
// describe the same gate the API enforces (each records a 4xx response).
var specViolatingRequests = map[string]string{
	"auth/milpacs_missing_header":       "no Authorization header: violates the bearer security requirement",
	"auth/milpacs_raw_key":              "raw key without Bearer scheme: violates the bearer security requirement",
	"auth/tickets_missing_header":       "no Authorization header: violates the bearer security requirement",
	"auth/tickets_raw_key":              "raw key without Bearer scheme: violates the bearer security requirement",
	"milpacs/profile_by_id_parse_error": "non-numeric userId violates the uint64-as-string path parameter",
	"roster/bogus_enum":                 "IMAGINARY_ROSTER is neither an enum name nor a number",
	"tickets/get_by_id_parse_error":     "non-numeric ticketId violates the integer path parameter",
	"tickets/messages_parse_error":      "non-numeric ticketId violates the integer path parameter",
}

// formatValidationErrors renders validator findings as one message per line,
// naming the operation and — for schema failures — the exact field.
func formatValidationErrors(operationID string, errs []*errors.ValidationError) string {
	var b strings.Builder
	for _, e := range errs {
		fmt.Fprintf(&b, "operation %s: [%s/%s] %s — %s\n", operationID, e.ValidationType, e.ValidationSubType, e.Message, e.Reason)
		for _, s := range e.SchemaValidationErrors {
			fmt.Fprintf(&b, "  field %s: %s\n", s.FieldPath, s.Reason)
		}
	}
	return b.String()
}

// caseRequest reconstructs the recorded HTTP request for a battery case,
// exactly as RunCase sends it.
func caseRequest(c Case) *http.Request {
	req := httptest.NewRequest(c.Method, c.Path, nil)
	if v, ok := authHeader(c.Auth); ok {
		req.Header.Set("Authorization", v)
	}
	return req
}

// goldenResponse reconstructs the recorded HTTP response for a golden.
func goldenResponse(g *Golden) *http.Response {
	h := http.Header{}
	for k, v := range g.Header {
		h.Set(k, v)
	}
	var body []byte
	if g.Body != nil {
		body = g.Body
	} else if g.BodyText != nil {
		body = []byte(*g.BodyText)
	}
	return &http.Response{
		StatusCode: g.Status,
		Header:     h,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

// operationID names an operation for failure messages.
func operationID(pathItem *v3.PathItem, specPath string) string {
	if pathItem != nil && pathItem.Get != nil && pathItem.Get.OperationId != "" {
		return pathItem.Get.OperationId
	}
	return "GET " + specPath
}

// TestSpec_GoldenReplay is the executable-spec loop: every committed golden
// is replayed against the OpenAPI document. Request validation runs where
// the request is expressible (and is asserted to FAIL for the deliberately
// contract-violating requests); response validation — status code, content
// type, body schema — runs for every golden. Failures name the operation and
// the field.
func TestSpec_GoldenReplay(t *testing.T) {
	doc, model := loadSpec(t)
	v, errs := validator.NewValidator(doc)
	require.Empty(t, errs, "building validator")
	respValidator := responses.NewResponseBodyValidator(model)

	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			golden, err := LoadGolden(goldensDir, c.Name)
			require.NoError(t, err)
			req := caseRequest(c)

			if reason, off := offSpecCases[c.Name]; off {
				pathItem, _, _ := paths.FindPath(req, model, nil)
				assert.Nil(t, pathItem,
					"case %s must stay off-spec (%s) but matched a spec path", c.Name, reason)
				return
			}

			specPath := classifySpecPath(c.Path)
			require.NotEmpty(t, specPath, "case %s: path %s matches no known route", c.Name, c.Path)

			var pathItem *v3.PathItem
			skipRequestValidation := false
			if tmpl, ok := templateUnmatchableCases[c.Name]; ok {
				specPath = tmpl
				pathItem = model.Paths.PathItems.GetOrZero(tmpl)
				require.NotNil(t, pathItem, "case %s: spec is missing path %s", c.Name, tmpl)
				skipRequestValidation = true
			} else {
				found, ferrs, tmpl := paths.FindPath(req, model, nil)
				require.NotNil(t, found, "case %s: spec has no path matching %s:\n%s",
					c.Name, c.Path, formatValidationErrors("GET "+specPath, ferrs))
				require.Equal(t, specPath, tmpl,
					"case %s: matched the wrong spec path", c.Name)
				pathItem = found
			}
			opID := operationID(pathItem, specPath)

			if !skipRequestValidation {
				valid, verrs := v.ValidateHttpRequestSyncWithPathItem(req, pathItem, specPath)
				if reason, bad := specViolatingRequests[c.Name]; bad {
					assert.False(t, valid,
						"case %s: request must violate the spec (%s) but validated clean", c.Name, reason)
				} else {
					assert.True(t, valid, "case %s: request does not validate:\n%s",
						c.Name, formatValidationErrors(opID, verrs))
				}
			}

			resp := goldenResponse(golden)
			valid, verrs := respValidator.ValidateResponseBodyWithPathItem(req, resp, pathItem, specPath)
			assert.True(t, valid, "case %s: recorded response does not validate:\n%s",
				c.Name, formatValidationErrors(opID, verrs))
		})
	}
}

// TestSpec_EveryOperationHasGolden is coverage direction A: every operation
// in the spec is exercised by at least one golden, so the document cannot
// describe surface the corpus does not witness.
func TestSpec_EveryOperationHasGolden(t *testing.T) {
	_, model := loadSpec(t)

	covered := map[string]bool{}
	for _, c := range Cases() {
		if _, off := offSpecCases[c.Name]; off {
			continue
		}
		if tmpl, ok := templateUnmatchableCases[c.Name]; ok {
			covered["GET "+tmpl] = true
			continue
		}
		if specPath := classifySpecPath(c.Path); specPath != "" {
			covered["GET "+specPath] = true
		}
	}

	total := 0
	for pair := orderedmap.First(model.Paths.PathItems); pair != nil; pair = pair.Next() {
		specPath := pair.Key()
		item := pair.Value()
		for method, op := range item.GetOperations().FromOldest() {
			total++
			key := strings.ToUpper(method) + " " + specPath
			assert.True(t, covered[key],
				"operation %s (%s) has no golden — record one or remove the operation",
				operationID(item, specPath), key)
			_ = op
		}
	}
	assert.NotZero(t, total, "spec declares no operations")
}

// TestSpec_EveryGoldenRouteInSpec is coverage direction B: every golden's
// route resolves to a spec operation (off-spec carve-outs excepted), so the
// API cannot serve surface the document does not describe.
func TestSpec_EveryGoldenRouteInSpec(t *testing.T) {
	_, model := loadSpec(t)

	for _, c := range Cases() {
		if _, off := offSpecCases[c.Name]; off {
			continue
		}
		specPath := classifySpecPath(c.Path)
		if tmpl, ok := templateUnmatchableCases[c.Name]; ok {
			specPath = tmpl
		}
		require.NotEmpty(t, specPath, "case %s: path %s matches no known route", c.Name, c.Path)

		item := model.Paths.PathItems.GetOrZero(specPath)
		assert.NotNil(t, item, "case %s: golden route %s missing from the spec", c.Name, specPath)
		if item != nil {
			assert.NotNil(t, item.Get, "case %s: spec path %s lacks a GET operation", c.Name, specPath)
		}
	}
}
