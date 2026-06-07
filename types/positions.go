package types

// PositionExpanded is one org-chart slot in the position-group reference
// tree — the catalog variant that carries the display order and the
// secondary-capability flag (the plain Position embedded in profiles omits
// both). Served nested under PositionGroup by
// GET /api/v1/milpacs/position/groups.
type PositionExpanded struct {
	PositionTitle             string `json:"positionTitle"`
	PositionId                uint64 `json:"positionId,string"`
	PositionDisplayOrder      uint32 `json:"positionDisplayOrder"`
	PositionPossibleSecondary bool   `json:"positionPossibleSecondary"`
}

// PositionGroup is one node of the position reference tree: a titled,
// display-ordered group holding its positions.
type PositionGroup struct {
	GroupId           uint64                  `json:"groupId,string"`
	GroupTitle        string                  `json:"groupTitle"`
	GroupDisplayOrder uint32                  `json:"groupDisplayOrder"`
	Positions         List[*PositionExpanded] `json:"positions"`
}

// PositionGroupsResponse is the GET /api/v1/milpacs/position/groups envelope.
// Groups is a List, so even the zero value serializes as [] on the wire,
// never null.
type PositionGroupsResponse struct {
	Groups List[*PositionGroup] `json:"groups"`
}
