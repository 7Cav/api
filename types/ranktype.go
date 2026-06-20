package types

import (
	"strconv"
	"strings"
)

// RankType identifies a pay-grade entry. The numeric values are the upstream
// rank_id foreign key (xf_nf_rosters_rank) and match the proto enum being
// retired at Phase 4 exactly.
//
// Unlike RosterType and RecordType, RankType never appears on the wire as a
// named enum: it exists solely so the datastore can derive RankShort by
// stripping the RANK_TYPE_ prefix off the name (e.g. RankType(6) -> "COL").
// The name table, the 24/25 gap, and the decimal fallback for uncataloged
// values are ported verbatim from the generated proto RankType_name so the
// derived RankShort stays byte-identical across the proto -> types swap.
type RankType int32

const (
	RankTypeUnspecified RankType = 0
	RankTypeGoa         RankType = 1
	RankTypeGen         RankType = 2
	RankTypeLtg         RankType = 3
	RankTypeMg          RankType = 4
	RankTypeBg          RankType = 5
	RankTypeCol         RankType = 6
	RankTypeLtc         RankType = 7
	RankTypeMaj         RankType = 8
	RankTypeCpt         RankType = 9
	RankType1lt         RankType = 10
	RankType2lt         RankType = 11
	RankTypeCsm         RankType = 12
	RankTypeSgm         RankType = 13
	RankType1sg         RankType = 14
	RankTypeMsg         RankType = 15
	RankTypeSfc         RankType = 16
	RankTypeSsg         RankType = 17
	RankTypeSgt         RankType = 18
	RankTypeCpl         RankType = 19
	RankTypeSpc         RankType = 20
	RankTypePfc         RankType = 21
	RankTypePvt         RankType = 22
	RankTypeRct         RankType = 23
	// 24 and 25 are intentionally unassigned — the gap is preserved from the
	// proto enum so those ids fall back to their decimal string.
	RankTypeCw5 RankType = 26
	RankTypeCw4 RankType = 27
	RankTypeCw3 RankType = 28
	RankTypeCw2 RankType = 29
	RankTypeWo1 RankType = 30
	RankTypeAr  RankType = 31
)

// rankTypeNames is the wire-name catalog, keyed by enum value. Ported verbatim
// from proto.RankType_name; the 24/25 gap is intentional.
var rankTypeNames = map[RankType]string{
	RankTypeUnspecified: "RANK_TYPE_UNSPECIFIED",
	RankTypeGoa:         "RANK_TYPE_GOA",
	RankTypeGen:         "RANK_TYPE_GEN",
	RankTypeLtg:         "RANK_TYPE_LTG",
	RankTypeMg:          "RANK_TYPE_MG",
	RankTypeBg:          "RANK_TYPE_BG",
	RankTypeCol:         "RANK_TYPE_COL",
	RankTypeLtc:         "RANK_TYPE_LTC",
	RankTypeMaj:         "RANK_TYPE_MAJ",
	RankTypeCpt:         "RANK_TYPE_CPT",
	RankType1lt:         "RANK_TYPE_1LT",
	RankType2lt:         "RANK_TYPE_2LT",
	RankTypeCsm:         "RANK_TYPE_CSM",
	RankTypeSgm:         "RANK_TYPE_SGM",
	RankType1sg:         "RANK_TYPE_1SG",
	RankTypeMsg:         "RANK_TYPE_MSG",
	RankTypeSfc:         "RANK_TYPE_SFC",
	RankTypeSsg:         "RANK_TYPE_SSG",
	RankTypeSgt:         "RANK_TYPE_SGT",
	RankTypeCpl:         "RANK_TYPE_CPL",
	RankTypeSpc:         "RANK_TYPE_SPC",
	RankTypePfc:         "RANK_TYPE_PFC",
	RankTypePvt:         "RANK_TYPE_PVT",
	RankTypeRct:         "RANK_TYPE_RCT",
	RankTypeCw5:         "RANK_TYPE_CW5",
	RankTypeCw4:         "RANK_TYPE_CW4",
	RankTypeCw3:         "RANK_TYPE_CW3",
	RankTypeCw2:         "RANK_TYPE_CW2",
	RankTypeWo1:         "RANK_TYPE_WO1",
	RankTypeAr:          "RANK_TYPE_AR",
}

// String returns the wire name, or the bare decimal number for values the
// catalog has no name for — mirroring the generated proto String() so the
// derived RankShort stays identical.
func (rt RankType) String() string {
	if name, ok := rankTypeNames[rt]; ok {
		return name
	}
	return decimalString(int64(rt))
}

// RankShort is the short rank name the datastore stamps onto profiles: the
// enum name with the RANK_TYPE_ prefix stripped (e.g. "COL"). For uncataloged
// ids it is the bare decimal (the prefix strip is a no-op on a number).
func (rt RankType) RankShort() string {
	return trimRankPrefix(rt.String())
}

func decimalString(v int64) string {
	return strconv.FormatInt(v, 10)
}

func trimRankPrefix(name string) string {
	return strings.TrimPrefix(name, "RANK_TYPE_")
}
