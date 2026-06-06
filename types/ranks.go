package types

// RankExpanded is one entry in the rank reference list — the catalog variant
// that carries the display order (the plain Rank embedded in profiles omits
// it). Served by GET /api/v1/milpacs/ranks.
type RankExpanded struct {
	RankShort        string `json:"rankShort"`
	RankFull         string `json:"rankFull"`
	RankImageUrl     string `json:"rankImageUrl"`
	RankId           uint64 `json:"rankId,string"`
	RankDisplayOrder uint32 `json:"rankDisplayOrder"`
}

// RanksResponse is the GET /api/v1/milpacs/ranks envelope. Ranks must be
// allocated even when empty ([] on the wire, never null).
type RanksResponse struct {
	Ranks []*RankExpanded `json:"ranks"`
}
