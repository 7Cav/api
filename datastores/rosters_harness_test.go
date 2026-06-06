package datastores_test

import (
	"regexp"
	"testing"
	"time"

	"github.com/7cav/api/proto"
)

// dateTime mirrors the lite/uniform timestamp convention: unix rendered
// as "2006-01-02 15:04:05". Computed via time.Unix so assertions hold
// in any test-runner timezone.
func dateTime(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02 15:04:05")
}

// The full roster keys complete Profile shapes by milpac relation id.
// Combat roster = every fixture member with roster_id 1, nobody else.
func TestFindRosterByType_CombatRosterKeyedByRelationId(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindRosterByType(proto.RosterType_ROSTER_TYPE_COMBAT)
	if err != nil {
		t.Fatalf("FindRosterByType(COMBAT): %v", err)
	}
	if len(roster.Profiles) != 4 {
		t.Fatalf("combat roster: want 4 members, got %d", len(roster.Profiles))
	}
	for _, relation := range []uint64{1, 105, 205, 310} {
		if roster.Profiles[relation] == nil {
			t.Errorf("combat roster must contain relation %d", relation)
		}
	}

	// Full shape: the relational payload comes along.
	member, ok := roster.Profiles[205]
	if !ok {
		t.Fatalf("combat roster must contain relation 205, got %v", profileKeys(roster.Profiles))
	}
	if member.User.UserId != 150 {
		t.Errorf("relation 205 maps to forum user 150, got %d", member.User.UserId)
	}
	if len(member.Records) != 2 || len(member.Awards) != 1 {
		t.Errorf("relation 205: want 2 records and 1 award, got %d/%d", len(member.Records), len(member.Awards))
	}

	// Roster membership separates the shapes: reserve and past members.
	reserve, err := ds.FindRosterByType(proto.RosterType_ROSTER_TYPE_RESERVE)
	if err != nil {
		t.Fatalf("FindRosterByType(RESERVE): %v", err)
	}
	if len(reserve.Profiles) != 2 || reserve.Profiles[320] == nil || reserve.Profiles[340] == nil {
		t.Errorf("reserve roster: want exactly relations 320 and 340, got %v", profileKeys(reserve.Profiles))
	}

	past, err := ds.FindRosterByType(proto.RosterType_ROSTER_TYPE_PAST_MEMBERS)
	if err != nil {
		t.Fatalf("FindRosterByType(PAST_MEMBERS): %v", err)
	}
	if len(past.Profiles) != 2 || past.Profiles[330] == nil || past.Profiles[350] == nil {
		t.Errorf("past-members roster: want exactly relations 330 and 350, got %v", profileKeys(past.Profiles))
	}
}

// A roster with no members is an empty roster, not an error.
//
// roster_id=3 (ELOA) deliberately has NO fixture members — its emptiness
// is load-bearing for this test (breadcrumb in testdb/fixtures.sql next
// to the roster-member rows). Seed an ELOA member and this test loses
// its subject.
func TestFindRosterByType_EmptyRosterIsNotAnError(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindRosterByType(proto.RosterType_ROSTER_TYPE_ELOA)
	if err != nil {
		t.Fatalf("FindRosterByType(ELOA): %v", err)
	}
	if roster == nil || len(roster.Profiles) != 0 {
		t.Errorf("empty roster: want roster with zero profiles, got %v", roster)
	}
}

// The lite roster serves the slim row shape: identity, rank, positions,
// custom fields and the activity dates — including the last forum post
// resolved through the hot aggregation.
func TestFindLiteRosterByType_LiteShapeWithActivityDates(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindLiteRosterByType(proto.RosterType_ROSTER_TYPE_COMBAT)
	if err != nil {
		t.Fatalf("FindLiteRosterByType(COMBAT): %v", err)
	}
	if len(roster.Profiles) != 4 {
		t.Fatalf("combat lite roster: want 4 members, got %d", len(roster.Profiles))
	}

	member, ok := roster.Profiles[205]
	if !ok {
		t.Fatalf("combat lite roster must contain relation 205, got %v", profileKeys(roster.Profiles))
	}
	if member.User.UserId != 150 || member.User.Username != "Trooper.C" {
		t.Errorf("relation 205 = user %d %q, want 150 Trooper.C", member.User.UserId, member.User.Username)
	}
	if member.RealName != "Charlie Brown" || member.Mos != "68W" {
		t.Errorf("custom fields = %q/%q, want Charlie Brown/68W", member.RealName, member.Mos)
	}
	if member.Rank.RankFull != "Specialist" {
		t.Errorf("RankFull = %q, want Specialist", member.Rank.RankFull)
	}
	if member.AwardDate != dateTime(1711000000) {
		t.Errorf("AwardDate = %q, want %q (latest award)", member.AwardDate, dateTime(1711000000))
	}
	if member.RecordDate != dateTime(1710000000) {
		t.Errorf("RecordDate = %q, want %q (latest service record)", member.RecordDate, dateTime(1710000000))
	}
	if member.LastForumPostDate == "" {
		t.Error("expected LastForumPostDate for a member with forum posts")
	}
}

// A member who never posted gets an EMPTY last-post date (the
// aggregation left-joins), not a missing roster entry.
func TestFindLiteRosterByType_NeverPostedMemberHasEmptyLastPostDate(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindLiteRosterByType(proto.RosterType_ROSTER_TYPE_PAST_MEMBERS)
	if err != nil {
		t.Fatalf("FindLiteRosterByType(PAST_MEMBERS): %v", err)
	}
	member := roster.Profiles[330]
	if member == nil {
		t.Fatalf("past-members lite roster must contain relation 330, got %v", roster.Profiles)
	}
	if member.LastForumPostDate != "" {
		t.Errorf("never-posted member: LastForumPostDate = %q, want empty", member.LastForumPostDate)
	}
}

// The S1 uniforms roster serves the uniforms-tool shape: uniform dates,
// the primary position title flattened to a string, and the position
// group mapped to an area of responsibility.
func TestFindS1UniformsRosterByType_UniformsShape(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindS1UniformsRosterByType(proto.RosterType_ROSTER_TYPE_COMBAT)
	if err != nil {
		t.Fatalf("FindS1UniformsRosterByType(COMBAT): %v", err)
	}
	if len(roster.Profiles) != 4 {
		t.Fatalf("combat uniforms roster: want 4 members, got %d", len(roster.Profiles))
	}

	// Relation 1: has a uniform date, awards and records — the update
	// trigger is the latest of awards and uniform-relevant records. The
	// fixture's non-relevant OPERATION record (1687000000) is deliberately
	// NEWER than the latest award (1686000000): if the relevant-record-type
	// filter were dropped, the trigger would move to 1687000000 and this
	// assertion would fail.
	member, ok := roster.Profiles[1]
	if !ok {
		t.Fatalf("combat uniforms roster must contain relation 1, got %v", profileKeys(roster.Profiles))
	}
	if member.User.Username != "Trooper.A" {
		t.Errorf("relation 1 = %q, want Trooper.A", member.User.Username)
	}
	if member.PrimaryPositionTitle != "Squad Leader" {
		t.Errorf("PrimaryPositionTitle = %q, want Squad Leader", member.PrimaryPositionTitle)
	}
	if member.UniformDate != dateTime(1700000000) {
		t.Errorf("UniformDate = %q, want %q", member.UniformDate, dateTime(1700000000))
	}
	if member.UniformUpdateTriggerDate != dateTime(1686000000) {
		t.Errorf("UniformUpdateTriggerDate = %q, want %q (latest award; the NEWER non-relevant operation record must be filtered out)",
			member.UniformUpdateTriggerDate, dateTime(1686000000))
	}
	if member.AreaOfResponsibility != "Alpha Company" {
		t.Errorf("AreaOfResponsibility = %q, want the position group title", member.AreaOfResponsibility)
	}
	if len(member.Secondaries) != 1 || member.Secondaries[0].PositionTitle != "Military Police" {
		t.Errorf("Secondaries = %v, want [Military Police]", member.Secondaries)
	}

	// Relation 310: no uniform yet — empty dates; recruit group maps to RTC.
	recruit, ok := roster.Profiles[310]
	if !ok {
		t.Fatalf("combat uniforms roster must contain relation 310, got %v", profileKeys(roster.Profiles))
	}
	if recruit.UniformDate != "" {
		t.Errorf("member without uniform: UniformDate = %q, want empty", recruit.UniformDate)
	}
	if recruit.AreaOfResponsibility != "RTC" {
		t.Errorf("New Recruits group maps to RTC, got %q", recruit.AreaOfResponsibility)
	}

	// Reserve roster: the ELOA position group maps to ELOA.
	reserve, err := ds.FindS1UniformsRosterByType(proto.RosterType_ROSTER_TYPE_RESERVE)
	if err != nil {
		t.Fatalf("FindS1UniformsRosterByType(RESERVE): %v", err)
	}
	reservist, ok := reserve.Profiles[320]
	if !ok {
		t.Fatalf("reserve uniforms roster must contain relation 320, got %v", profileKeys(reserve.Profiles))
	}
	if reservist.AreaOfResponsibility != "ELOA" {
		t.Errorf("Extended Leave Of Absence group maps to ELOA, got %q", reservist.AreaOfResponsibility)
	}
}

// Position search matches position titles as substrings against both
// primary positions and secondary-capable positions, returning the lite
// shape. Roster membership is NOT filtered: past members match too.
func TestFindProfilesByPosition_MatchesPrimaryHolders(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindProfilesByPosition("Rifleman")
	if err != nil {
		t.Fatalf("FindProfilesByPosition(Rifleman): %v", err)
	}
	got := profileKeys(roster.Profiles)
	for _, relation := range []uint64{105, 205, 330} {
		if roster.Profiles[relation] == nil {
			t.Errorf("Rifleman search must include relation %d (primary holder), got %v", relation, got)
		}
	}
	if len(roster.Profiles) != 3 {
		t.Errorf("Rifleman search: want exactly 3 members, got %v", got)
	}
}

// A secondary-capable position matches members holding it as a
// SECONDARY position.
func TestFindProfilesByPosition_MatchesSecondaryHolders(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindProfilesByPosition("Military Police")
	if err != nil {
		t.Fatalf("FindProfilesByPosition(Military Police): %v", err)
	}
	if len(roster.Profiles) != 1 || roster.Profiles[1] == nil {
		t.Errorf("Military Police search: want exactly relation 1 (secondary holder), got %v", profileKeys(roster.Profiles))
	}
}

// A plausible query matching nothing returns an EMPTY result with 200
// semantics — empty profiles map, no error, never nil. Frozen as-is by
// the golden corpus pending the #137 verdict.
func TestFindProfilesByPosition_NoMatchIsEmptyResultNotError(t *testing.T) {
	ds := openHarnessDatastore(t)

	roster, err := ds.FindProfilesByPosition("Platoon Leader")
	if err != nil {
		t.Fatalf("FindProfilesByPosition(no match): %v", err)
	}
	if roster == nil || roster.Profiles == nil {
		t.Fatal("empty search result must still carry a non-nil profiles map ({\"profiles\":{}} on the wire)")
	}
	if len(roster.Profiles) != 0 {
		t.Errorf("want empty profiles map, got %v", profileKeys(roster.Profiles))
	}
}

// LIKE metacharacters in the query are escaped, not interpreted: "%"
// must not wildcard-match every position, and "_" must not match every
// single character. Either regression would return every member whose
// primary position title is non-empty (i.e. all of them).
func TestFindProfilesByPosition_EscapesLikeMetacharacters(t *testing.T) {
	ds := openHarnessDatastore(t)

	for _, meta := range []string{"%", "_"} {
		t.Run(meta, func(t *testing.T) {
			roster, err := ds.FindProfilesByPosition(meta)
			if err != nil {
				t.Fatalf("FindProfilesByPosition(%q): %v", meta, err)
			}
			if len(roster.Profiles) != 0 {
				t.Errorf("%q must match literally (no fixture title contains it), got %v", meta, profileKeys(roster.Profiles))
			}
		})
	}
}

// The AWOL report lists combat and reserve members whose last forum
// post is older than seven days. Recent posters and members who never
// posted at all are both excluded; past members are out of scope.
func TestFindAwol_FlagsStalePostersOnActiveRosters(t *testing.T) {
	ds := openHarnessDatastore(t)

	awols, err := ds.FindAwol()
	if err != nil {
		t.Fatalf("FindAwol: %v", err)
	}

	// Keyed by forum USER id (Awol.UserId) — relation ids live in
	// Awol.MilpacId and must not be used as exclusion keys here.
	byUser := map[uint64]*proto.Awol{}
	for _, a := range awols {
		byUser[a.UserId] = a
	}
	for _, want := range []uint64{100, 105, 300} {
		if byUser[want] == nil {
			t.Errorf("user %d last posted years ago and must be AWOL", want)
		}
	}
	if len(awols) != 3 {
		t.Errorf("want exactly 3 AWOL members, got %d (%v)", len(awols), byUser)
	}
	for user, reason := range map[uint64]string{
		150: "posted within the last day",
		205: "posted within the last day",
		302: "reservist who never posted (no aggregation row — the LEFT JOIN NULL must not be flagged)",
		303: "past member (roster 6) with stale posts — only the roster filter excludes them",
		301: "past member who also never posted — excluded on either count",
	} {
		if byUser[user] != nil {
			t.Errorf("user %d must not be AWOL: %s", user, reason)
		}
	}

	// Full entry shape for the reservist.
	entry := byUser[300]
	if entry == nil {
		t.Fatal("expected AWOL entry for user 300")
	}
	if entry.Username != "Reservist.E" {
		t.Errorf("Username = %q, want Reservist.E", entry.Username)
	}
	if entry.RankName != "Sergeant" {
		t.Errorf("RankName = %q, want Sergeant", entry.RankName)
	}
	if entry.GroupName != "Extended Leave Of Absence" {
		t.Errorf("GroupName = %q, want the position group title", entry.GroupName)
	}
	if entry.MilpacId != 320 {
		t.Errorf("MilpacId = %d, want relation 320", entry.MilpacId)
	}
	if entry.Timestamp != 1600000000 || entry.PostId != 20004 {
		t.Errorf("last post = ts %d id %d, want 1600000000/20004", entry.Timestamp, entry.PostId)
	}
	if ok, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, entry.HumanDate); !ok {
		t.Errorf("HumanDate = %q, want yyyy-mm-dd", entry.HumanDate)
	}
}

// The rank catalog comes back complete, in display order, with the
// enum-derived short name and the catalog title.
func TestFindAllRanks_OrderedCatalog(t *testing.T) {
	ds := openHarnessDatastore(t)

	ranks, err := ds.FindAllRanks()
	if err != nil {
		t.Fatalf("FindAllRanks: %v", err)
	}
	if len(ranks) != 4 {
		t.Fatalf("want 4 seeded ranks, got %d", len(ranks))
	}

	wantOrder := []uint64{5, 15, 24, 27}
	for i, want := range wantOrder {
		if ranks[i].RankId != want {
			t.Errorf("ranks[%d].RankId = %d, want %d (display_order ascending)", i, ranks[i].RankId, want)
		}
	}
	if ranks[0].RankShort != "BG" || ranks[0].RankFull != "Sergeant Major" {
		t.Errorf("rank 5 = %q/%q, want enum short BG with catalog title Sergeant Major", ranks[0].RankShort, ranks[0].RankFull)
	}
	if ranks[1].RankShort != "MSG" {
		t.Errorf("rank 15 short = %q, want MSG", ranks[1].RankShort)
	}
	if ranks[0].RankImageUrl != "https://7cav.us/data/roster_ranks/0/5.jpg?5" {
		t.Errorf("RankImageUrl = %q", ranks[0].RankImageUrl)
	}
	if ranks[0].RankDisplayOrder != 50 {
		t.Errorf("RankDisplayOrder = %d, want 50", ranks[0].RankDisplayOrder)
	}
}

// Position groups nest their positions, both levels in display order,
// with '----' divider rows filtered out of the listing.
func TestFindAllPositionGroups_NestedAndOrdered(t *testing.T) {
	ds := openHarnessDatastore(t)

	groups, err := ds.FindAllPositionGroups()
	if err != nil {
		t.Fatalf("FindAllPositionGroups: %v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("want 4 seeded groups, got %d", len(groups))
	}

	wantTitles := []string{"Regimental HQ", "New Recruits", "Alpha Company", "Extended Leave Of Absence"}
	for i, want := range wantTitles {
		if groups[i].GroupTitle != want {
			t.Errorf("groups[%d] = %q, want %q (display_order ascending)", i, groups[i].GroupTitle, want)
		}
	}

	alpha := groups[2]
	if len(alpha.Positions) != 2 {
		t.Fatalf("Alpha Company: want 2 positions, got %d", len(alpha.Positions))
	}
	if alpha.Positions[0].PositionTitle != "Rifleman" || alpha.Positions[1].PositionTitle != "Squad Leader" {
		t.Errorf("Alpha Company positions = %q,%q, want Rifleman,Squad Leader (display order)",
			alpha.Positions[0].PositionTitle, alpha.Positions[1].PositionTitle)
	}

	hq := groups[0]
	if len(hq.Positions) != 1 || hq.Positions[0].PositionTitle != "Military Police" {
		t.Fatalf("Regimental HQ: want only Military Police (the '----' divider row is filtered out), got %v", hq.Positions)
	}
	if !hq.Positions[0].PositionPossibleSecondary {
		t.Error("Military Police must surface as secondary-capable")
	}
}

// profileKeys lists the relation-id keys of a profiles map (any value
// type), for readable failure messages.
func profileKeys[V any](m map[uint64]V) []uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
