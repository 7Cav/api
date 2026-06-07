package types_test

import (
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both golden-pinned path forms parse: the enum NAME and the enum NUMBER
// (the upstream roster_id FK) land on the same value — gateway runtime.Enum
// parity (PRD #112).
func TestParseRosterType_NameAndNumberForms(t *testing.T) {
	cases := []struct {
		in   string
		want types.RosterType
	}{
		{"ROSTER_TYPE_UNSPECIFIED", types.RosterTypeUnspecified},
		{"0", types.RosterTypeUnspecified},
		{"ROSTER_TYPE_COMBAT", types.RosterTypeCombat},
		{"1", types.RosterTypeCombat},
		{"ROSTER_TYPE_RESERVE", types.RosterTypeReserve},
		{"2", types.RosterTypeReserve},
		{"ROSTER_TYPE_ELOA", types.RosterTypeEloa},
		{"3", types.RosterTypeEloa},
		{"ROSTER_TYPE_WALL_OF_HONOR", types.RosterTypeWallOfHonor},
		{"4", types.RosterTypeWallOfHonor},
		{"ROSTER_TYPE_ARLINGTON", types.RosterTypeArlington},
		{"5", types.RosterTypeArlington},
		{"ROSTER_TYPE_PAST_MEMBERS", types.RosterTypePastMembers},
		{"6", types.RosterTypePastMembers},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := types.ParseRosterType(c.in)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

// The number form inherits the gateway's BASE-0 parse (runtime.Int32 =
// strconv.ParseInt(val, 0, 32)): Go integer-literal prefixes parse — the
// same frozen leniency tier as the profile path id. A literal that parses to
// a number OUTSIDE the catalog still fails (the gateway checked the enum's
// value set).
func TestParseRosterType_NumberFormIsBaseZero(t *testing.T) {
	got, err := types.ParseRosterType("0x1")
	require.NoError(t, err)
	assert.Equal(t, types.RosterTypeCombat, got, "hex form parses (base 0)")

	got, err = types.ParseRosterType("0b10")
	require.NoError(t, err)
	assert.Equal(t, types.RosterTypeReserve, got, "binary form parses (base 0)")

	got, err = types.ParseRosterType("06")
	require.NoError(t, err)
	assert.Equal(t, types.RosterTypePastMembers, got, "leading-0 octal form parses (base 0)")

	_, err = types.ParseRosterType("1_0")
	require.EqualError(t, err, "1_0 is not valid", "parses to 10 — outside the catalog")
}

// Anything else fails with the gateway's leaked enum-parse text verbatim —
// the bogus_enum golden pins the wrapped form.
func TestParseRosterType_InvalidLiterals(t *testing.T) {
	for _, in := range []string{
		"IMAGINARY_ROSTER",    // unknown name
		"roster_type_combat",  // names are case-sensitive
		"7",                   // number outside the catalog
		"-1",                  // negative, outside the catalog
		"09",                  // octal with a 9 — invalid syntax under base 0
		"",                    // empty
		"ROSTER_TYPE_COMBAT ", // stray whitespace
	} {
		t.Run(in, func(t *testing.T) {
			_, err := types.ParseRosterType(in)
			require.EqualError(t, err, in+" is not valid")
		})
	}
}
