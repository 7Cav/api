package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// marshalToMap round-trips v through encoding/json into a generic map so the
// assertions read the wire form a client would parse — names, presence and
// JSON kinds — not Go struct internals.
func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m), "wire form must be a JSON object: %s", raw)
	return m
}

// The populated form: lowerCamelCase names, 64-bit rankId as a JSON string,
// 32-bit rankDisplayOrder as a JSON number — the protojson conventions the
// golden corpus freezes (see contract/goldens/milpacs/ranks.golden.json).
func TestRankExpanded_WireForm(t *testing.T) {
	m := marshalToMap(t, types.RankExpanded{
		RankShort:        "MG",
		RankFull:         "Major General",
		RankImageUrl:     "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618",
		RankId:           4,
		RankDisplayOrder: 4,
	})

	assert.Equal(t, map[string]any{
		"rankShort":        "MG",
		"rankFull":         "Major General",
		"rankImageUrl":     "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618",
		"rankId":           "4", // 64-bit int: decimal-string wire form
		"rankDisplayOrder": float64(4),
	}, m)
}

// Emit-everything: the zero value still serializes every field — no
// omitempty anywhere. An absent key, "" and 0 are distinct wire states and
// the contract requires presence.
func TestRankExpanded_ZeroValueEmitsEveryField(t *testing.T) {
	m := marshalToMap(t, types.RankExpanded{})

	assert.Equal(t, map[string]any{
		"rankShort":        "",
		"rankFull":         "",
		"rankImageUrl":     "",
		"rankId":           "0",
		"rankDisplayOrder": float64(0),
	}, m)
}

// RanksResponse wraps the list under "ranks". The handler owns the
// allocation discipline (an empty list must be [] not null — goldens enforce
// it); the type's job is the envelope and field name.
func TestRanksResponse_WireForm(t *testing.T) {
	raw, err := json.Marshal(types.RanksResponse{Ranks: []*types.RankExpanded{}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ranks":[]}`, string(raw))
}
