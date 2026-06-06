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
	"fmt"
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
