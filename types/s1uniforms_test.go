package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The populated S1 uniforms form: trimmed identity (rank without rankId,
// title-only secondaries, primary collapsed to a title string) plus the
// uniform-audit fields — the shape the s1 goldens freeze
// (contract/goldens/s1/uniforms_combat_by_name.golden.json).
func TestS1UniformsProfile_WireForm(t *testing.T) {
	m := marshalToMap(t, types.S1UniformsProfile{
		User:                     &types.User{UserId: 3, Username: "Jarvis.A"},
		Rank:                     &types.S1UniformsRank{RankShort: "MG", RankFull: "Major General", RankImageUrl: "img"},
		RealName:                 "Adam Jarvis",
		UniformUrl:               "uniform",
		UniformDate:              "2025-11-02",
		UniformUpdateTriggerDate: "2025-12-01",
		Roster:                   types.RosterTypeCombat,
		PrimaryPositionTitle:     "Regimental Technical Aide",
		Secondaries:              []*types.S1UniformsPosition{{PositionTitle: "S6 Web Developer"}},
		JoinDate:                 "2014-02-08",
		PromotionDate:            "2020-10-17",
		AreaOfResponsibility:     "S6",
	})

	assert.Equal(t, map[string]any{
		"user":                     map[string]any{"userId": "3", "username": "Jarvis.A"},
		"rank":                     map[string]any{"rankShort": "MG", "rankFull": "Major General", "rankImageUrl": "img"},
		"realName":                 "Adam Jarvis",
		"uniformUrl":               "uniform",
		"uniformDate":              "2025-11-02",
		"uniformUpdateTriggerDate": "2025-12-01",
		"roster":                   "ROSTER_TYPE_COMBAT",
		"primaryPositionTitle":     "Regimental Technical Aide",
		"secondaries":              []any{map[string]any{"positionTitle": "S6 Web Developer"}},
		"joinDate":                 "2014-02-08",
		"promotionDate":            "2020-10-17",
		"areaOfResponsibility":     "S6",
	}, m)
}

// Emit-everything on the S1 shape: unset strings "", empty collections [] —
// the sparse member of the uniforms_combat goldens.
func TestS1UniformsProfile_SparseFormEmitsEmpties(t *testing.T) {
	m := marshalToMap(t, types.S1UniformsProfile{
		User:                 &types.User{UserId: 8, Username: "John.Doe"},
		Rank:                 &types.S1UniformsRank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "img22"},
		RealName:             "John Doe",
		UniformUrl:           "uniform2",
		Roster:               types.RosterTypeCombat,
		PrimaryPositionTitle: "Rifleman",
		Secondaries:          []*types.S1UniformsPosition{},
		JoinDate:             "2026-01-15",
	})

	assert.Equal(t, []any{}, m["secondaries"])
	assert.Equal(t, "", m["uniformDate"])
	assert.Equal(t, "", m["uniformUpdateTriggerDate"])
	assert.Equal(t, "", m["areaOfResponsibility"])
	assert.Len(t, m, 12, "every S1UniformsProfile key present, nothing extra")
}

// A zero-value S1UniformsProfile is wire-valid (the List convention).
func TestS1UniformsProfile_ZeroValueMarshalsWireValid(t *testing.T) {
	b, err := json.Marshal(types.S1UniformsProfile{})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"secondaries":[]`)
}

func TestS1UniformsRoster_EmptyMapWireForm(t *testing.T) {
	b, err := json.Marshal(types.S1UniformsRoster{Profiles: map[uint64]*types.S1UniformsProfile{}})
	require.NoError(t, err)
	assert.Equal(t, `{"profiles":{}}`, string(b))
}
