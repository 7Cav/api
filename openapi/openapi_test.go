package openapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 1 deprecation surface for GetUserViaKeycloakId (issue #97).
// OpenAPI 2.0 supports `deprecated` on operations and parameters but NOT on
// schema properties (added in OpenAPI 3.0). This test pins what the
// swagger.json — i.e. the API doc surface — actually advertises to consumers.
// Schema-property deprecation for Profile.keycloak_id / LiteProfile.keycloak_id
// is asserted via the proto descriptor in proto_deprecation_test.go.
func TestMilpacsSwagger_DeprecatesKeycloakIdSurface(t *testing.T) {
	raw, err := Files.ReadFile("assets/milpacs.swagger.json")
	require.NoError(t, err)

	var spec struct {
		Paths map[string]map[string]struct {
			Deprecated bool `json:"deprecated"`
			Parameters []struct {
				Name       string `json:"name"`
				Deprecated bool   `json:"deprecated"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &spec))

	op, ok := spec.Paths["/api/v1/milpac/keycloak/{keycloakId}"]["get"]
	require.True(t, ok, "GET /api/v1/milpac/keycloak/{keycloakId} missing from spec")
	assert.True(t, op.Deprecated, "GetUserViaKeycloakId op must be deprecated")

	require.Len(t, op.Parameters, 1)
	assert.Equal(t, "keycloakId", op.Parameters[0].Name)
	assert.True(t, op.Parameters[0].Deprecated, "keycloakId path parameter must be deprecated")
}
