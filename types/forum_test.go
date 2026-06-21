package types_test

import (
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
)

// ForumGroup is one entry of the forum permission-group directory
// (GET /api/v1/forum/groups). groupId is a 32-bit id → JSON number (not a
// decimal string like the 64-bit milpac ids), groupName a plain string. The
// zero value still emits every field (no omitempty).
func TestForumGroup_ZeroValueEmitsEveryField(t *testing.T) {
	m := marshalToMap(t, types.ForumGroup{})

	assert.Equal(t, map[string]any{
		"groupId":   float64(0), // 32-bit id: JSON number, never a string
		"groupName": "",
	}, m)
}

func TestForumGroup_WireForm(t *testing.T) {
	m := marshalToMap(t, types.ForumGroup{GroupId: 7, GroupName: "Administrator"})

	assert.Equal(t, map[string]any{
		"groupId":   float64(7),
		"groupName": "Administrator",
	}, m)
}
