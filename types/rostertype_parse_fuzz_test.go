package types_test

import (
	"testing"

	"github.com/7cav/api/types"
)

// FuzzParseRosterType fuzzes the RosterType path-parameter parser — the
// name-or-number enum binding the goldens freeze (PRD #112). Two invariants
// hold for any input:
//
//   - it never panics (base-0 ParseInt sees hex/octal/binary/underscore forms,
//     huge magnitudes, negatives, empty — all must resolve to a clean error,
//     never a crash);
//   - a non-error result is always a CATALOGED value that round-trips: its
//     name re-parses to the identical value. This is the property the handler
//     relies on when it marshals the bound enum back as its name string — a
//     value that parsed but had no name would emit a broken wire enum.
//
// Run: go test ./types -run x -fuzz FuzzParseRosterType
func FuzzParseRosterType(f *testing.F) {
	for _, seed := range []string{
		"ROSTER_TYPE_COMBAT",       // canonical name
		"1",                        // decimal number form
		"0x1",                      // base-0 hex (frozen leniency)
		"0b10",                     // base-0 binary
		"01",                       // leading-zero octal
		"1_0",                      // digit underscores
		"ROSTER_TYPE_UNSPECIFIED",  // zero value
		"99",                       // numeric but uncataloged
		"-1",                       // negative
		"99999999999999999999",     // overflows int32
		"combat",                   // lowercase, not a name
		"",                         // empty
		"ROSTER_TYPE_COMBAT ",      // trailing space (not trimmed)
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, val string) {
		got, err := types.ParseRosterType(val)
		if err != nil {
			return // any rejection is fine — the only forbidden outcome is a panic
		}
		// Parsed clean → must be cataloged and round-trip through its name.
		name := got.String()
		back, err := types.ParseRosterType(name)
		if err != nil {
			t.Fatalf("ParseRosterType(%q) = %v (name %q), but its name failed to re-parse: %v", val, got, name, err)
		}
		if back != got {
			t.Fatalf("round-trip mismatch: ParseRosterType(%q) = %v, but ParseRosterType(%q) = %v", val, got, name, back)
		}
	})
}
