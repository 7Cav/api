package types

// Profile is the full milpac view: rank, positions, awards, records, and the
// connected-account identifiers. It is the shared profile shape of the API,
// not a single route's response: today the four profile lookup routes (by
// id, username, Discord id, gamertag) serve it as the top-level body, and
// from #127 on it also appears as the roster map's value type.
//
// Deliberately ABSENT: keycloakId. The new types never had the field — the
// corpus transform documents the break (the Keycloak surface dies at
// cutover, #134).
type Profile struct {
	User              *User           `json:"user"`
	Rank              *Rank           `json:"rank"`
	RealName          string          `json:"realName"`
	UniformUrl        string          `json:"uniformUrl"`
	Roster            RosterType      `json:"roster"`
	Primary           *Position       `json:"primary"`
	Secondaries       List[*Position] `json:"secondaries"`
	Records           List[*Record]   `json:"records"`
	Awards            List[*Award]    `json:"awards"`
	JoinDate          string          `json:"joinDate"`
	PromotionDate     string          `json:"promotionDate"`
	DiscordId         string          `json:"discordId"`
	LastForumPostDate string          `json:"lastForumPostDate"`
	Mos               string          `json:"mos"`
	ConsoleGamertag   string          `json:"consoleGamertag"`
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

// LiteProfile is the lite milpac view (the domain glossary's second profile
// shape): the full Profile minus the records and awards collections, carrying
// instead the two scalar summary dates (awardDate = most recent award,
// recordDate = most recent service-record entry of ANY type — all nine
// record types, transfers and ELOAs included, not just promotions; see
// getLatestServiceRecordDate, datastores/mysql.go — computed upstream by the
// datastore, passed through here). Caveat: on the lite-roster query path the
// datastore omits the Records/AwardRecords preloads, so these helpers compute
// over whatever the query loaded.
//
// It is a shared profile shape, not a single route's: the position search
// route serves it as the LiteRoster map's value type, and the lite roster
// route serves the same shape.
//
// Deliberately ABSENT: keycloakId — same documented break as Profile.
type LiteProfile struct {
	User              *User           `json:"user"`
	Rank              *Rank           `json:"rank"`
	RealName          string          `json:"realName"`
	UniformUrl        string          `json:"uniformUrl"`
	Roster            RosterType      `json:"roster"`
	Primary           *Position       `json:"primary"`
	Secondaries       List[*Position] `json:"secondaries"`
	JoinDate          string          `json:"joinDate"`
	PromotionDate     string          `json:"promotionDate"`
	DiscordId         string          `json:"discordId"`
	AwardDate         string          `json:"awardDate"`
	RecordDate        string          `json:"recordDate"`
	LastForumPostDate string          `json:"lastForumPostDate"`
	Mos               string          `json:"mos"`
	ConsoleGamertag   string          `json:"consoleGamertag"`
}

// LiteRoster is the lite-profiles-by-relation-id map envelope, shared by the
// position search response and the lite roster route. encoding/json emits
// uint64 map keys as decimal strings — the protojson map<uint64,...> wire
// form ("1": {...}) — and sorts them (lexically on the stringified keys,
// "10" before "2", not protojson's numeric order — JSON-equal, not
// byte-equal). The map must be ALLOCATED even when empty ({} on the wire,
// never null): a nil map marshals as null, so the allocation discipline
// lives in the handlers (the goldens enforce it — {"profiles":{}} is the
// frozen empty-result form, see #137).
type LiteRoster struct {
	Profiles map[uint64]*LiteProfile `json:"profiles"`
}

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
