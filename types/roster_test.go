package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The populated lite form: the full Profile minus records/awards arrays,
// plus the two scalar summary dates — the shape the lite roster goldens
// freeze (contract/goldens/roster/lite_combat_by_name.golden.json).
func TestLiteProfile_WireForm(t *testing.T) {
	m := marshalToMap(t, types.LiteProfile{
		User:              &types.User{UserId: 3, Username: "Jarvis.A"},
		Rank:              &types.Rank{RankShort: "MG", RankFull: "Major General", RankImageUrl: "img", RankId: 4},
		RealName:          "Adam Jarvis",
		UniformUrl:        "uniform",
		Roster:            types.RosterTypeCombat,
		Primary:           &types.Position{PositionTitle: "Regimental Technical Aide", PositionId: 773},
		Secondaries:       []*types.Position{{PositionTitle: "S6 Web Developer", PositionId: 812}},
		JoinDate:          "2014-02-08",
		PromotionDate:     "2020-10-17",
		DiscordId:         "112233445566778899",
		AwardDate:         "2021-03-01",
		RecordDate:        "2020-10-17",
		LastForumPostDate: "2026-05-30",
		Mos:               "11B",
		ConsoleGamertag:   "",
	})

	assert.Equal(t, map[string]any{
		"user":              map[string]any{"userId": "3", "username": "Jarvis.A"},
		"rank":              map[string]any{"rankShort": "MG", "rankFull": "Major General", "rankImageUrl": "img", "rankId": "4"},
		"realName":          "Adam Jarvis",
		"uniformUrl":        "uniform",
		"roster":            "ROSTER_TYPE_COMBAT",
		"primary":           map[string]any{"positionTitle": "Regimental Technical Aide", "positionId": "773"},
		"secondaries":       []any{map[string]any{"positionTitle": "S6 Web Developer", "positionId": "812"}},
		"joinDate":          "2014-02-08",
		"promotionDate":     "2020-10-17",
		"discordId":         "112233445566778899",
		"awardDate":         "2021-03-01",
		"recordDate":        "2020-10-17",
		"lastForumPostDate": "2026-05-30",
		"mos":               "11B",
		"consoleGamertag":   "",
	}, m)
}

// Emit-everything on the lite shape: unset nested messages null, empty
// collections [], unset strings "" — and NO keycloakId key (the new types
// never had the field; same documented break as Profile).
func TestLiteProfile_SparseFormEmitsNullsAndEmpties(t *testing.T) {
	m := marshalToMap(t, types.LiteProfile{
		User:        &types.User{UserId: 8, Username: "John.Doe"},
		Rank:        &types.Rank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "img22", RankId: 22},
		RealName:    "John Doe",
		UniformUrl:  "uniform2",
		Roster:      types.RosterTypeCombat,
		Secondaries: []*types.Position{},
		JoinDate:    "2026-01-15",
	})

	assert.Nil(t, m["primary"])
	assert.Equal(t, []any{}, m["secondaries"])
	assert.Equal(t, "", m["awardDate"])
	assert.Equal(t, "", m["recordDate"])
	assert.Equal(t, "", m["promotionDate"])
	assert.NotContains(t, m, "keycloakId")
	assert.Len(t, m, 15, "every LiteProfile key present, nothing extra")
}

// A zero-value LiteProfile is wire-valid (the List convention): secondaries
// marshals as [], never null.
func TestLiteProfile_ZeroValueMarshalsWireValid(t *testing.T) {
	b, err := json.Marshal(types.LiteProfile{})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"secondaries":[]`)
	assert.Contains(t, string(b), `"roster":"ROSTER_TYPE_UNSPECIFIED"`)
}

// The roster map keys on the milpac relation id and serializes uint64 keys
// as decimal JSON strings (encoding/json integer-key form — matching the
// protojson map encoding the goldens froze). An allocated-but-empty map is
// {} — the empty-roster wire form.
func TestRoster_MapWireForm(t *testing.T) {
	b, err := json.Marshal(types.Roster{Profiles: map[uint64]*types.Profile{}})
	require.NoError(t, err)
	assert.Equal(t, `{"profiles":{}}`, string(b))

	m := marshalToMap(t, types.Roster{Profiles: map[uint64]*types.Profile{42: {}}})
	profiles, ok := m["profiles"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, profiles, "42", "uint64 keys serialize as decimal strings")
}

func TestLiteRoster_EmptyMapWireForm(t *testing.T) {
	b, err := json.Marshal(types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{}})
	require.NoError(t, err)
	assert.Equal(t, `{"profiles":{}}`, string(b))
}

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
		"ROSTER_TYPE_COMBAT",      // canonical name
		"1",                       // decimal number form
		"0x1",                     // base-0 hex (frozen leniency)
		"0b10",                    // base-0 binary
		"01",                      // leading-zero octal
		"1_0",                     // digit underscores
		"ROSTER_TYPE_UNSPECIFIED", // zero value
		"99",                      // numeric but uncataloged
		"-1",                      // negative
		"99999999999999999999",    // overflows int32
		"combat",                  // lowercase, not a name
		"",                        // empty
		"ROSTER_TYPE_COMBAT ",     // trailing space (not trimmed)
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
