package types

// Profile is the full milpac view: rank, positions, awards, records, and the
// connected-account identifiers. Served by the four profile lookup routes
// (by id, username, Discord id, gamertag).
//
// Deliberately ABSENT: keycloakId. The new types never had the field — the
// corpus transform documents the break (the Keycloak surface dies at
// cutover, #134).
type Profile struct {
	User              *User       `json:"user"`
	Rank              *Rank       `json:"rank"`
	RealName          string      `json:"realName"`
	UniformUrl        string      `json:"uniformUrl"`
	Roster            RosterType  `json:"roster"`
	Primary           *Position   `json:"primary"`
	Secondaries       []*Position `json:"secondaries"`
	Records           []*Record   `json:"records"`
	Awards            []*Award    `json:"awards"`
	JoinDate          string      `json:"joinDate"`
	PromotionDate     string      `json:"promotionDate"`
	DiscordId         string      `json:"discordId"`
	LastForumPostDate string      `json:"lastForumPostDate"`
	Mos               string      `json:"mos"`
	ConsoleGamertag   string      `json:"consoleGamertag"`
}

// User is the forum identity embedded in profile shapes. UserId is the FORUM
// user id — distinct from the milpac relation key the by-id route binds.
type User struct {
	UserId   uint64 `json:"userId,string"`
	Username string `json:"username"`
}

// Rank is the pay-grade entry embedded in profiles — the plain variant
// without the display order (the catalog variant is RankExpanded).
type Rank struct {
	RankShort    string `json:"rankShort"`
	RankFull     string `json:"rankFull"`
	RankImageUrl string `json:"rankImageUrl"`
	RankId       uint64 `json:"rankId,string"`
}

// Position is an org-chart slot held by a member (primary or secondary).
type Position struct {
	PositionTitle string `json:"positionTitle"`
	PositionId    uint64 `json:"positionId,string"`
}

// Record is one entry on a member's service history.
type Record struct {
	RecordDetails string     `json:"recordDetails"`
	RecordType    RecordType `json:"recordType"`
	RecordDate    string     `json:"recordDate"`
	RecordUid     uint64     `json:"recordUid,string"`
}

// Award is a decoration entry on a member's milpac.
type Award struct {
	AwardDetails  string `json:"awardDetails"`
	AwardName     string `json:"awardName"`
	AwardDate     string `json:"awardDate"`
	AwardImageUrl string `json:"awardImageUrl"`
	AwardUid      uint64 `json:"awardUid,string"`
}
