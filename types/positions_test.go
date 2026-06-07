package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The populated form: lowerCamelCase names, 64-bit positionId as a JSON
// string, 32-bit positionDisplayOrder as a JSON number, and the boolean
// EMITTED even when false — the protojson conventions the golden corpus
// freezes (see contract/goldens/milpacs/position_groups.golden.json).
func TestPositionExpanded_WireForm(t *testing.T) {
	m := marshalToMap(t, types.PositionExpanded{
		PositionTitle:             "Regimental Technical Aide",
		PositionId:                773,
		PositionDisplayOrder:      3,
		PositionPossibleSecondary: false,
	})

	assert.Equal(t, map[string]any{
		"positionTitle":             "Regimental Technical Aide",
		"positionId":                "773", // 64-bit int: decimal-string wire form
		"positionDisplayOrder":      float64(3),
		"positionPossibleSecondary": false, // false booleans emitted (no omitempty)
	}, m)
}

// Emit-everything: the zero value still serializes every field, and the
// nested positions List is [] even unallocated — never null.
func TestPositionGroup_ZeroValueEmitsEveryField(t *testing.T) {
	m := marshalToMap(t, types.PositionGroup{})

	assert.Equal(t, map[string]any{
		"groupId":           "0",
		"groupTitle":        "",
		"groupDisplayOrder": float64(0),
		"positions":         []any{},
	}, m)
}

// PositionGroupsResponse wraps the list under "groups"; the zero value is
// wire-valid as {"groups":[]} (List semantics).
func TestPositionGroupsResponse_WireForm(t *testing.T) {
	raw, err := json.Marshal(types.PositionGroupsResponse{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"groups":[]}`, string(raw))
}
