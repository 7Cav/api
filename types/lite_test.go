package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// LiteRoster's profiles map keys are uint64 relation ids: encoding/json
// emits integer map keys as decimal STRINGS and sorts them — the protojson
// map<uint64, LiteProfile> wire form the goldens freeze
// (contract/goldens/position/search_happy.golden.json: {"1": {...}}).
func TestLiteRoster_MapKeysAreDecimalStrings(t *testing.T) {
	raw, err := json.Marshal(types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{
		1: {},
	}})
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	profiles, ok := m["profiles"].(map[string]any)
	require.True(t, ok, "profiles must be a JSON object: %s", raw)
	assert.Contains(t, profiles, "1", "uint64 key 1 must serialize as the string key \"1\"")
}

// An allocated-but-empty profiles map is {} on the wire — the frozen
// empty-result form ({"profiles":{}}, see #137). The ALLOCATION discipline
// lives in the handler mappers: a nil map would marshal as null, so this
// pins the allocated half the goldens enforce end-to-end.
func TestLiteRoster_EmptyAllocatedMapIsEmptyObject(t *testing.T) {
	raw, err := json.Marshal(types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"profiles":{}}`, string(raw))
}

// Emit-everything on the lite shape: the zero value serializes every field —
// unset nested messages null, the secondaries List [], strings "" — and
// carries NO keycloakId (the documented break) and NO records/awards (the
// lite cut).
func TestLiteProfile_ZeroValueEmitsEveryField(t *testing.T) {
	m := marshalToMap(t, types.LiteProfile{})

	assert.Equal(t, map[string]any{
		"user":              nil,
		"rank":              nil,
		"realName":          "",
		"uniformUrl":        "",
		"roster":            "ROSTER_TYPE_UNSPECIFIED",
		"primary":           nil,
		"secondaries":       []any{},
		"joinDate":          "",
		"promotionDate":     "",
		"discordId":         "",
		"awardDate":         "",
		"recordDate":        "",
		"lastForumPostDate": "",
		"mos":               "",
		"consoleGamertag":   "",
	}, m)
}
