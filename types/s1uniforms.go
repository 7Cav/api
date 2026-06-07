package types

// S1UniformsRoster is the S1-uniforms response shape: the requested roster's
// member set as S1UniformsProfile views (the data the S1 uniforms tool
// consumes), keyed by milpac relation id. Same allocation discipline as
// Roster: the profiles map is always allocated, {} on the wire when empty.
type S1UniformsRoster struct {
	Profiles map[uint64]*S1UniformsProfile `json:"profiles"`
}

// S1UniformsProfile is the S1-uniforms milpac view (the domain glossary's
// third profile shape): uniform-audit fields (the uniform dates, area of
// responsibility) over a trimmed identity — rank without its id, positions
// as bare titles, the primary collapsed to primaryPositionTitle.
type S1UniformsProfile struct {
	User                     *User                     `json:"user"`
	Rank                     *S1UniformsRank           `json:"rank"`
	RealName                 string                    `json:"realName"`
	UniformUrl               string                    `json:"uniformUrl"`
	UniformDate              string                    `json:"uniformDate"`
	UniformUpdateTriggerDate string                    `json:"uniformUpdateTriggerDate"`
	Roster                   RosterType                `json:"roster"`
	PrimaryPositionTitle     string                    `json:"primaryPositionTitle"`
	Secondaries              List[*S1UniformsPosition] `json:"secondaries"`
	JoinDate                 string                    `json:"joinDate"`
	PromotionDate            string                    `json:"promotionDate"`
	AreaOfResponsibility     string                    `json:"areaOfResponsibility"`
}

// S1UniformsRank is the rank entry of the S1 uniforms shape — the plain Rank
// minus rankId.
type S1UniformsRank struct {
	RankShort    string `json:"rankShort"`
	RankFull     string `json:"rankFull"`
	RankImageUrl string `json:"rankImageUrl"`
}

// S1UniformsPosition is a secondary position in the S1 uniforms shape —
// title only, no id.
type S1UniformsPosition struct {
	PositionTitle string `json:"positionTitle"`
}
