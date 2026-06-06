package rest_test

// The hand-owned OpenAPI 3.1 spec (openapi/openapi.yaml) is executable
// against the NEW stack too: every implemented battery case's OBSERVED
// response (not the committed golden — contract/spec_test.go already covers
// those) is validated against the document. Today that means the ranks
// operation's full surface plus the 401 tiers observed on the profile-by-id
// and tickets-list operations — the rest of the spec's operations are
// witnessed only once their routes land (#126–#129). Same non-vacuousness
// rules as the contract replay loop: the observed status must be EXPLICITLY
// documented on the operation, a JSON response requires an application/json
// schema to validate against, and every implemented case's path must be
// classified in specRoutes — unclassified paths fail, never skip.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/7cav/api/contract"
	"github.com/pb33f/libopenapi"
	"github.com/pb33f/libopenapi-validator/paths"
	"github.com/pb33f/libopenapi-validator/responses"
	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const specPath = "../openapi/openapi.yaml"

// specRoutes maps an implemented battery case's request path (query string
// stripped) to its spec path template. EVERY implemented case's path must be
// classified here (recipe step 6 in the rest package doc) — validateObserved
// fails on an unclassified path rather than skipping, so coverage cannot rot
// silently. "" marks the deliberately off-spec unknown-path surface, asserted
// to stay unmatched.
var specRoutes = map[string]string{
	"/api/v1/milpacs/ranks":      "/api/v1/milpacs/ranks",
	"/api/v1/tickets/categories": "/api/v1/tickets/categories",
	"/api/v1/tickets/42":         "/api/v1/tickets/{ticketId}",
	"/api/v1/tickets/9999":       "/api/v1/tickets/{ticketId}",
	"/api/v1/tickets/abc":          "/api/v1/tickets/{ticketId}",
	"/api/v1/tickets/ref/MF1UI9HE": "/api/v1/tickets/ref/{ticketRef}",
	"/api/v1/tickets/ref/NOPE9999": "/api/v1/tickets/ref/{ticketRef}",
	// The 401-tier battery cases replay against these two paths before their
	// routes are implemented (auth runs before routing, so the observed 401s
	// are route-independent); the operations document 401 explicitly.
	"/api/v1/milpacs/profile/id/1": "/api/v1/milpacs/profile/id/{userId}",
	"/api/v1/tickets":              "/api/v1/tickets",
	"/api/v1/does/not/exist":       "", // off-spec: unknown-path tier (mux behavior, not an operation)
}

func loadSpecModel(t *testing.T) *v3.Document {
	t.Helper()
	raw, err := os.ReadFile(specPath)
	require.NoError(t, err)
	doc, err := libopenapi.NewDocument(raw)
	require.NoError(t, err)
	model, errs := doc.BuildV3Model()
	require.Empty(t, errs)
	return &model.Model
}

// observedResponse reconstructs an http.Response from a replayed Golden so
// the validator can check it.
func observedResponse(g *contract.Golden) *http.Response {
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

// validateObserved checks one observed response against the spec, refusing
// to pass vacuously (mirrors contract/spec_test.go replayGoldenResponse).
func validateObserved(t *testing.T, model *v3.Document, rv responses.ResponseBodyValidator, reqPath string, g *contract.Golden) {
	t.Helper()

	basePath := reqPath
	if i := strings.IndexByte(basePath, '?'); i >= 0 {
		basePath = basePath[:i]
	}
	tmpl, known := specRoutes[basePath]
	require.True(t, known, "path %s missing from specRoutes — classify it (or mark it off-spec)", basePath)

	req := httptest.NewRequest(http.MethodGet, reqPath, nil)

	if tmpl == "" {
		pathItem, _, matched := paths.FindPath(req, model, nil)
		assert.Nil(t, pathItem,
			"path %s must stay off-spec but matched spec path %q", reqPath, matched)
		return
	}

	pathItem := model.Paths.PathItems.GetOrZero(tmpl)
	require.NotNil(t, pathItem, "spec is missing path %s", tmpl)
	op := pathItem.Get
	require.NotNil(t, op, "spec path %s has no GET operation", tmpl)
	require.NotNil(t, op.Responses, "operation %s declares no responses", op.OperationId)

	statusCode := strconv.Itoa(g.Status)
	docResp := op.Responses.Codes.GetOrZero(statusCode)
	require.NotNil(t, docResp,
		"operation %s: observed status %s is not explicitly documented (default resolution does not count)",
		op.OperationId, statusCode)

	if g.Body != nil {
		var schema *base.SchemaProxy
		if docResp.Content != nil {
			if mt := docResp.Content.GetOrZero("application/json"); mt != nil {
				schema = mt.Schema
			}
		}
		require.NotNil(t, schema,
			"operation %s: response %s declares no application/json schema — a JSON body would validate vacuously",
			op.OperationId, statusCode)
	} else {
		require.Positive(t, orderedmap.Len(docResp.Content),
			"operation %s: response %s declares no content but a body was observed", op.OperationId, statusCode)
	}

	valid, verrs := rv.ValidateResponseBodyWithPathItem(req, observedResponse(g), pathItem, tmpl)
	if !valid {
		var b strings.Builder
		for _, e := range verrs {
			fmt.Fprintf(&b, "[%s/%s] %s — %s\n", e.ValidationType, e.ValidationSubType, e.Message, e.Reason)
			for _, s := range e.SchemaValidationErrors {
				fmt.Fprintf(&b, "  field %s: %s\n", s.FieldPath, s.Reason)
			}
		}
		t.Errorf("operation %s: observed response does not validate:\n%s", op.OperationId, b.String())
	}
}

// TestNewStack_SpecValidation replays every implemented battery case against
// the new stack and validates the OBSERVED response against the spec.
func TestNewStack_SpecValidation(t *testing.T) {
	model := loadSpecModel(t)
	rv := responses.NewResponseBodyValidator(model)
	h := newStack(t)

	known := map[string]contract.Case{}
	for _, c := range contract.Cases() {
		known[c.Name] = c
	}

	// EVERY implemented case is validated — no silent skips: an
	// unclassified path fails inside validateObserved (the specRoutes
	// require), so forgetting recipe step 6 is a red test, not a vacuous
	// pass. The 401-tier cases replaying against not-yet-implemented routes
	// still validate fine: their observed 401s are explicitly documented on
	// the spec operations those paths classify to.
	for _, name := range implementedCases {
		c, ok := known[name]
		require.True(t, ok, "implementedCases entry %q names no battery case", name)
		t.Run(name, func(t *testing.T) {
			g, _, err := contract.RunCase(h, c)
			require.NoError(t, err)
			validateObserved(t, model, rv, c.Path, g)
		})
	}

	// The ranks operation's 401 tier, observed live (explicitly documented
	// on the operation as the shared Unauthorized response). The 403 scope
	// tier resolves only via `default` — the corpus has no committed
	// 403-on-ranks golden forcing an explicit declaration — so it is golden-
	// compared in TestNewStack_AuthTiersOnRanksRoute but not spec-validated
	// here (explicit-status rule).
	t.Run("ranks_401_tier", func(t *testing.T) {
		g, _, err := contract.RunCase(h, contract.Case{
			Name: "ranks-401", Method: http.MethodGet, Path: "/api/v1/milpacs/ranks", Auth: contract.AuthNone,
			Notes: "synthesized: 401 tier observed on the ranks operation",
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, g.Status)
		validateObserved(t, model, rv, "/api/v1/milpacs/ranks", g)
	})
}
