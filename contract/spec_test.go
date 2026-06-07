package contract

// The hand-owned OpenAPI 3.1 reference spec (PRD #112, issue #121) is an
// executable artifact: every committed golden is replayed against it and the
// battery/spec route coverage is asserted in both directions. These tests are
// the enforcement loop — spec drift fails CI with a message naming the
// operation and field.
//
// The spec lives at openapi/openapi.yaml. Validation uses
// pb33f/libopenapi-validator (test-only dependency), chosen because it is the
// maintained Go validator with real OpenAPI 3.1 support (kin-openapi was
// 3.0-only at time of choice, 2026-06).

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
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
	"github.com/pb33f/libopenapi-validator/helpers"
	"github.com/pb33f/libopenapi-validator/paths"
	"github.com/pb33f/libopenapi-validator/responses"
	"github.com/pb33f/libopenapi-validator/schema_validation"

	"github.com/7cav/api/internal/spectest"
)

const specPath = "../openapi/openapi.yaml"

// loadSpec parses the hand-owned OpenAPI document and returns its v3 model.
func loadSpec(t *testing.T) (libopenapi.Document, *v3.Document) {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err, "hand-owned OpenAPI spec missing at %s", specPath)
	return buildSpec(t, raw)
}

// buildSpec builds a document and v3 model from raw spec bytes.
func buildSpec(t *testing.T, raw []byte) (libopenapi.Document, *v3.Document) {
	t.Helper()
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

// forEachDocumentSchema visits every inline (non-$ref) schema in the
// document at its definition site: components.schemas, components.parameters,
// components.responses (content and headers), and every path operation's
// parameters and response content. $ref uses are skipped — the referenced
// schema is visited where it is defined. Recursion covers properties, items,
// additionalProperties, oneOf, anyOf, allOf and not.
//
// This walk is EXHAUSTIVE for the document by construction:
// TestSpec_DocumentStaysInWalkedDialect bans every schema-carrying construct
// the walk does not visit. Extend the dialect only by extending both
// together.
func forEachDocumentSchema(t *testing.T, model *v3.Document, visit func(path string, s *base.Schema)) {
	t.Helper()
	var walk func(path string, sp *base.SchemaProxy)
	walk = func(path string, sp *base.SchemaProxy) {
		if sp == nil || sp.IsReference() {
			return // referenced schemas are checked at their definition site
		}
		s := sp.Schema()
		if s == nil {
			return
		}
		visit(path, s)
		for pair := orderedmap.First(s.Properties); pair != nil; pair = pair.Next() {
			walk(path+"."+pair.Key(), pair.Value())
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
		for i, sub := range s.AllOf {
			walk(fmt.Sprintf("%s.allOf[%d]", path, i), sub)
		}
		if s.Not != nil {
			walk(path+".not", s.Not)
		}
	}

	walkContent := func(path string, content *orderedmap.Map[string, *v3.MediaType]) {
		for pair := orderedmap.First(content); pair != nil; pair = pair.Next() {
			walk(path+".content."+pair.Key(), pair.Value().Schema)
		}
	}
	walkResponse := func(path string, r *v3.Response) {
		if r == nil {
			return
		}
		walkContent(path, r.Content)
		for pair := orderedmap.First(r.Headers); pair != nil; pair = pair.Next() {
			walk(path+".headers."+pair.Key(), pair.Value().Schema)
		}
	}

	if model.Components != nil {
		for pair := orderedmap.First(model.Components.Schemas); pair != nil; pair = pair.Next() {
			walk("components.schemas."+pair.Key(), pair.Value())
		}
		for pair := orderedmap.First(model.Components.Parameters); pair != nil; pair = pair.Next() {
			walk("components.parameters."+pair.Key(), pair.Value().Schema)
		}
		for pair := orderedmap.First(model.Components.Responses); pair != nil; pair = pair.Next() {
			walkResponse("components.responses."+pair.Key(), pair.Value())
		}
	}
	for pair := orderedmap.First(model.Paths.PathItems); pair != nil; pair = pair.Next() {
		route := pair.Key()
		for method, op := range pair.Value().GetOperations().FromOldest() {
			opPath := strings.ToUpper(method) + " " + route
			for _, p := range op.Parameters {
				walk(opPath+".param."+p.Name, p.Schema)
			}
			if op.Responses != nil {
				for rp := orderedmap.First(op.Responses.Codes); rp != nil; rp = rp.Next() {
					walkResponse(opPath+".responses."+rp.Key(), rp.Value())
				}
				walkResponse(opPath+".responses.default", op.Responses.Default)
			}
		}
	}
}

// TestSpec_WireConventions pins the documented house style structurally over
// every inline schema in the document (components AND path-level inline
// schemas): every schema property is lowerCamelCase (Any's "@type" is the
// one historical exception), and every enum schema leads with its
// _UNSPECIFIED zero name (protojson zero-safe enum-name convention).
func TestSpec_WireConventions(t *testing.T) {
	_, model := loadSpec(t)
	require.NotNil(t, model.Components)

	lowerCamel := regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

	forEachDocumentSchema(t, model, func(path string, s *base.Schema) {
		for pair := orderedmap.First(s.Properties); pair != nil; pair = pair.Next() {
			name := pair.Key()
			if name != "@type" {
				assert.True(t, lowerCamel.MatchString(name),
					"%s: property %q is not lowerCamelCase", path, name)
			}
		}
		if len(s.Enum) > 0 {
			first := fmt.Sprintf("%v", s.Enum[0].Value)
			assert.True(t, strings.HasSuffix(first, "_UNSPECIFIED"),
				"%s: enum must lead with its _UNSPECIFIED zero name, got %q", path, first)
		}
	})
}

// TestSpec_DocumentStaysInWalkedDialect pins the document to the exact
// OpenAPI dialect the structural nets walk. forEachDocumentSchema covers
// components.schemas/parameters/responses and operation-level parameters and
// response content — and deliberately NOTHING more.
//
// Orchestrator decision — BAN, do not extend the walker: a walker can never
// durably outrun the OpenAPI grammar (every libopenapi upgrade can grow new
// schema carriers), so chasing it turns the structural nets' coverage claim
// into best-effort. A pinned dialect makes that claim EXACT: every construct
// that can carry a schema in this document is one the nets walk, and any new
// construct fails loudly here at introduction time, naming what to extend.
// Banned because unwalked:
//   - path-item-level parameters: (operation-level only)
//   - parameter content: (every parameter carries schema:)
//   - requestBody on any operation (GET-only surface, pinned by the battery)
//   - components.headers / components.requestBodies
//   - patternProperties / propertyNames inside any walked schema
func TestSpec_DocumentStaysInWalkedDialect(t *testing.T) {
	_, model := loadSpec(t)
	const remedy = "is outside the walked dialect — extending the dialect requires extending forEachDocumentSchema AND this test together"

	checkParam := func(where string, p *v3.Parameter) {
		assert.NotNil(t, p.Schema,
			"construct parameter-without-schema (%s, parameter %q) %s", where, p.Name, remedy)
		assert.Zero(t, orderedmap.Len(p.Content),
			"construct parameter-content (%s, parameter %q) %s", where, p.Name, remedy)
	}

	if model.Components != nil {
		assert.Zero(t, orderedmap.Len(model.Components.Headers),
			"construct components.headers %s", remedy)
		assert.Zero(t, orderedmap.Len(model.Components.RequestBodies),
			"construct components.requestBodies %s", remedy)
		for pair := orderedmap.First(model.Components.Parameters); pair != nil; pair = pair.Next() {
			checkParam("components.parameters."+pair.Key(), pair.Value())
		}
	}

	for pair := orderedmap.First(model.Paths.PathItems); pair != nil; pair = pair.Next() {
		route := pair.Key()
		assert.Empty(t, pair.Value().Parameters,
			"construct path-item-level parameters (on %s) %s", route, remedy)
		for method, op := range pair.Value().GetOperations().FromOldest() {
			opPath := strings.ToUpper(method) + " " + route
			assert.Nil(t, op.RequestBody,
				"construct requestBody (on %s) %s", opPath, remedy)
			for _, p := range op.Parameters {
				checkParam(opPath, p)
			}
		}
	}

	forEachDocumentSchema(t, model, func(path string, s *base.Schema) {
		assert.Zero(t, orderedmap.Len(s.PatternProperties),
			"construct patternProperties (at %s) %s", path, remedy)
		assert.Nil(t, s.PropertyNames,
			"construct propertyNames (at %s) %s", path, remedy)
	})
}

// TestSpec_SchemasAreEmitEverything pins the emit-everything strictness
// structurally: every object schema that declares properties must require
// ALL of them and close itself with additionalProperties: false. Without
// this, deleting a required: list or an additionalProperties: false line is
// a silent loosening — goldens witness presence, not the spec's tolerance
// for absence. A property-less object schema is forbidden outright unless
// its additionalProperties is itself a schema (the legitimate map shapes,
// e.g. Roster.profiles): a bare `type: object` validates ANY object, so
// gutting a schema wholesale would otherwise keep the replay loop green
// vacuously. Any is the documented exception: protobuf Any is an open type
// by design (arbitrary fields per @type), so it cannot be closed.
func TestSpec_SchemasAreEmitEverything(t *testing.T) {
	_, model := loadSpec(t)

	forEachDocumentSchema(t, model, func(path string, s *base.Schema) {
		if path == "components.schemas.Any" {
			return // open by design; see schema description
		}
		if orderedmap.Len(s.Properties) == 0 {
			if slices.Contains(s.Type, "object") {
				isMap := s.AdditionalProperties != nil && s.AdditionalProperties.IsA()
				assert.True(t, isMap,
					"%s: object schema declares no properties — a bare object validates anything; declare its properties or make it an explicit map (additionalProperties as a schema)", path)
			}
			return // the required/closed rule binds object schemas that declare properties
		}
		var props []string
		for pair := orderedmap.First(s.Properties); pair != nil; pair = pair.Next() {
			props = append(props, pair.Key())
		}
		assert.ElementsMatch(t, props, s.Required,
			"%s: object schema must require ALL its properties (emit-everything)", path)
		closed := s.AdditionalProperties != nil && s.AdditionalProperties.IsB() && !s.AdditionalProperties.B
		assert.True(t, closed,
			"%s: object schema must set additionalProperties: false (emit-everything)", path)
	})
}

// TestSpec_NullableUnionsAreExact pins the shape of the Nullable* null
// unions structurally. Their null branches are corpus-unwitnessed except for
// "primary": null, so a null branch quietly rewritten to another type (or a
// wrapper growing extra branches/keywords) would stay green against the
// goldens. Every components.schemas.Nullable* must therefore be EXACTLY a
// oneOf of two branches — one $ref (the non-null target) and one bare
// {type: 'null'} — and nothing else.
func TestSpec_NullableUnionsAreExact(t *testing.T) {
	_, model := loadSpec(t)
	require.NotNil(t, model.Components)

	count := 0
	for pair := orderedmap.First(model.Components.Schemas); pair != nil; pair = pair.Next() {
		name := pair.Key()
		if !strings.HasPrefix(name, "Nullable") {
			continue
		}
		count++
		where := "components.schemas." + name
		s := pair.Value().Schema()
		require.NotNil(t, s, "%s: schema does not build", where)

		// The wrapper carries the oneOf and nothing else.
		assert.Len(t, s.OneOf, 2, "%s: Nullable* must be exactly a oneOf of 2 branches", where)
		assert.Empty(t, s.Type, "%s: Nullable* wrapper must not declare a type", where)
		assert.Zero(t, orderedmap.Len(s.Properties), "%s: Nullable* wrapper must not declare properties", where)
		assert.Empty(t, s.AnyOf, "%s: Nullable* wrapper must not use anyOf", where)
		assert.Empty(t, s.AllOf, "%s: Nullable* wrapper must not use allOf", where)
		assert.Nil(t, s.Not, "%s: Nullable* wrapper must not use not", where)
		assert.Empty(t, s.Enum, "%s: Nullable* wrapper must not declare an enum", where)

		refs, nulls := 0, 0
		for _, branch := range s.OneOf {
			if branch.IsReference() {
				refs++
				continue
			}
			bs := branch.Schema()
			if bs == nil {
				continue
			}
			bare := len(bs.Type) == 1 && bs.Type[0] == "null" &&
				orderedmap.Len(bs.Properties) == 0 && bs.Items == nil &&
				len(bs.OneOf) == 0 && len(bs.AnyOf) == 0 && len(bs.AllOf) == 0 &&
				bs.Not == nil && len(bs.Enum) == 0
			if bare {
				nulls++
			}
		}
		assert.Equal(t, 1, refs,
			"%s: exactly one branch must be a $ref to the non-null target", where)
		assert.Equal(t, 1, nulls,
			"%s: exactly one branch must be a bare {type: 'null'}", where)
	}
	assert.Equal(t, 4, count,
		"the Nullable* component set is pinned (User, Rank, Position, S1UniformsRank)")
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
// swallows them. Membership is pinned by TestSpec_CarveOutMapsAreLive to the
// auth/unknown_path_* cases only — this map is not a generic escape hatch.
var offSpecCases = map[string]string{
	"auth/unknown_path_authenticated":   "unknown path under the API prefix: JSON 404 is mux behavior, not an operation",
	"auth/unknown_path_unauthenticated": "unknown path without auth: 401 tier fires before routing",
}

// templateUnmatchableCases are goldens whose concrete request path cannot be
// matched by ANY OpenAPI path template — the gateway's {position_query=**}
// glob (proto/milpacs.proto) swallows slashes, which OpenAPI cannot express.
// Exactly one recorded form qualifies: the multi-segment query. (The
// trailing-slash form DOES match /{positionQuery} with an empty segment; it
// lives in specViolatingRequests instead.) The replay loop asserts the
// exemption's own justification — paths.FindPath must fail — then resolves
// the operation by template name: request-side validation is impossible, but
// response validation still runs against the named operation.
var templateUnmatchableCases = map[string]string{
	"position/search_multi_segment": "/api/v1/milpacs/position/search/{positionQuery}",
}

// mustFailRequest pins WHY a deliberately contract-violating request must
// fail validation: an error of wantType carrying wantMessage must be among
// the validator's findings, so a must-fail case cannot silently start
// failing for an unrelated reason.
type mustFailRequest struct {
	reason      string
	wantType    string // errors.ValidationError.ValidationType
	wantMessage string // substring of errors.ValidationError.Message
}

// specViolatingRequests are goldens whose request deliberately breaks the
// documented request contract; the replay loop asserts request validation
// FAILS for them — for the pinned reason — proving the spec's parameter and
// security constraints describe the same gate the API enforces. Every entry
// records a 4xx response (asserted in the replay loop).
var specViolatingRequests = map[string]mustFailRequest{
	"auth/milpacs_missing_header": {
		reason:      "no Authorization header: violates the bearer security requirement",
		wantType:    helpers.SecurityValidation,
		wantMessage: "Authorization header for 'bearer' scheme",
	},
	"auth/milpacs_raw_key": {
		reason:      "raw key without Bearer scheme: violates the bearer security requirement",
		wantType:    helpers.SecurityValidation,
		wantMessage: "Authorization header scheme 'bearer' mismatch",
	},
	"auth/tickets_missing_header": {
		reason:      "no Authorization header: violates the bearer security requirement",
		wantType:    helpers.SecurityValidation,
		wantMessage: "Authorization header for 'bearer' scheme",
	},
	"auth/tickets_raw_key": {
		reason:      "raw key without Bearer scheme: violates the bearer security requirement",
		wantType:    helpers.SecurityValidation,
		wantMessage: "Authorization header scheme 'bearer' mismatch",
	},
	"milpacs/profile_by_id_parse_error": {
		reason:      "non-numeric userId violates the uint64-as-string path parameter",
		wantType:    helpers.ParameterValidation,
		wantMessage: "Path parameter 'userId' failed to validate",
	},
	"position/search_trailing_slash": {
		reason:      "the ** glob's empty-segment form: the required positionQuery path parameter is empty",
		wantType:    helpers.ParameterValidation,
		wantMessage: "Path parameter 'positionQuery' is missing",
	},
	"roster/bogus_enum": {
		reason:      "IMAGINARY_ROSTER is neither an enum name nor a number",
		wantType:    helpers.ParameterValidation,
		wantMessage: "Path parameter 'roster' failed to validate",
	},
	"tickets/get_by_id_parse_error": {
		reason:      "non-numeric ticketId violates the integer path parameter",
		wantType:    helpers.ParameterValidation,
		wantMessage: "Path parameter 'ticketId' is not a valid integer",
	},
	"tickets/messages_parse_error": {
		reason:      "non-numeric ticketId violates the integer path parameter",
		wantType:    helpers.ParameterValidation,
		wantMessage: "Path parameter 'ticketId' is not a valid integer",
	},
}

// TestSpec_CarveOutMapsAreLive: the three exemption maps must reference real
// battery cases and keep their pinned cardinality — a renamed case or a
// bogus key otherwise rots silently, and a growing map is a growing hole in
// the replay loop.
func TestSpec_CarveOutMapsAreLive(t *testing.T) {
	known := map[string]bool{}
	for _, c := range Cases() {
		known[c.Name] = true
	}

	assert.Len(t, offSpecCases, 2, "off-spec carve-outs are pinned")
	for name := range offSpecCases {
		assert.True(t, known[name], "offSpecCases key %q names no battery case", name)
		assert.True(t, strings.HasPrefix(name, "auth/unknown_path_"),
			"offSpecCases is the unknown-path tier only, got %q — it must not become a generic spec-coverage escape hatch", name)
	}

	assert.Len(t, templateUnmatchableCases, 1, "template-unmatchable carve-outs are pinned")
	for name := range templateUnmatchableCases {
		assert.True(t, known[name], "templateUnmatchableCases key %q names no battery case", name)
	}

	assert.Len(t, specViolatingRequests, 9, "must-fail request entries are pinned")
	for name := range specViolatingRequests {
		assert.True(t, known[name], "specViolatingRequests key %q names no battery case", name)
	}
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

// hasValidationError reports whether an error of wantType carrying
// wantMessage is among the validator's findings.
func hasValidationError(errs []*errors.ValidationError, wantType, wantMessage string) bool {
	for _, e := range errs {
		if e.ValidationType == wantType && strings.Contains(e.Message, wantMessage) {
			return true
		}
	}
	return false
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
func operationID(op *v3.Operation, method, specPath string) string {
	if op != nil && op.OperationId != "" {
		return op.OperationId
	}
	return strings.ToUpper(method) + " " + specPath
}

// replayGoldenResponse checks a golden's recorded response against the spec
// and returns human-readable problems (empty = the response validates). It
// refuses to pass vacuously — the library validator returns valid=true when
// an operation has nil responses, a matched response has no content, or the
// media type carries no schema (all legal OpenAPI 3.1), so this resolves the
// response itself and requires substance first: the operation must declare
// responses, the golden's status must be EXPLICITLY documented (default
// resolution does not count — every corpus-witnessed status is declared),
// and a JSON golden requires an application/json schema to validate against.
func replayGoldenResponse(respValidator responses.ResponseBodyValidator, req *http.Request, golden *Golden, pathItem *v3.PathItem, specPath string) []string {
	op := pathItem.Get // the corpus is GET-only (pinned by TestBatteryIsWellFormed)
	opID := operationID(op, http.MethodGet, specPath)
	if op == nil {
		return []string{fmt.Sprintf("operation %s: spec path has no GET operation", opID)}
	}
	if op.Responses == nil || orderedmap.Len(op.Responses.Codes) == 0 {
		return []string{fmt.Sprintf("operation %s declares no responses", opID)}
	}
	code := strconv.Itoa(golden.Status)
	docResp := op.Responses.Codes.GetOrZero(code)
	if docResp == nil {
		return []string{fmt.Sprintf(
			"operation %s: golden status %s is not explicitly documented in responses — default resolution does not count; declare the witnessed status",
			opID, code)}
	}
	switch {
	case golden.Body != nil: // JSON golden: demand a schema to validate against
		var schema *base.SchemaProxy
		if docResp.Content != nil {
			if mt := docResp.Content.GetOrZero("application/json"); mt != nil {
				schema = mt.Schema
			}
		}
		if schema == nil {
			return []string{fmt.Sprintf(
				"operation %s: response %s declares no application/json schema — a JSON golden would validate vacuously",
				opID, code)}
		}
	case golden.BodyText != nil:
		if orderedmap.Len(docResp.Content) == 0 {
			return []string{fmt.Sprintf(
				"operation %s: response %s declares no content but the golden records a body", opID, code)}
		}
	}

	resp := goldenResponse(golden)
	valid, verrs := respValidator.ValidateResponseBodyWithPathItem(req, resp, pathItem, specPath)
	if !valid {
		return []string{formatValidationErrors(opID, verrs)}
	}
	return nil
}

// TestSpec_GoldenReplay is the executable-spec loop: every committed golden
// is replayed against the OpenAPI document. Request validation runs where
// the request is expressible (and is asserted to FAIL — for the pinned
// reason — for the deliberately contract-violating requests); response
// validation — explicitly documented status code, content type, body schema —
// runs for every golden that resolves to an operation. The two unknown-path
// carve-outs assert non-resolution instead (offSpecCases). Failures name the
// operation and the field.
func TestSpec_GoldenReplay(t *testing.T) {
	doc, model := loadSpec(t)
	v, errs := validator.NewValidator(doc)
	require.Empty(t, errs, "building validator")
	respValidator := responses.NewResponseBodyValidator(model)

	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			golden, err := LoadGolden(goldensDir, c.Name)
			require.NoError(t, err)
			req := newCaseRequest(c)

			if reason, off := offSpecCases[c.Name]; off {
				pathItem, _, matched := paths.FindPath(req, model, nil)
				assert.Nil(t, pathItem,
					"case %s must stay off-spec (%s) but matched spec path %q", c.Name, reason, matched)
				return
			}

			specPath := classifySpecPath(c.Path)
			require.NotEmpty(t, specPath, "case %s: path %s matches no known route", c.Name, c.Path)

			var pathItem *v3.PathItem
			skipRequestValidation := false
			if tmpl, ok := templateUnmatchableCases[c.Name]; ok {
				// Assert the exemption's own justification first: if the path
				// ever becomes template-matchable, the carve-out must die
				// rather than silently suppress request validation.
				found, _, matched := paths.FindPath(req, model, nil)
				require.Nil(t, found,
					"case %s is listed template-unmatchable but FindPath matched %q — remove it from templateUnmatchableCases",
					c.Name, matched)
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
			opID := operationID(pathItem.Get, http.MethodGet, specPath)

			if !skipRequestValidation {
				valid, verrs := v.ValidateHttpRequestSyncWithPathItem(req, pathItem, specPath)
				if entry, bad := specViolatingRequests[c.Name]; bad {
					require.True(t, golden.Status >= 400 && golden.Status < 500,
						"case %s: must-fail entries record 4xx responses, got %d", c.Name, golden.Status)
					assert.False(t, valid,
						"case %s: request must violate the spec (%s) but validated clean", c.Name, entry.reason)
					assert.True(t, hasValidationError(verrs, entry.wantType, entry.wantMessage),
						"case %s: request must fail for its documented reason ([%s] %q), got:\n%s",
						c.Name, entry.wantType, entry.wantMessage, formatValidationErrors(opID, verrs))
				} else {
					assert.True(t, valid, "case %s: request does not validate:\n%s",
						c.Name, formatValidationErrors(opID, verrs))
				}
			}

			for _, p := range replayGoldenResponse(respValidator, req, golden, pathItem, specPath) {
				t.Errorf("case %s: recorded response does not validate:\n%s", c.Name, p)
			}
		})
	}
}

// batteryCase resolves a battery case by name.
func batteryCase(t *testing.T, name string) Case {
	t.Helper()
	for _, c := range Cases() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no battery case named %s", name)
	return Case{}
}

// mutateSpec applies one raw-text mutation to the spec source and rebuilds
// the document and model. find must occur exactly once so the mutation
// cannot silently miss its target when the spec is edited.
func mutateSpec(t *testing.T, find, replace string) (libopenapi.Document, *v3.Document) {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(raw), find),
		"canary mutation target must occur exactly once in the spec:\n%s", find)
	return buildSpec(t, []byte(strings.Replace(string(raw), find, replace, 1)))
}

// TestSpec_MutationCanary is the permanent teeth-check for response
// validation: each of the five mutations replays one golden against a
// deliberately broken in-memory copy of the spec and demands a LOUD failure
// naming the operation and the broken piece. The underlying library returns
// valid=true for nil responses, missing content and non-JSON media types —
// if the replay loop ever turns vacuous again (nil responses, stripped
// content, default-swallowed statuses, loosened types, or the text-golden
// no-content branch), this test fails first.
func TestSpec_MutationCanary(t *testing.T) {
	const ranks200Block = `        "200":
          description: Rank reference list.
          headers:
            Cache-Control:
              description: >-
                Freshness signal: data may be up to 10 minutes stale —
                consumers may treat a response as fresh for 10 minutes
                between polls.
              schema:
                type: string
                const: max-age=600
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/RanksResponse'
`
	const ranksResponsesBlock = `      responses:
` + ranks200Block + `        "401":
          $ref: '#/components/responses/Unauthorized'
        default:
          $ref: '#/components/responses/Error'
`

	muts := []struct {
		name     string
		find     string
		replace  string
		caseName string
		want     string // substring the failure must carry
		wantOp   string // operation name the failure must carry
	}{
		{
			name:     "flipped field type fails schema validation",
			find:     "rankDisplayOrder:\n          type: integer",
			replace:  "rankDisplayOrder:\n          type: string",
			caseName: "milpacs/ranks",
			want:     "rankDisplayOrder",
			wantOp:   "MilpacService_GetAllRanks",
		},
		{
			name: "witnessed status must be explicitly documented, not default-swallowed",
			find: `"200":
          description: Rank reference list.`,
			replace: `"299":
          description: Rank reference list.`,
			caseName: "milpacs/ranks",
			want:     "status 200",
			wantOp:   "MilpacService_GetAllRanks",
		},
		{
			name:     "JSON golden requires an application/json response schema",
			find:     ranks200Block,
			replace:  "        \"200\":\n          description: Rank reference list.\n",
			caseName: "milpacs/ranks",
			want:     "application/json",
			wantOp:   "MilpacService_GetAllRanks",
		},
		{
			name:     "operation must declare responses",
			find:     ranksResponsesBlock,
			replace:  "",
			caseName: "milpacs/ranks",
			want:     "declares no responses",
			wantOp:   "MilpacService_GetAllRanks",
		},
		{
			// The text-golden substance branch: a 401 golden records a
			// plain-text body, so a response stripped of its content must
			// fail loudly rather than validate vacuously.
			name: "text golden requires declared content on the response",
			find: `      content:
        text/plain:
          schema:
            type: string
`,
			replace:  "",
			caseName: "auth/milpacs_missing_header",
			want:     "declares no content but the golden records a body",
			wantOp:   "MilpacService_GetProfile",
		},
	}

	for _, m := range muts {
		t.Run(m.name, func(t *testing.T) {
			_, model := mutateSpec(t, m.find, m.replace)
			respValidator := responses.NewResponseBodyValidator(model)

			c := batteryCase(t, m.caseName)
			golden, err := LoadGolden(goldensDir, c.Name)
			require.NoError(t, err)
			req := newCaseRequest(c)
			pathItem, _, tmpl := paths.FindPath(req, model, nil)
			require.NotNil(t, pathItem)

			problems := replayGoldenResponse(respValidator, req, golden, pathItem, tmpl)
			require.NotEmpty(t, problems,
				"broken spec (%s) must fail response validation — the replay loop has gone vacuous", m.name)
			joined := strings.Join(problems, "\n")
			assert.Contains(t, joined, m.want, "failure must name the broken piece")
			assert.Contains(t, joined, m.wantOp, "failure must name the operation")
		})
	}
}

// TestSpec_Every200DeclaresCacheControl pins the Cache-Control freshness
// signal (#131) at the spec layer, both directions:
//
//   - every operation's explicitly declared 2xx response MUST declare a
//     Cache-Control header (deliberately optional — see the required-header
//     note in the body) whose schema is a string const of the form
//     "max-age=N" — the spec is where the per-route-group values are
//     recorded for consumers, and a new operation cannot land without
//     declaring its freshness bound;
//   - no non-2xx response (operation-declared, default, or shared
//     components.responses) may declare one — errors are always served
//     live, exactly as the retired response cache (200s-only) behaved.
//
// The VALUES the new stack actually sends are asserted against these
// declarations in rest/spec_test.go (observed-equals-declared), so the spec
// and the registration table cannot drift apart silently. The frozen golden
// corpus deliberately records no Cache-Control (the old stack sends none —
// a recorded key would pin the header's absence), which is why this is a
// structural net plus a live coupling test, not a golden field.
func TestSpec_Every200DeclaresCacheControl(t *testing.T) {
	_, model := loadSpec(t)

	cacheControlConst := regexp.MustCompile(`^max-age=[0-9]+$`)

	checkResponse := func(where, code string, r *v3.Response) {
		if r == nil {
			return
		}
		var hdr *v3.Header
		if r.Headers != nil {
			hdr = r.Headers.GetOrZero("Cache-Control")
		}
		if !strings.HasPrefix(code, "2") {
			assert.Nil(t, hdr,
				"%s: response %s declares Cache-Control — errors are served live, only 2xx carries the freshness signal", where, code)
			return
		}
		require.NotNil(t, hdr,
			"%s: 2xx response declares no Cache-Control header — every read endpoint records its freshness bound in the spec (#131)", where)
		// Deliberately NOT required: the validator enforces required response
		// headers, and the frozen golden corpus (recorded from the old stack,
		// which sends none — and allowlist-filtered besides) replays against
		// this document, so required: true breaks TestSpec_GoldenReplay
		// permanently. The new stack's always-sent-on-200 guarantee is pinned
		// live instead (rest/cachecontrol_test.go and the observed-equals-
		// declared coupling in rest/spec_test.go).
		assert.False(t, hdr.Required,
			"%s: Cache-Control must stay optional — required: true fails the frozen-corpus replay (goldens record no Cache-Control)", where)
		require.NotNil(t, hdr.Schema, "%s: Cache-Control header declares no schema", where)
		s := hdr.Schema.Schema()
		require.NotNil(t, s, "%s: Cache-Control header schema does not build", where)
		assert.Equal(t, []string{"string"}, s.Type, "%s: Cache-Control schema must be a string", where)
		require.NotNil(t, s.Const,
			"%s: Cache-Control schema must pin its exact value with const — the declaration IS the recorded per-route-group value", where)
		assert.Regexp(t, cacheControlConst, s.Const.Value,
			"%s: Cache-Control const must be of the form max-age=N", where)
	}

	if model.Components != nil {
		for pair := orderedmap.First(model.Components.Responses); pair != nil; pair = pair.Next() {
			// Shared components.responses are the error tier (Unauthorized,
			// Error) — never 2xx, so they must declare no Cache-Control.
			checkResponse("components.responses."+pair.Key(), "default", pair.Value())
		}
	}

	two00s := 0
	for pair := orderedmap.First(model.Paths.PathItems); pair != nil; pair = pair.Next() {
		route := pair.Key()
		for method, op := range pair.Value().GetOperations().FromOldest() {
			opPath := strings.ToUpper(method) + " " + route
			if op.Responses == nil {
				continue // absence of responses is the replay loop's failure to report
			}
			for rp := orderedmap.First(op.Responses.Codes); rp != nil; rp = rp.Next() {
				if strings.HasPrefix(rp.Key(), "2") {
					two00s++
				}
				checkResponse(opPath+".responses."+rp.Key(), rp.Key(), rp.Value())
			}
			checkResponse(opPath+".responses.default", "default", op.Responses.Default)
		}
	}
	// Non-vacuousness: the surface declares a 2xx on every operation
	// (TestSpec_EveryOperationHasGolden demands a 2xx golden per operation,
	// and the replay loop demands the status be declared).
	assert.GreaterOrEqual(t, two00s, 16, "expected a 2xx declaration per public operation, saw %d", two00s)
}

// TestSpec_EveryOperationHasGolden is coverage direction A: every operation
// in the spec is exercised by at least one golden — including at least one
// 2xx golden, so error-only coverage cannot count as witnessing the success
// shape — and the document cannot describe surface the corpus does not
// witness.
func TestSpec_EveryOperationHasGolden(t *testing.T) {
	_, model := loadSpec(t)

	covered := map[string]bool{}
	covered2xx := map[string]bool{}
	for _, c := range Cases() {
		if _, off := offSpecCases[c.Name]; off {
			continue
		}
		specPath := classifySpecPath(c.Path)
		if tmpl, ok := templateUnmatchableCases[c.Name]; ok {
			specPath = tmpl
		}
		if specPath == "" {
			continue
		}
		key := "GET " + specPath
		covered[key] = true
		golden, err := LoadGolden(goldensDir, c.Name)
		require.NoError(t, err)
		if golden.Status/100 == 2 {
			covered2xx[key] = true
		}
	}

	total := 0
	for pair := orderedmap.First(model.Paths.PathItems); pair != nil; pair = pair.Next() {
		specPath := pair.Key()
		item := pair.Value()
		// The corpus is GET-only today (pinned by TestBatteryIsWellFormed),
		// so coverage keys carry GET; iterating every declared operation
		// keeps this loop honest if a non-GET operation ever appears.
		for method, op := range item.GetOperations().FromOldest() {
			total++
			key := strings.ToUpper(method) + " " + specPath
			id := operationID(op, method, specPath)
			assert.True(t, covered[key],
				"operation %s (%s) has no golden — record one or remove the operation", id, key)
			assert.True(t, covered2xx[key],
				"operation %s (%s) has no 2xx golden — error-only goldens do not witness the success shape", id, key)
		}
	}
	assert.NotZero(t, total, "spec declares no operations")
}

// TestSpec_DeclaredStatusesAreCorpusWitnessed is the inverse of the replay
// loop's explicit-status rule. The replay loop demands every witnessed
// status be declared; this test demands every DECLARED status be witnessed,
// so a fictional response code cannot ride along undetected. Carve-outs:
// "401" (the pre-routing auth tier is uniform across the surface, declared
// on every operation, but witnessed by goldens on only two of them) and the
// enumerated liveWitnessedStatuses — every other residual entry is a spec
// bug.
//
// liveWitnessedStatuses are statuses the new stack demonstrably emits but
// the FROZEN corpus never recorded: the witness is a live per-test outage
// observation in rest/spec_test.go (TestNewStack_SpecValidation's
// lite_roster_500_outage / s1_uniforms_500_outage subtests), with the frozen
// bodies pinned by TestNewStack_LiteAndS1OutagesAreInternalJSON.
//
// CONSTRAINT (ratified at the #127 review): every entry in this map MUST
// name a live witness that (a) drives the status through the real stack
// (rest.New + contract.RunCase, not a replayed golden) and (b) validates
// the observed response via validateObserved, whose explicit-status rule
// makes the spec line load-bearing — deleting the declared status turns
// the witness red. The reverse direction is enforced mechanically too:
// the entries live in internal/spectest, and rest/spec_test.go DRIVES its
// witness subtests from that registry with a 1:1 meta-assertion, so an
// entry whose witness is deleted fails there instead of relying on review.
// An entry without an asserting witness is a spec bug, not a carve-out —
// carve-outs are never grandfathered.
var liveWitnessedStatuses = func() map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for _, lw := range spectest.LiveWitnessedStatuses {
		if m[lw.Op] == nil {
			m[lw.Op] = map[string]bool{}
		}
		m[lw.Op][lw.Status] = true
	}
	return m
}()

func TestSpec_DeclaredStatusesAreCorpusWitnessed(t *testing.T) {
	_, model := loadSpec(t)

	witnessed := map[string]map[string]bool{} // "GET /path" -> set of golden statuses
	for _, c := range Cases() {
		if _, off := offSpecCases[c.Name]; off {
			continue
		}
		specPath := classifySpecPath(c.Path)
		if tmpl, ok := templateUnmatchableCases[c.Name]; ok {
			specPath = tmpl
		}
		require.NotEmpty(t, specPath, "case %s: path %s matches no known route", c.Name, c.Path)
		golden, err := LoadGolden(goldensDir, c.Name)
		require.NoError(t, err)
		key := "GET " + specPath
		if witnessed[key] == nil {
			witnessed[key] = map[string]bool{}
		}
		witnessed[key][strconv.Itoa(golden.Status)] = true
	}

	unwitnessed401Ops := 0
	for pair := orderedmap.First(model.Paths.PathItems); pair != nil; pair = pair.Next() {
		specPath := pair.Key()
		for method, op := range pair.Value().GetOperations().FromOldest() {
			key := strings.ToUpper(method) + " " + specPath
			id := operationID(op, method, specPath)
			if op.Responses == nil {
				continue // absence of responses is the replay loop's failure to report
			}
			for rp := orderedmap.First(op.Responses.Codes); rp != nil; rp = rp.Next() {
				code := rp.Key()
				if witnessed[key][code] {
					continue
				}
				if code == "401" {
					// The uniform pre-routing 401, declared everywhere but
					// witnessed only where a 401 golden exists.
					unwitnessed401Ops++
					continue
				}
				if liveWitnessedStatuses[key][code] {
					// Witnessed live against the new stack (see the map's
					// doc comment) — the frozen corpus cannot grow a golden.
					continue
				}
				t.Errorf("declared status %s on %s is witnessed by no golden — explicit statuses must be corpus-witnessed (401 and liveWitnessedStatuses carve-outs only)", code, id)
			}
		}
	}
	assert.Equal(t, 14, unwitnessed401Ops,
		"the 401 carve-out covers exactly the operations lacking a 401 golden")
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
