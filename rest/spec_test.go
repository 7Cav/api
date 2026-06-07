package rest_test

// The hand-owned OpenAPI 3.1 spec (openapi/openapi.yaml) is executable
// against the NEW stack too: every implemented battery case's OBSERVED
// response (not the committed golden — contract/spec_test.go already covers
// those) is validated against the document. Today that means the ranks
// operation, the four profile lookup operations (id, username, discord,
// gamertag — #126), all five tickets operations (#129), the three roster
// operations (full/lite/S1 uniforms — #127), and the position groups,
// position search and AWOL operations (#128) — the full Phase 3 fan-out
// surface. Same non-vacuousness rules as the contract replay loop: the
// observed status
// must be EXPLICITLY documented on the operation, a JSON response requires
// an application/json schema to validate against, and every implemented
// case's path must be classified in specRoutes — unclassified paths fail,
// never skip.

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
	"github.com/7cav/api/internal/spectest"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/rest"
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
	"/api/v1/milpacs/ranks": "/api/v1/milpacs/ranks",
	// Reference lists (#128): position groups and the AWOL list.
	"/api/v1/milpacs/position/groups": "/api/v1/milpacs/position/groups",
	"/api/v1/milpacs/awol":            "/api/v1/milpacs/awol",
	// Position search (#128): happy (%20-encoded title), empty result
	// (frozen #137 behavior), multi-segment (the ** glob form an OpenAPI
	// template cannot express — classified to the canonical template so the
	// observed RESPONSE still validates; the contract suite carries the
	// request-side carve-out, templateUnmatchableCases), trailing slash
	// (empty query, the handler's 400).
	"/api/v1/milpacs/position/search/Regimental%20Technical%20Aide": "/api/v1/milpacs/position/search/{positionQuery}",
	"/api/v1/milpacs/position/search/squad%20leader":                "/api/v1/milpacs/position/search/{positionQuery}",
	"/api/v1/milpacs/position/search/Platoon/Leader":                "/api/v1/milpacs/position/search/{positionQuery}",
	"/api/v1/milpacs/position/search/":                              "/api/v1/milpacs/position/search/{positionQuery}",
	// Tickets (#129): categories, by id (happy/not-found/parse-error), by
	// ref (happy/not-found), messages (happy/page-two/parse-error), list —
	// the list path also carries the 401-tier battery cases (auth runs
	// before routing, so the observed 401s are route-independent).
	"/api/v1/tickets":              "/api/v1/tickets",
	"/api/v1/tickets/categories":   "/api/v1/tickets/categories",
	"/api/v1/tickets/42":           "/api/v1/tickets/{ticketId}",
	"/api/v1/tickets/9999":         "/api/v1/tickets/{ticketId}",
	"/api/v1/tickets/abc":          "/api/v1/tickets/{ticketId}",
	"/api/v1/tickets/ref/MF1UI9HE": "/api/v1/tickets/ref/{ticketRef}",
	"/api/v1/tickets/ref/NOPE9999": "/api/v1/tickets/ref/{ticketRef}",
	"/api/v1/tickets/42/messages":  "/api/v1/tickets/{ticketId}/messages",
	"/api/v1/tickets/555/messages": "/api/v1/tickets/{ticketId}/messages",
	"/api/v1/tickets/abc/messages": "/api/v1/tickets/{ticketId}/messages",
	// Profile by id: happy (1), sparse (2), not-found (999), zero (0),
	// parse-error (abc), injected outage (777) — plus the 401-tier battery
	// cases that replay against /id/1.
	"/api/v1/milpacs/profile/id/1":   "/api/v1/milpacs/profile/id/{userId}",
	"/api/v1/milpacs/profile/id/2":   "/api/v1/milpacs/profile/id/{userId}",
	"/api/v1/milpacs/profile/id/999": "/api/v1/milpacs/profile/id/{userId}",
	"/api/v1/milpacs/profile/id/0":   "/api/v1/milpacs/profile/id/{userId}",
	"/api/v1/milpacs/profile/id/abc": "/api/v1/milpacs/profile/id/{userId}",
	"/api/v1/milpacs/profile/id/777": "/api/v1/milpacs/profile/id/{userId}",
	// Profile by username: happy, not-found.
	"/api/v1/milpacs/profile/username/Jarvis.A":   "/api/v1/milpacs/profile/username/{username}",
	"/api/v1/milpacs/profile/username/Ghost.User": "/api/v1/milpacs/profile/username/{username}",
	// Discord lookup: happy, not-found.
	"/api/v1/milpac/discord/112233445566778899": "/api/v1/milpac/discord/{discordId}",
	"/api/v1/milpac/discord/999000999":          "/api/v1/milpac/discord/{discordId}",
	// Gamertag lookup: happy, not-found.
	"/api/v1/milpac/gamertag/CavGamer77": "/api/v1/milpac/gamertag/{gamertag}",
	"/api/v1/milpac/gamertag/GhostTag":   "/api/v1/milpac/gamertag/{gamertag}",
	// Full roster (#127): both enum path forms (name/number), empty roster,
	// zero enum under both forms, bogus literal, injected outage — the
	// unknown-query case's base path is the by-name happy path.
	"/api/v1/roster/ROSTER_TYPE_COMBAT":      "/api/v1/roster/{roster}",
	"/api/v1/roster/1":                       "/api/v1/roster/{roster}",
	"/api/v1/roster/ROSTER_TYPE_RESERVE":     "/api/v1/roster/{roster}",
	"/api/v1/roster/ROSTER_TYPE_UNSPECIFIED": "/api/v1/roster/{roster}",
	"/api/v1/roster/0":                       "/api/v1/roster/{roster}",
	"/api/v1/roster/IMAGINARY_ROSTER":        "/api/v1/roster/{roster}",
	"/api/v1/roster/ROSTER_TYPE_ARLINGTON":   "/api/v1/roster/{roster}",
	// Lite roster: name/number forms, empty roster, zero enum.
	"/api/v1/roster/ROSTER_TYPE_COMBAT/lite":      "/api/v1/roster/{roster}/lite",
	"/api/v1/roster/1/lite":                       "/api/v1/roster/{roster}/lite",
	"/api/v1/roster/2/lite":                       "/api/v1/roster/{roster}/lite",
	"/api/v1/roster/ROSTER_TYPE_UNSPECIFIED/lite": "/api/v1/roster/{roster}/lite",
	// S1 uniforms: name/number forms, zero enum.
	"/api/v1/s1/uniforms/ROSTER_TYPE_COMBAT": "/api/v1/s1/uniforms/{roster}",
	"/api/v1/s1/uniforms/1":                  "/api/v1/s1/uniforms/{roster}",
	"/api/v1/s1/uniforms/0":                  "/api/v1/s1/uniforms/{roster}",
	"/api/v1/does/not/exist":                 "", // off-spec: unknown-path tier (mux behavior, not an operation)
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

	// The live-witnessed statuses (spec carve-outs the FROZEN corpus cannot
	// witness — TestNewStack_LiteAndS1OutagesAreInternalJSON pins the frozen
	// bodies). The subtests are DRIVEN by the internal/spectest registry the
	// contract-side carve-out map is built from, with a 1:1 meta-assertion
	// in both directions, so the coupling is mechanical: deleting a witness
	// spec here orphans its registry entry and fails below; deleting a
	// registry entry while the spec still declares the status fails
	// contract's TestSpec_DeclaredStatusesAreCorpusWitnessed. No editing
	// order silently suppresses the invariant (ratified at #127 review).
	type liveWitnessSpec struct {
		status int
		path   string
		ds     *fakeDatastore
	}
	liveWitnesses := map[string]liveWitnessSpec{
		"lite_roster_500_outage": {
			status: http.StatusInternalServerError,
			path:   "/api/v1/roster/ROSTER_TYPE_COMBAT/lite",
			ds: &fakeDatastore{
				findLiteRosterByType: func(proto.RosterType) (*proto.LiteRoster, error) { return nil, io.ErrUnexpectedEOF },
			},
		},
		"s1_uniforms_500_outage": {
			status: http.StatusInternalServerError,
			path:   "/api/v1/s1/uniforms/ROSTER_TYPE_COMBAT",
			ds: &fakeDatastore{
				findS1UniformsRosterByType: func(proto.RosterType) (*proto.S1UniformsRoster, error) { return nil, io.ErrUnexpectedEOF },
			},
		},
	}
	registered := map[string]bool{}
	for _, lw := range spectest.LiveWitnessedStatuses {
		require.False(t, registered[lw.Witness],
			"registry names witness %q twice", lw.Witness)
		registered[lw.Witness] = true
		spec, ok := liveWitnesses[lw.Witness]
		require.True(t, ok,
			"registry entry %s %s names witness %q but no witness spec exists — an entry without an asserting witness is a spec bug, remove the entry or write the witness",
			lw.Op, lw.Status, lw.Witness)
		require.Equal(t, lw.Status, strconv.Itoa(spec.status),
			"witness %q observes a different status than its registry entry declares", lw.Witness)
		ran := false
		t.Run(lw.Witness, func(t *testing.T) {
			oh := rest.New(spec.ds, &stubReferenceCache{})
			g, _, err := contract.RunCase(oh, contract.Case{
				Name: lw.Witness, Method: http.MethodGet, Path: spec.path, Auth: contract.AuthRead,
				Notes: "synthesized: live witness for the spec's explicit " + lw.Status + " (internal/spectest registry)",
			})
			require.NoError(t, err)
			require.Equal(t, spec.status, g.Status)
			validateObserved(t, model, rv, spec.path, g)
			ran = true
		})
		require.True(t, ran,
			"witness %q did not run to completion — a skipped witness leaves its carve-out unenforced", lw.Witness)
	}
	for name := range liveWitnesses {
		require.True(t, registered[name],
			"witness spec %q has no registry entry — remove the spec or register the carve-out in internal/spectest", name)
	}
}
