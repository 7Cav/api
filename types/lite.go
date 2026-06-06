package types

// LiteProfile is the lite milpac view: the Profile shape minus the records
// and awards arrays, with the flattened awardDate/recordDate stamps instead.
// It is a shared profile shape, not a single route's: today the position
// search route serves it as the LiteRoster map's value type, and the lite
// roster route (#127) serves the same shape.
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

// LiteRoster is the lite-profiles-by-relation-id map envelope: the position
// search response, and (from #127) the lite roster route's. encoding/json
// emits uint64 map keys as decimal strings — the protojson map<uint64,...>
// wire form ("1": {...}) — and sorts them (lexically on the stringified keys,
// "10" before "2", not protojson's numeric order — JSON-equal, not
// byte-equal). The map must be ALLOCATED even
// when empty ({} on the wire, never null): a nil map marshals as null, so
// the allocation discipline lives in the handlers (the goldens enforce it —
// {"profiles":{}} is the frozen empty-result form, see #137).
type LiteRoster struct {
	Profiles map[uint64]*LiteProfile `json:"profiles"`
}
