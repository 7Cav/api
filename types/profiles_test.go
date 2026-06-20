package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The populated form: every nested message present, 64-bit ids as decimal
// strings, enums as name strings — the shape the profile goldens freeze
// (contract/goldens/milpacs/profile_by_id_happy.golden.json).
func TestProfile_WireForm(t *testing.T) {
	m := marshalToMap(t, types.Profile{
		User:        &types.User{UserId: 3, Username: "Jarvis.A"},
		Rank:        &types.Rank{RankShort: "MG", RankFull: "Major General", RankImageUrl: "img", RankId: 4},
		RealName:    "Adam Jarvis",
		UniformUrl:  "uniform",
		Roster:      types.RosterTypeCombat,
		Primary:     &types.Position{PositionTitle: "Regimental Technical Aide", PositionId: 773},
		Secondaries: []*types.Position{{PositionTitle: "S6 Web Developer", PositionId: 812}},
		Records: []*types.Record{
			{RecordDetails: "Promoted", RecordType: types.RecordTypePromotion, RecordDate: "2020-10-17", RecordUid: 46},
		},
		Awards: []*types.Award{
			{AwardDetails: "details", AwardName: "Commendation Medal", AwardDate: "2021-03-01", AwardImageUrl: "ccm", AwardUid: 901},
		},
		JoinDate:          "2014-02-08",
		PromotionDate:     "2020-10-17",
		DiscordId:         "112233445566778899",
		LastForumPostDate: "2026-05-30",
		Mos:               "11B",
		ConsoleGamertag:   "",
	})

	assert.Equal(t, map[string]any{
		"user":        map[string]any{"userId": "3", "username": "Jarvis.A"},
		"rank":        map[string]any{"rankShort": "MG", "rankFull": "Major General", "rankImageUrl": "img", "rankId": "4"},
		"realName":    "Adam Jarvis",
		"uniformUrl":  "uniform",
		"roster":      "ROSTER_TYPE_COMBAT",
		"primary":     map[string]any{"positionTitle": "Regimental Technical Aide", "positionId": "773"},
		"secondaries": []any{map[string]any{"positionTitle": "S6 Web Developer", "positionId": "812"}},
		"records": []any{map[string]any{
			"recordDetails": "Promoted", "recordType": "RECORD_TYPE_PROMOTION", "recordDate": "2020-10-17", "recordUid": "46",
		}},
		"awards": []any{map[string]any{
			"awardDetails": "details", "awardName": "Commendation Medal", "awardDate": "2021-03-01", "awardImageUrl": "ccm", "awardUid": "901",
		}},
		"joinDate":          "2014-02-08",
		"promotionDate":     "2020-10-17",
		"discordId":         "112233445566778899",
		"lastForumPostDate": "2026-05-30",
		"mos":               "11B",
		"consoleGamertag":   "",
	}, m)
}

// Emit-everything: unset nested messages are null, empty (allocated)
// collections are [], unset strings are "" — and NO keycloakId key anywhere:
// the new types never had the field (the corpus transform documents the
// break; route-level Keycloak removal rides cutover #134).
func TestProfile_SparseFormEmitsNullsAndEmpties(t *testing.T) {
	m := marshalToMap(t, types.Profile{
		User:        &types.User{UserId: 8, Username: "John.Doe"},
		Rank:        &types.Rank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "img22", RankId: 22},
		RealName:    "John Doe",
		UniformUrl:  "uniform2",
		Roster:      types.RosterTypeCombat,
		Primary:     nil,
		Secondaries: []*types.Position{},
		Records:     []*types.Record{},
		Awards:      []*types.Award{},
		JoinDate:    "2026-01-15",
	})

	assert.Equal(t, map[string]any{
		"user":              map[string]any{"userId": "8", "username": "John.Doe"},
		"rank":              map[string]any{"rankShort": "PVT", "rankFull": "Private", "rankImageUrl": "img22", "rankId": "22"},
		"realName":          "John Doe",
		"uniformUrl":        "uniform2",
		"roster":            "ROSTER_TYPE_COMBAT",
		"primary":           nil,
		"secondaries":       []any{},
		"records":           []any{},
		"awards":            []any{},
		"joinDate":          "2026-01-15",
		"promotionDate":     "",
		"discordId":         "",
		"lastForumPostDate": "",
		"mos":               "",
		"consoleGamertag":   "",
	}, m)
	require.NotContains(t, m, "keycloakId")
}

// json.Marshal must never see a keycloak field even via embedding tricks —
// pin the raw bytes too, belt and braces for the documented break.
func TestProfile_NoKeycloakIdOnTheWire(t *testing.T) {
	raw, err := json.Marshal(types.Profile{})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "keycloak")
}

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
