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
