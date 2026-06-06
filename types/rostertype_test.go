package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Enums serialize as their NAME string (protojson convention); the zero
// value is zero-safe by construction — it emits the _UNSPECIFIED name, so a
// forgotten assignment can never leak a bare 0 onto the wire.
func TestRosterType_MarshalsAsNameString(t *testing.T) {
	cases := []struct {
		value types.RosterType
		want  string
	}{
		{types.RosterTypeUnspecified, `"ROSTER_TYPE_UNSPECIFIED"`},
		{types.RosterType(0), `"ROSTER_TYPE_UNSPECIFIED"`}, // zero value == unspecified
		{types.RosterTypeCombat, `"ROSTER_TYPE_COMBAT"`},
		{types.RosterTypeReserve, `"ROSTER_TYPE_RESERVE"`},
		{types.RosterTypeEloa, `"ROSTER_TYPE_ELOA"`},
		{types.RosterTypeWallOfHonor, `"ROSTER_TYPE_WALL_OF_HONOR"`},
		{types.RosterTypeArlington, `"ROSTER_TYPE_ARLINGTON"`},
		{types.RosterTypePastMembers, `"ROSTER_TYPE_PAST_MEMBERS"`},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.value)
		require.NoError(t, err)
		assert.Equal(t, c.want, string(raw))
	}
}

// A value with no registered name falls back to the bare number — exactly
// what protojson emits for an unknown enum value, so upstream data the
// catalog hasn't caught up with still round-trips instead of crashing.
func TestRosterType_UnknownValueMarshalsAsNumber(t *testing.T) {
	raw, err := json.Marshal(types.RosterType(99))
	require.NoError(t, err)
	assert.Equal(t, `99`, string(raw))
}

// String mirrors the wire name (and the number fallback) so enum values
// format the same way in error messages as they do in JSON — the roster
// handlers interpolate the enum into frozen message strings.
func TestRosterType_StringMatchesWireName(t *testing.T) {
	assert.Equal(t, "ROSTER_TYPE_COMBAT", types.RosterTypeCombat.String())
	assert.Equal(t, "ROSTER_TYPE_UNSPECIFIED", types.RosterType(0).String())
	assert.Equal(t, "99", types.RosterType(99).String())
}
