package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The populated lite form: the full Profile minus records/awards arrays,
// plus the two scalar summary dates — the shape the lite roster goldens
// freeze (contract/goldens/roster/lite_combat_by_name.golden.json).
func TestLiteProfile_WireForm(t *testing.T) {
	m := marshalToMap(t, types.LiteProfile{
		User:              &types.User{UserId: 3, Username: "Jarvis.A"},
		Rank:              &types.Rank{RankShort: "MG", RankFull: "Major General", RankImageUrl: "img", RankId: 4},
		RealName:          "Adam Jarvis",
		UniformUrl:        "uniform",
		Roster:            types.RosterTypeCombat,
		Primary:           &types.Position{PositionTitle: "Regimental Technical Aide", PositionId: 773},
		Secondaries:       []*types.Position{{PositionTitle: "S6 Web Developer", PositionId: 812}},
		JoinDate:          "2014-02-08",
		PromotionDate:     "2020-10-17",
		DiscordId:         "112233445566778899",
		AwardDate:         "2021-03-01",
		RecordDate:        "2020-10-17",
		LastForumPostDate: "2026-05-30",
		Mos:               "11B",
		ConsoleGamertag:   "",
	})

	assert.Equal(t, map[string]any{
		"user":              map[string]any{"userId": "3", "username": "Jarvis.A"},
		"rank":              map[string]any{"rankShort": "MG", "rankFull": "Major General", "rankImageUrl": "img", "rankId": "4"},
		"realName":          "Adam Jarvis",
		"uniformUrl":        "uniform",
		"roster":            "ROSTER_TYPE_COMBAT",
		"primary":           map[string]any{"positionTitle": "Regimental Technical Aide", "positionId": "773"},
		"secondaries":       []any{map[string]any{"positionTitle": "S6 Web Developer", "positionId": "812"}},
		"joinDate":          "2014-02-08",
		"promotionDate":     "2020-10-17",
		"discordId":         "112233445566778899",
		"awardDate":         "2021-03-01",
		"recordDate":        "2020-10-17",
		"lastForumPostDate": "2026-05-30",
		"mos":               "11B",
		"consoleGamertag":   "",
	}, m)
}

// Emit-everything on the lite shape: unset nested messages null, empty
// collections [], unset strings "" — and NO keycloakId key (the new types
// never had the field; same documented break as Profile).
func TestLiteProfile_SparseFormEmitsNullsAndEmpties(t *testing.T) {
	m := marshalToMap(t, types.LiteProfile{
		User:        &types.User{UserId: 8, Username: "John.Doe"},
		Rank:        &types.Rank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "img22", RankId: 22},
		RealName:    "John Doe",
		UniformUrl:  "uniform2",
		Roster:      types.RosterTypeCombat,
		Secondaries: []*types.Position{},
		JoinDate:    "2026-01-15",
	})

	assert.Nil(t, m["primary"])
	assert.Equal(t, []any{}, m["secondaries"])
	assert.Equal(t, "", m["awardDate"])
	assert.Equal(t, "", m["recordDate"])
	assert.Equal(t, "", m["promotionDate"])
	assert.NotContains(t, m, "keycloakId")
	assert.Len(t, m, 15, "every LiteProfile key present, nothing extra")
}

// A zero-value LiteProfile is wire-valid (the List convention): secondaries
// marshals as [], never null.
func TestLiteProfile_ZeroValueMarshalsWireValid(t *testing.T) {
	b, err := json.Marshal(types.LiteProfile{})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"secondaries":[]`)
	assert.Contains(t, string(b), `"roster":"ROSTER_TYPE_UNSPECIFIED"`)
}

// The roster map keys on the milpac relation id and serializes uint64 keys
// as decimal JSON strings (encoding/json integer-key form — matching the
// protojson map encoding the goldens froze). An allocated-but-empty map is
// {} — the empty-roster wire form.
func TestRoster_MapWireForm(t *testing.T) {
	b, err := json.Marshal(types.Roster{Profiles: map[uint64]*types.Profile{}})
	require.NoError(t, err)
	assert.Equal(t, `{"profiles":{}}`, string(b))

	m := marshalToMap(t, types.Roster{Profiles: map[uint64]*types.Profile{42: {}}})
	profiles, ok := m["profiles"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, profiles, "42", "uint64 keys serialize as decimal strings")
}

func TestLiteRoster_EmptyMapWireForm(t *testing.T) {
	b, err := json.Marshal(types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{}})
	require.NoError(t, err)
	assert.Equal(t, `{"profiles":{}}`, string(b))
}
