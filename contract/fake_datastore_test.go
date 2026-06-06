package contract

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/proto"
	"github.com/7cav/api/xenforo"
	"gorm.io/gorm"
)

// errOutage is the injected failure used to record the Internal-error (HTTP
// 500) goldens. The message leaks into the response body via the handlers'
// status.Errorf wrapping — that leak is frozen behavior.
var errOutage = errors.New("simulated datastore outage")

// recordingDatastore is the seeded, fully deterministic Datastore the corpus
// is recorded against. It follows the fake-datastore pattern from
// servers/grpc/fake_datastore_test.go but carries fixed seed data instead of
// per-test function fields, because the corpus needs one stable world:
//
//   - Profiles are keyed by MILPAC RELATION ID, not forum user id, mirroring
//     datastores.Mysql.FindProfilesById (gorm First(&profile, id) keys on the
//     milpacs.Profile primary key = relation_id). The seed makes the two
//     diverge — relation 1 ↔ user 3 — so the by-id golden proves which wins.
//   - Cursor formats mirror datastores/tickets.go exactly: base64url("ts:id")
//     for tickets, base64url("position") for messages, same default/clamp
//     rules for per_page (0 → 50, >100 → 100).
//   - Position search returns an empty (non-nil) LiteRoster for any query
//     that isn't an exact seeded title — mirroring the frozen #137 behavior
//     where plausible queries yield {"profiles":{}}.
//
// The keycloak lookup panics: the route is deliberately not recorded (#116)
// and the battery must never reach it.
type recordingDatastore struct{}

// --- API keys ----------------------------------------------------------

func (recordingDatastore) ValidateApiKey(rawKey string) (*datastores.ApiKeyResult, error) {
	switch rawKey {
	case "cav7_readkey":
		return &datastores.ApiKeyResult{KeyId: 101, UserId: 3, Scopes: scopeSet("read")}, nil
	case "cav7_ticketskey":
		return &datastores.ApiKeyResult{KeyId: 102, UserId: 8, Scopes: scopeSet("read:tickets")}, nil
	case "cav7_noscopekey":
		return &datastores.ApiKeyResult{KeyId: 103, UserId: 9, Scopes: scopeSet()}, nil
	default:
		return nil, nil // zero rows — generic Unauthorized, leaks nothing
	}
}

func scopeSet(scopes ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, s := range scopes {
		m[s] = struct{}{}
	}
	return m
}

// --- Milpacs profiles ---------------------------------------------------

// seedJarvis is the rich profile: every collection populated, relation id 1
// diverging from user id 3 (matches the live-capture divergence for Jarvis.A).
func seedJarvis() *proto.Profile {
	return &proto.Profile{
		User: &proto.User{UserId: 3, Username: "Jarvis.A"},
		Rank: &proto.Rank{
			RankShort:    "MG",
			RankFull:     "Major General",
			RankImageUrl: "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618",
			RankId:       4,
		},
		RealName:   "Adam Jarvis",
		UniformUrl: "https://7cav.us/data/roster_uniforms/0/1.jpg",
		Roster:     proto.RosterType_ROSTER_TYPE_COMBAT,
		Primary:    &proto.Position{PositionTitle: "Regimental Technical Aide", PositionId: 773},
		Secondaries: []*proto.Position{
			{PositionTitle: "S6 Web Developer", PositionId: 812},
		},
		Records: []*proto.Record{
			{
				RecordDetails: "Promoted to Major General (O-8)",
				RecordType:    proto.RecordType_RECORD_TYPE_PROMOTION,
				RecordDate:    "2020-10-17",
				RecordUid:     46,
			},
			{
				RecordDetails: "Completed 18th Combat Mission (Operation Pride of Charlie, Fall 2020)",
				RecordType:    proto.RecordType_RECORD_TYPE_OPERATION,
				RecordDate:    "2020-09-27",
				RecordUid:     13863,
			},
		},
		Awards: []*proto.Award{
			{
				AwardDetails:  "For technical excellence & dedication <est. 2014>",
				AwardName:     "Commendation Medal",
				AwardDate:     "2021-03-01",
				AwardImageUrl: "https://7cav.us/data/awards/ccm.jpg",
				AwardUid:      901,
			},
		},
		JoinDate:          "2014-02-08",
		PromotionDate:     "2020-10-17",
		KeycloakId:        "3f8e2a10-dead-beef-cafe-0123456789ab",
		DiscordId:         "112233445566778899",
		LastForumPostDate: "2026-05-30",
		Mos:               "11B",
		ConsoleGamertag:   "",
	}
}

// seedDoe is the sparse profile: unset nested messages stay nil (emitted as
// null), collections stay empty (emitted as []), strings stay "" — the
// emit-everything goldens hang off this profile. Relation 2 ↔ user 8.
func seedDoe() *proto.Profile {
	return &proto.Profile{
		User:            &proto.User{UserId: 8, Username: "John.Doe"},
		Rank:            &proto.Rank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "https://7cav.us/data/roster_ranks/0/22.jpg", RankId: 22},
		RealName:        "John Doe",
		UniformUrl:      "https://7cav.us/data/roster_uniforms/0/2.jpg",
		Roster:          proto.RosterType_ROSTER_TYPE_COMBAT,
		Primary:         nil, // → "primary": null
		Secondaries:     []*proto.Position{},
		Records:         []*proto.Record{},
		Awards:          []*proto.Award{},
		JoinDate:        "2026-01-15",
		ConsoleGamertag: "CavGamer77",
	}
}

func seedJarvisLite() *proto.LiteProfile {
	j := seedJarvis()
	return &proto.LiteProfile{
		User:              j.User,
		Rank:              j.Rank,
		RealName:          j.RealName,
		UniformUrl:        j.UniformUrl,
		Roster:            j.Roster,
		Primary:           j.Primary,
		Secondaries:       j.Secondaries,
		JoinDate:          j.JoinDate,
		PromotionDate:     j.PromotionDate,
		KeycloakId:        j.KeycloakId,
		DiscordId:         j.DiscordId,
		AwardDate:         "2021-03-01",
		RecordDate:        "2020-10-17",
		LastForumPostDate: j.LastForumPostDate,
		Mos:               j.Mos,
	}
}

func seedDoeLite() *proto.LiteProfile {
	d := seedDoe()
	return &proto.LiteProfile{
		User:            d.User,
		Rank:            d.Rank,
		RealName:        d.RealName,
		UniformUrl:      d.UniformUrl,
		Roster:          d.Roster,
		Secondaries:     []*proto.Position{},
		JoinDate:        d.JoinDate,
		ConsoleGamertag: d.ConsoleGamertag,
	}
}

func (recordingDatastore) FindProfilesById(userIds ...uint64) ([]*proto.Profile, error) {
	// Path value is the milpac relation key (see type comment). 777 is the
	// injected-outage id for the 500 golden.
	switch userIds[0] {
	case 1:
		return []*proto.Profile{seedJarvis()}, nil
	case 2:
		return []*proto.Profile{seedDoe()}, nil
	case 777:
		return nil, errOutage
	default:
		return nil, gorm.ErrRecordNotFound
	}
}

func (recordingDatastore) FindProfilesByUsername(username string) ([]*proto.Profile, error) {
	switch username {
	case "Jarvis.A":
		return []*proto.Profile{seedJarvis()}, nil
	case "John.Doe":
		return []*proto.Profile{seedDoe()}, nil
	default:
		return nil, gorm.ErrRecordNotFound
	}
}

func (recordingDatastore) FindProfileByDiscordID(discordId string) (*proto.Profile, error) {
	if discordId == "112233445566778899" {
		return seedJarvis(), nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (recordingDatastore) FindProfileByGamertag(gamertag string) (*proto.Profile, error) {
	if gamertag == "CavGamer77" {
		return seedDoe(), nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (recordingDatastore) FindProfileByKeycloakID(string) (*proto.Profile, error) {
	panic("contract: keycloak route is deliberately not recorded (#116)")
}

// --- Rosters ------------------------------------------------------------

func (recordingDatastore) FindRosterByType(t proto.RosterType) (*proto.Roster, error) {
	switch t {
	case proto.RosterType_ROSTER_TYPE_COMBAT:
		return &proto.Roster{Profiles: map[uint64]*proto.Profile{1: seedJarvis(), 2: seedDoe()}}, nil
	case proto.RosterType_ROSTER_TYPE_ARLINGTON:
		return nil, errOutage
	default:
		// Empty-but-present roster: {"profiles":{}}.
		return &proto.Roster{Profiles: map[uint64]*proto.Profile{}}, nil
	}
}

func (recordingDatastore) FindLiteRosterByType(t proto.RosterType) (*proto.LiteRoster, error) {
	switch t {
	case proto.RosterType_ROSTER_TYPE_COMBAT:
		return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{1: seedJarvisLite(), 2: seedDoeLite()}}, nil
	default:
		return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{}}, nil
	}
}

func (recordingDatastore) FindS1UniformsRosterByType(t proto.RosterType) (*proto.S1UniformsRoster, error) {
	if t != proto.RosterType_ROSTER_TYPE_COMBAT {
		return &proto.S1UniformsRoster{Profiles: map[uint64]*proto.S1UniformsProfile{}}, nil
	}
	return &proto.S1UniformsRoster{Profiles: map[uint64]*proto.S1UniformsProfile{
		1: {
			User:                     &proto.User{UserId: 3, Username: "Jarvis.A"},
			Rank:                     &proto.S1UniformsRank{RankShort: "MG", RankFull: "Major General", RankImageUrl: "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618"},
			RealName:                 "Adam Jarvis",
			UniformUrl:               "https://7cav.us/data/roster_uniforms/0/1.jpg",
			UniformDate:              "2025-11-02",
			UniformUpdateTriggerDate: "2025-12-01",
			Roster:                   proto.RosterType_ROSTER_TYPE_COMBAT,
			PrimaryPositionTitle:     "Regimental Technical Aide",
			Secondaries:              []*proto.S1UniformsPosition{{PositionTitle: "S6 Web Developer"}},
			JoinDate:                 "2014-02-08",
			PromotionDate:            "2020-10-17",
			AreaOfResponsibility:     "S6",
		},
		2: {
			User:                 &proto.User{UserId: 8, Username: "John.Doe"},
			Rank:                 &proto.S1UniformsRank{RankShort: "PVT", RankFull: "Private", RankImageUrl: "https://7cav.us/data/roster_ranks/0/22.jpg"},
			RealName:             "John Doe",
			UniformUrl:           "https://7cav.us/data/roster_uniforms/0/2.jpg",
			Roster:               proto.RosterType_ROSTER_TYPE_COMBAT,
			PrimaryPositionTitle: "Rifleman",
			Secondaries:          []*proto.S1UniformsPosition{},
			JoinDate:             "2026-01-15",
		},
	}}, nil
}

func (recordingDatastore) FindProfilesByPosition(positionQuery string) (*proto.LiteRoster, error) {
	if positionQuery == "Regimental Technical Aide" {
		return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{1: seedJarvisLite()}}, nil
	}
	// Frozen #137 behavior: plausible queries come back empty, not 404.
	return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{}}, nil
}

// --- Reference lists ------------------------------------------------------

func (recordingDatastore) FindAllRanks() ([]*proto.RankExpanded, error) {
	return []*proto.RankExpanded{
		{RankShort: "MG", RankFull: "Major General", RankImageUrl: "https://7cav.us/data/roster_ranks/0/4.jpg?1741364618", RankId: 4, RankDisplayOrder: 4},
		{RankShort: "PVT", RankFull: "Private", RankImageUrl: "https://7cav.us/data/roster_ranks/0/22.jpg", RankId: 22, RankDisplayOrder: 22},
	}, nil
}

func (recordingDatastore) FindAllPositionGroups() ([]*proto.PositionGroup, error) {
	return []*proto.PositionGroup{
		{
			GroupId:           9,
			GroupTitle:        "Regimental HQ",
			GroupDisplayOrder: 1,
			Positions: []*proto.PositionExpanded{
				{PositionTitle: "Regimental Technical Aide", PositionId: 773, PositionDisplayOrder: 3, PositionPossibleSecondary: false},
				{PositionTitle: "S6 Web Developer", PositionId: 812, PositionDisplayOrder: 7, PositionPossibleSecondary: true},
			},
		},
	}, nil
}

func (recordingDatastore) FindAwol() ([]*proto.Awol, error) {
	return []*proto.Awol{
		{
			GroupName: "Alpha Company",
			RankName:  "Private",
			Username:  "John.Doe",
			UserId:    8,
			HumanDate: "2026-05-01",
			Timestamp: 1777600000,
			PostId:    445566,
			MilpacId:  2,
		},
	}, nil
}

func (recordingDatastore) GetTableUpdates() ([]xenforo.TableInfo, error) {
	panic("contract: GetTableUpdates is not a public route")
}

// --- Tickets --------------------------------------------------------------

// seedTickets returns the ticket world sorted by last_modified_date DESC,
// ticket_id DESC — the order the real datastore queries in.
func seedTickets() []*proto.Ticket {
	return []*proto.Ticket{
		{
			TicketId:            44,
			TicketRef:           "ZZTOP44Q",
			Title:               "Transfer request: Bravo to Alpha",
			CategoryId:          5,
			CategoryTitle:       "S1 Personnel",
			CategoryAncestorIds: []uint32{1},
			TicketState:         "open",
			StatusId:            2,
			StatusName:          "In Progress",
			PriorityId:          2,
			PriorityName:        "Normal",
			PrefixId:            3,
			PrefixName:          "Transfer",
			DiscussionState:     "visible",
			TicketLocked:        false,
			StarterUserId:       8,
			StarterUsername:     "John.Doe",
			AssignedUserId:      3,
			AssignedUsername:    "Jarvis.A",
			Participants: []*proto.TicketParticipant{
				{UserId: 8, LastReadDate: 1748690000},
				{UserId: 3, LastReadDate: 1748695000},
			},
			StartDate:           1748600000,
			LastMessageDate:     1748699000,
			LastMessageUserId:   3,
			LastMessageUsername: "Jarvis.A",
			LastModifiedDate:    1748700000,
			ReplyCount:          1,
			TotalMessageCount:   2,
			CustomFields:        map[string]string{"department": "S1", "billet": "Rifleman"},
			ForumUrl:            "https://7cav.us/tickets/ZZTOP44Q/",
		},
		{
			TicketId:            43,
			TicketRef:           "QQWW43RR",
			Title:               "TeamSpeak <connection> issues & errors",
			CategoryId:          7,
			CategoryTitle:       "S6 Technical Support",
			CategoryAncestorIds: []uint32{},
			TicketState:         "resolved",
			StatusId:            5,
			StatusName:          "Resolved",
			PriorityId:          1,
			PriorityName:        "Low",
			DiscussionState:     "visible",
			StarterUserId:       3,
			StarterUsername:     "Jarvis.A",
			Participants:        []*proto.TicketParticipant{},
			StartDate:           1748500000,
			LastMessageDate:     1748590000,
			LastMessageUserId:   3,
			LastMessageUsername: "Jarvis.A",
			LastModifiedDate:    1748600000,
			ReplyCount:          0,
			TotalMessageCount:   1,
			CustomFields:        map[string]string{},
			ForumUrl:            "https://7cav.us/tickets/QQWW43RR/",
		},
		{
			TicketId:            42,
			TicketRef:           "MF1UI9HE",
			Title:               "Enlistment — John.Doe",
			CategoryId:          1,
			CategoryTitle:       "Recruiting",
			CategoryAncestorIds: []uint32{},
			TicketState:         "open",
			StatusId:            1,
			StatusName:          "New",
			PriorityId:          3,
			PriorityName:        "High",
			PrefixId:            1,
			PrefixName:          "Enlistment",
			DiscussionState:     "visible",
			StarterUserId:       8,
			StarterUsername:     "John.Doe",
			Participants:        []*proto.TicketParticipant{{UserId: 8, LastReadDate: 1748450000}},
			StartDate:           1748400000,
			LastMessageDate:     1748480000,
			LastMessageUserId:   8,
			LastMessageUsername: "John.Doe",
			LastModifiedDate:    1748500000,
			ReplyCount:          2,
			TotalMessageCount:   3,
			CustomFields:        map[string]string{"recruiter": "Jarvis.A"},
			ForumUrl:            "https://7cav.us/tickets/MF1UI9HE/",
		},
	}
}

// seedMessages42 is the message thread for ticket 42, position ascending.
func seedMessages42() []*proto.Message {
	return []*proto.Message{
		{
			MessageId:    9001,
			TicketId:     42,
			UserId:       8,
			Username:     "John.Doe",
			MessageDate:  1748400000,
			Message:      "[B]Enlistment[/B] request — I'd like to join the 7th Cavalry & start ASAP <o7>",
			MessageState: "visible",
			Position:     0,
			AttachCount:  0,
		},
		{
			MessageId:    9002,
			TicketId:     42,
			UserId:       3,
			Username:     "Jarvis.A",
			MessageDate:  1748450000,
			Message:      "Welcome aboard — processing now.",
			MessageState: "visible",
			Position:     1,
			AttachCount:  1,
			LastEditDate: 1748451000,
			EditCount:    1,
		},
		{
			MessageId:    9003,
			TicketId:     42,
			UserId:       8,
			Username:     "John.Doe",
			MessageDate:  1748480000,
			Message:      "Thank you!",
			MessageState: "visible",
			Position:     2,
		},
	}
}

// Cursor helpers mirroring datastores/tickets.go exactly.
func encodeTicketCursor(lastModified, ticketID uint32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", lastModified, ticketID)))
}

func decodeTicketCursor(c string) (uint32, uint32, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: expected ts:id", datastores.ErrInvalidCursor)
	}
	ts, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	id, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	return uint32(ts), uint32(id), nil
}

func encodeMessageCursor(position uint32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(position), 10)))
}

func decodeMessageCursor(c string) (uint32, error) {
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	pos, err := strconv.ParseUint(string(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", datastores.ErrInvalidCursor, err)
	}
	return uint32(pos), nil
}

func (recordingDatastore) ListTickets(_ context.Context, _ datastores.TicketReferenceCache, f *datastores.ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
	perPage := f.PerPage
	if perPage == 0 {
		perPage = 50
	} else if perPage > 100 {
		perPage = 100
	}

	var afterTs, afterId uint32
	hasCursor := f.AfterCursor != ""
	if hasCursor {
		var err error
		afterTs, afterId, err = decodeTicketCursor(f.AfterCursor)
		if err != nil {
			return nil, "", false, err
		}
	}

	var rows []*proto.Ticket
	for _, t := range seedTickets() {
		if hasCursor && !(t.LastModifiedDate < afterTs || (t.LastModifiedDate == afterTs && t.TicketId < afterId)) {
			continue
		}
		if !matchTicket(t, f) {
			continue
		}
		rows = append(rows, t)
	}

	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}
	next := ""
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		next = encodeTicketCursor(last.LastModifiedDate, last.TicketId)
	}
	if rows == nil {
		rows = []*proto.Ticket{}
	}
	return rows, next, hasMore, nil
}

func matchTicket(t *proto.Ticket, f *datastores.ListTicketsFilter) bool {
	if len(f.CategoryIDs) > 0 {
		match := containsU32(f.CategoryIDs, t.CategoryId)
		if !match && !f.ExcludeSubcategories {
			for _, anc := range t.CategoryAncestorIds {
				if containsU32(f.CategoryIDs, anc) {
					match = true
					break
				}
			}
		}
		if !match {
			return false
		}
	}
	if len(f.TicketStates) > 0 && !containsStr(f.TicketStates, t.TicketState) {
		return false
	}
	if len(f.StatusIDs) > 0 && !containsU32(f.StatusIDs, t.StatusId) {
		return false
	}
	if len(f.PrefixIDs) > 0 && !containsU32(f.PrefixIDs, t.PrefixId) {
		return false
	}
	if len(f.AssignedUserIDs) > 0 && !containsU32(f.AssignedUserIDs, t.AssignedUserId) {
		return false
	}
	if len(f.StarterUserIDs) > 0 && !containsU32(f.StarterUserIDs, t.StarterUserId) {
		return false
	}
	if f.ModifiedSince > 0 && t.LastModifiedDate < f.ModifiedSince {
		return false
	}
	return true
}

func containsU32(haystack []uint32, needle uint32) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func containsStr(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func (recordingDatastore) GetTicket(_ context.Context, _ datastores.TicketReferenceCache, ticketID uint32, _ string) (*proto.Ticket, error) {
	for _, t := range seedTickets() {
		if t.TicketId == ticketID {
			return t, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (recordingDatastore) GetTicketByRef(_ context.Context, _ datastores.TicketReferenceCache, ref string, _ string) (*proto.Ticket, error) {
	for _, t := range seedTickets() {
		if t.TicketRef == ref {
			return t, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (recordingDatastore) GetTicketFirstMessages(_ context.Context, ticketID uint32, n int, _ bool) ([]*proto.Message, uint32, error) {
	if ticketID != 42 {
		return []*proto.Message{}, 0, nil
	}
	msgs := seedMessages42()
	total := uint32(len(msgs))
	if n < len(msgs) {
		msgs = msgs[:n]
	}
	return msgs, total, nil
}

func (recordingDatastore) ListTicketMessages(_ context.Context, ticketID uint32, afterCursor string, perPage uint32, _ bool) ([]*proto.Message, string, bool, error) {
	from, err := decodeMessageCursor(afterCursor)
	if err != nil {
		return nil, "", false, err
	}
	if perPage == 0 {
		perPage = 50
	} else if perPage > 100 {
		perPage = 100
	}
	var rows []*proto.Message
	if ticketID == 42 {
		for _, m := range seedMessages42() {
			if m.Position >= from {
				rows = append(rows, m)
			}
		}
	}
	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}
	next := ""
	if hasMore && len(rows) > 0 {
		next = encodeMessageCursor(rows[len(rows)-1].Position + 1)
	}
	if rows == nil {
		rows = []*proto.Message{}
	}
	return rows, next, hasMore, nil
}

func (recordingDatastore) ListCategories(_ context.Context, _ datastores.TicketReferenceCache) ([]*proto.Category, error) {
	return []*proto.Category{
		{CategoryId: 1, Title: "Recruiting", Description: "Enlistment & recruiting questions", ParentCategoryId: 0, Depth: 0, DisplayOrder: 10, TicketCount: 120},
		{CategoryId: 5, Title: "S1 Personnel", Description: "", ParentCategoryId: 1, Depth: 1, DisplayOrder: 20, TicketCount: 34},
		{CategoryId: 7, Title: "S6 Technical Support", Description: "Teamspeak, forum & game-server help", ParentCategoryId: 0, Depth: 0, DisplayOrder: 30, TicketCount: 78},
	}, nil
}
