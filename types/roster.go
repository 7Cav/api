package types

import (
	"fmt"
	"strconv"
)

// Roster is the full-roster response shape: the requested roster's complete
// member set as full Profile views, keyed by milpac relation id (uint64 —
// decimal string keys on the wire, the encoding/json integer-key form that
// matches protojson's map encoding; note encoding/json sorts the stringified
// keys LEXICALLY ("10" before "2") where protojson ordered them numerically,
// so responses are JSON-equal, not byte-equal, to the old stack). The
// profiles map is always allocated:
// an empty roster serializes as {"profiles":{}}, never null (the allocation
// discipline lives in the handler's mapper; the reserve_empty golden proves
// it).
type Roster struct {
	Profiles map[uint64]*Profile `json:"profiles"`
}

// RosterType identifies which roster is requested. The numeric values are
// the upstream roster_id foreign key (xf_nf_rosters_* tables) and match the
// proto enum being retired at Phase 4 exactly; the wire form is the NAME
// string.
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

// rosterTypeNames is the wire-name catalog, keyed by enum value.
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

// rosterTypeValues is the inverse of the wire-name catalog: enum NAME →
// value, built from rosterTypeNames so the two can never drift. It mirrors
// the generated proto.RosterType_value map the old gateway bound path
// parameters with.
var rosterTypeValues = func() map[string]RosterType {
	m := make(map[string]RosterType, len(rosterTypeNames))
	for v, name := range rosterTypeNames {
		m[name] = v
	}
	return m
}()

// ParseRosterType is the inverse parse for the RosterType path parameter,
// mirroring grpc-gateway's runtime.Enum (v2.29.0 runtime/convert.go) exactly
// — both accepted forms are golden-pinned (PRD #112):
//
//   - the enum NAME (ROSTER_TYPE_COMBAT), looked up in the catalog;
//   - the enum NUMBER (the upstream roster_id FK), parsed via the gateway's
//     runtime.Int32 = strconv.ParseInt(val, 0, 32) — BASE 0, so Go
//     integer-literal forms (0x1 hex, 0b1 binary, leading-0 octal, digit
//     underscores) parse too (the same frozen leniency tier as the profile
//     path id). A parsed number must be a CATALOGED value: the gateway
//     rejected numbers outside the enum's value set.
//
// Anything else fails with the gateway's leaked enum-parse text — "%s is not
// valid" — which the roster handlers wrap in the frozen type-mismatch 400.
func ParseRosterType(val string) (RosterType, error) {
	if v, ok := rosterTypeValues[val]; ok {
		return v, nil
	}
	i, err := strconv.ParseInt(val, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("%s is not valid", val)
	}
	if _, ok := rosterTypeNames[RosterType(i)]; !ok {
		return 0, fmt.Errorf("%s is not valid", val)
	}
	return RosterType(i), nil
}
