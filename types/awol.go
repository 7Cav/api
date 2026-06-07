package types

// Awol is one member flagged absent without leave: who (username/userId),
// where (groupName), their rank, and when the absence registered — both the
// human-readable date and the epoch timestamp, plus the forum post the flag
// hangs off. 64-bit ids and the timestamp serialize as decimal strings
// (package convention).
type Awol struct {
	GroupName string `json:"groupName"`
	RankName  string `json:"rankName"`
	Username  string `json:"username"`
	UserId    uint64 `json:"userId,string"`
	HumanDate string `json:"humanDate"`
	Timestamp uint64 `json:"timestamp,string"`
	PostId    uint64 `json:"postId,string"`
	MilpacId  uint64 `json:"milpacId,string"`
}

// AwolResponse is the GET /api/v1/milpacs/awol envelope. Awols is a List, so
// even the zero value serializes as [] on the wire, never null.
type AwolResponse struct {
	Awols List[*Awol] `json:"awols"`
}
