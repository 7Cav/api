package types

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
