package types

import (
	"fmt"
	"strconv"
)

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
