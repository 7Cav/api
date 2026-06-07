package types

// Roster is the full-roster response shape: the requested roster's complete
// member set as full Profile views, keyed by milpac relation id (uint64 —
// decimal string keys on the wire, the encoding/json integer-key form that
// matches protojson's map encoding; note encoding/json sorts the stringified
// keys LEXICALLY ("10" before "2") where protojson ordered them numerically,
// so responses are JSON-equal, not byte-equal, to the old stack). The
// profiles map is always allocated:
// an empty roster serializes as {"profiles":{}}, never null (the allocation
// discipline lives in the handler's mapper; the reserve_empty golden proves
// it).
type Roster struct {
	Profiles map[uint64]*Profile `json:"profiles"`
}

// LiteRoster is the lite-roster response shape: the same member set as
// LiteProfile views (no records/awards arrays), keyed by milpac relation id.
type LiteRoster struct {
	Profiles map[uint64]*LiteProfile `json:"profiles"`
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
