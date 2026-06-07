package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The populated form: every 64-bit field (userId, timestamp, postId,
// milpacId) is a decimal-string on the wire — the protojson convention the
// golden corpus freezes (see contract/goldens/milpacs/awol.golden.json).
func TestAwol_WireForm(t *testing.T) {
	m := marshalToMap(t, types.Awol{
		GroupName: "Alpha Company",
		RankName:  "Private",
		Username:  "John.Doe",
		UserId:    8,
		HumanDate: "2026-05-01",
		Timestamp: 1777600000,
		PostId:    445566,
		MilpacId:  2,
	})

	assert.Equal(t, map[string]any{
		"groupName": "Alpha Company",
		"rankName":  "Private",
		"username":  "John.Doe",
		"userId":    "8",
		"humanDate": "2026-05-01",
		"timestamp": "1777600000",
		"postId":    "445566",
		"milpacId":  "2",
	}, m)
}

// Emit-everything: the zero value still serializes every field — no
// omitempty anywhere.
func TestAwol_ZeroValueEmitsEveryField(t *testing.T) {
	m := marshalToMap(t, types.Awol{})

	assert.Equal(t, map[string]any{
		"groupName": "",
		"rankName":  "",
		"username":  "",
		"userId":    "0",
		"humanDate": "",
		"timestamp": "0",
		"postId":    "0",
		"milpacId":  "0",
	}, m)
}

// AwolResponse wraps the list under "awols"; the zero value is wire-valid as
// {"awols":[]} (List semantics).
func TestAwolResponse_WireForm(t *testing.T) {
	raw, err := json.Marshal(types.AwolResponse{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"awols":[]}`, string(raw))
}
