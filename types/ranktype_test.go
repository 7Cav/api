package types

import (
	"strconv"
	"testing"
)

// TestRankType_String_MatchesProtoNames pins a representative set of rank ids
// to their wire names. The values are cross-checked against the generated
// proto RankType_name table that mysql.go relied on before the type-swap, so
// strings.TrimPrefix(RankType(id).String(), "RANK_TYPE_") stays byte-identical
// to the old proto-derived RankShort.
func TestRankType_String_MatchesProtoNames(t *testing.T) {
	cases := []struct {
		id   RankType
		name string
	}{
		{0, "RANK_TYPE_UNSPECIFIED"},
		{1, "RANK_TYPE_GOA"},
		{2, "RANK_TYPE_GEN"},
		{6, "RANK_TYPE_COL"},
		{9, "RANK_TYPE_CPT"},
		{18, "RANK_TYPE_SGT"},
		{22, "RANK_TYPE_PVT"},
		{23, "RANK_TYPE_RCT"},
		{26, "RANK_TYPE_CW5"},
		{29, "RANK_TYPE_CW2"},
		{30, "RANK_TYPE_WO1"},
		{31, "RANK_TYPE_AR"},
	}

	for _, c := range cases {
		if got := c.id.String(); got != c.name {
			t.Errorf("RankType(%d).String() = %q, want %q", c.id, got, c.name)
		}
	}
}

// TestRankType_String_GapAndUnknownFallBackToDecimal pins the 24/25 gap in the
// proto enum (and any other uncataloged value) to the bare decimal fallback —
// matching the generated proto String(), so an unknown rank id yields its
// number, not an empty short name.
func TestRankType_String_GapAndUnknownFallBackToDecimal(t *testing.T) {
	for _, id := range []RankType{24, 25, 99, -1} {
		want := strconv.FormatInt(int64(id), 10)
		if got := id.String(); got != want {
			t.Errorf("RankType(%d).String() = %q, want %q", id, got, want)
		}
	}
}

// TestRankType_RankShort_StripsPrefix checks the actual RankShort derivation
// the datastore performs, through the public RankShort() method.
func TestRankType_RankShort_StripsPrefix(t *testing.T) {
	cases := map[RankType]string{
		1:  "GOA",
		6:  "COL",
		23: "RCT",
		26: "CW5",
		31: "AR",
	}
	for id, short := range cases {
		if got := id.RankShort(); got != short {
			t.Errorf("RankType(%d).RankShort() = %q, want %q", id, got, short)
		}
	}
}
