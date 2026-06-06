package types

import "strconv"

// RosterType identifies which roster is requested. The numeric values are
// the upstream roster_id foreign key (xf_nf_rosters_* tables) and match the
// retired proto enum exactly; the wire form is the NAME string.
//
// This is the worked example of the enum convention (see the package doc):
// integer-backed named type, MarshalJSON emits the name, the zero value is
// the _UNSPECIFIED name, and unnamed values fall back to the bare number.
type RosterType int32

const (
	RosterTypeUnspecified RosterType = 0
	RosterTypeCombat      RosterType = 1
	RosterTypeReserve     RosterType = 2
	RosterTypeEloa        RosterType = 3
	RosterTypeWallOfHonor RosterType = 4
	RosterTypeArlington   RosterType = 5
	RosterTypePastMembers RosterType = 6
)

// rosterTypeNames is the wire-name catalog. Index == enum value.
var rosterTypeNames = map[RosterType]string{
	RosterTypeUnspecified: "ROSTER_TYPE_UNSPECIFIED",
	RosterTypeCombat:      "ROSTER_TYPE_COMBAT",
	RosterTypeReserve:     "ROSTER_TYPE_RESERVE",
	RosterTypeEloa:        "ROSTER_TYPE_ELOA",
	RosterTypeWallOfHonor: "ROSTER_TYPE_WALL_OF_HONOR",
	RosterTypeArlington:   "ROSTER_TYPE_ARLINGTON",
	RosterTypePastMembers: "ROSTER_TYPE_PAST_MEMBERS",
}

// String returns the wire name, or the bare decimal number for values the
// catalog has no name for — mirroring the generated proto String(), so enum
// values interpolate identically into the frozen error-message strings.
func (rt RosterType) String() string {
	if name, ok := rosterTypeNames[rt]; ok {
		return name
	}
	return strconv.FormatInt(int64(rt), 10)
}

// MarshalJSON emits the enum name as a JSON string; values without a name
// emit the bare number (protojson behavior for unknown enum values).
func (rt RosterType) MarshalJSON() ([]byte, error) {
	if name, ok := rosterTypeNames[rt]; ok {
		return strconv.AppendQuote(nil, name), nil
	}
	return strconv.AppendInt(nil, int64(rt), 10), nil
}
