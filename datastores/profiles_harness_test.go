package datastores_test

import (
	"errors"
	"testing"
	"time"

	"github.com/7cav/api/proto"
	"gorm.io/gorm"
)

// isoDate mirrors the documented record/award date convention: the
// upstream unix timestamp rendered as a yyyy-mm-dd date. Computed via
// time.Unix (not hardcoded) so assertions hold in any test-runner
// timezone.
func isoDate(unix int64) string {
	return time.Unix(unix, 0).Format("2006-01-02")
}

// The by-id lookup resolves its id against the milpac RELATION key, not
// the forum user id — the frozen semantic of the by-id profile route.
// The fixtures cross the two keys deliberately: relation 205 belongs to
// forum user 150, while forum user 205 belongs to relation 310.
func TestFindProfilesById_ResolvesByRelationKeyNotUserId(t *testing.T) {
	ds := openHarnessDatastore(t)

	profiles, err := ds.FindProfilesById(205)
	if err != nil {
		t.Fatalf("FindProfilesById(205): %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one profile, got %d", len(profiles))
	}
	if got := profiles[0].User.UserId; got != 150 {
		t.Errorf("id 205 must resolve via relation key to forum user 150, got %d", got)
	}

	// And the crossing direction: relation 310 is the member whose FORUM
	// user id is 205 — a by-id lookup of 310 must return that member.
	profiles, err = ds.FindProfilesById(310)
	if err != nil {
		t.Fatalf("FindProfilesById(310): %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one profile, got %d", len(profiles))
	}
	if got := profiles[0].User.UserId; got != 205 {
		t.Errorf("id 310 must resolve to the member whose forum user id is 205, got %d", got)
	}
}

// The full Profile shape carries the member's complete milpac: identity,
// rank, positions, custom fields, service records, awards, and the
// connected-account identifiers.
func TestFindProfilesById_ReturnsFullProfileShape(t *testing.T) {
	ds := openHarnessDatastore(t)

	profiles, err := ds.FindProfilesById(205)
	if err != nil {
		t.Fatalf("FindProfilesById(205): %v", err)
	}
	got := profiles[0]

	if got.User.Username != "Trooper.C" {
		t.Errorf("Username = %q, want Trooper.C", got.User.Username)
	}
	if got.Rank.RankId != 24 || got.Rank.RankFull != "Specialist" {
		t.Errorf("Rank = id %d %q, want 24 %q", got.Rank.RankId, got.Rank.RankFull, "Specialist")
	}
	if got.Rank.RankImageUrl != "https://7cav.us/data/roster_ranks/0/24.jpg?24" {
		t.Errorf("RankImageUrl = %q", got.Rank.RankImageUrl)
	}
	if got.UniformUrl != "https://7cav.us/data/roster_uniforms/0/205.jpg" {
		t.Errorf("UniformUrl = %q", got.UniformUrl)
	}
	if got.Roster != proto.RosterType_ROSTER_TYPE_COMBAT {
		t.Errorf("Roster = %v, want COMBAT", got.Roster)
	}
	if got.Primary.PositionId != 10 || got.Primary.PositionTitle != "Rifleman" {
		t.Errorf("Primary = %d %q, want 10 Rifleman", got.Primary.PositionId, got.Primary.PositionTitle)
	}
	if len(got.Secondaries) != 0 {
		t.Errorf("member without secondary positions must have none, got %v", got.Secondaries)
	}

	// Custom fields unmarshal from the JSON blob.
	if got.RealName != "Charlie Brown" {
		t.Errorf("RealName = %q, want Charlie Brown", got.RealName)
	}
	if got.JoinDate != "2020-02-20" || got.PromotionDate != "2024-03-12" {
		t.Errorf("JoinDate/PromotionDate = %q/%q, want 2020-02-20/2024-03-12", got.JoinDate, got.PromotionDate)
	}
	if got.Mos != "68W" {
		t.Errorf("Mos = %q, want 68W", got.Mos)
	}
	if got.ConsoleGamertag != "CharlieZulu" {
		t.Errorf("ConsoleGamertag = %q, want CharlieZulu", got.ConsoleGamertag)
	}

	// Service records: both seeded entries, with type and formatted date.
	if len(got.Records) != 2 {
		t.Fatalf("expected 2 service records, got %d", len(got.Records))
	}
	byUID := map[uint64]*proto.Record{}
	for _, r := range got.Records {
		byUID[r.RecordUid] = r
	}
	promo := byUID[1004]
	if promo == nil {
		t.Fatal("expected service record uid 1004")
	}
	if promo.RecordType != proto.RecordType_RECORD_TYPE_PROMOTION {
		t.Errorf("record 1004 type = %v, want PROMOTION", promo.RecordType)
	}
	if promo.RecordDetails != "Promoted to Specialist" {
		t.Errorf("record 1004 details = %q", promo.RecordDetails)
	}
	if promo.RecordDate != isoDate(1710000000) {
		t.Errorf("record 1004 date = %q, want %q", promo.RecordDate, isoDate(1710000000))
	}

	// Awards: joined to the award catalog for name and image.
	if len(got.Awards) != 1 {
		t.Fatalf("expected 1 award, got %d", len(got.Awards))
	}
	award := got.Awards[0]
	if award.AwardName != "Good Conduct Medal" {
		t.Errorf("AwardName = %q, want Good Conduct Medal", award.AwardName)
	}
	if award.AwardUid != 2003 {
		t.Errorf("AwardUid = %d, want 2003", award.AwardUid)
	}
	if award.AwardDate != isoDate(1711000000) {
		t.Errorf("AwardDate = %q, want %q", award.AwardDate, isoDate(1711000000))
	}
	if award.AwardImageUrl != "https://7cav.us/data/roster_awards/0/1.jpg?1" {
		t.Errorf("AwardImageUrl = %q", award.AwardImageUrl)
	}

	// Connected accounts.
	if got.DiscordId != "222222222222222222" {
		t.Errorf("DiscordId = %q, want 222222222222222222", got.DiscordId)
	}

	// Member posts recently in the fixtures, so the last-post column is set.
	if got.LastForumPostDate == "" {
		t.Error("expected LastForumPostDate for a member with forum posts")
	}
}

// Secondary positions resolve from the comma-separated id list to full
// position entries.
func TestFindProfilesById_ResolvesSecondaryPositions(t *testing.T) {
	ds := openHarnessDatastore(t)

	profiles, err := ds.FindProfilesById(1)
	if err != nil {
		t.Fatalf("FindProfilesById(1): %v", err)
	}
	got := profiles[0]
	if got.Primary.PositionTitle != "Squad Leader" {
		t.Errorf("Primary = %q, want Squad Leader", got.Primary.PositionTitle)
	}
	if len(got.Secondaries) != 1 {
		t.Fatalf("expected 1 secondary position, got %d", len(got.Secondaries))
	}
	if got.Secondaries[0].PositionId != 20 || got.Secondaries[0].PositionTitle != "Military Police" {
		t.Errorf("secondary = %d %q, want 20 Military Police", got.Secondaries[0].PositionId, got.Secondaries[0].PositionTitle)
	}
}

func TestFindProfilesById_UnknownIdIsRecordNotFound(t *testing.T) {
	ds := openHarnessDatastore(t)

	_, err := ds.FindProfilesById(99999)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("want gorm.ErrRecordNotFound (handlers map it to 404), got %v", err)
	}
}

// Username lookup goes through the forum user table and lands on the
// same member as the relation-key lookup.
func TestFindProfilesByUsername(t *testing.T) {
	ds := openHarnessDatastore(t)

	profiles, err := ds.FindProfilesByUsername("Trooper.C")
	if err != nil {
		t.Fatalf("FindProfilesByUsername(Trooper.C): %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one profile, got %d", len(profiles))
	}
	got := profiles[0]
	if got.User.UserId != 150 || got.User.Username != "Trooper.C" {
		t.Errorf("got user %d %q, want 150 Trooper.C", got.User.UserId, got.User.Username)
	}
	if got.RealName != "Charlie Brown" {
		t.Errorf("RealName = %q, want Charlie Brown", got.RealName)
	}

	_, err = ds.FindProfilesByUsername("Nobody.Z")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("unknown username: want gorm.ErrRecordNotFound, got %v", err)
	}
}

// Discord lookup resolves through the forum's connected-account rows.
// User 150's discord id must land on the member at relation 205 — the
// relation-key ≠ user-id member, proving the connected-account joins
// hang off the forum user, not the relation key.
func TestFindProfileByDiscordID(t *testing.T) {
	ds := openHarnessDatastore(t)

	got, err := ds.FindProfileByDiscordID("222222222222222222")
	if err != nil {
		t.Fatalf("FindProfileByDiscordID: %v", err)
	}
	if got.User.UserId != 150 || got.User.Username != "Trooper.C" {
		t.Errorf("got user %d %q, want 150 Trooper.C", got.User.UserId, got.User.Username)
	}
	if got.DiscordId != "222222222222222222" {
		t.Errorf("DiscordId = %q, want the looked-up id", got.DiscordId)
	}

	// A keycloak provider key must never resolve through the discord
	// lookup, even though both rows live in the same table.
	_, err = ds.FindProfileByDiscordID("6f1c0000-aaaa-bbbb-cccc-000000000150")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("keycloak key through discord lookup: want gorm.ErrRecordNotFound, got %v", err)
	}

	_, err = ds.FindProfileByDiscordID("999999999999999999")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("unknown discord id: want gorm.ErrRecordNotFound, got %v", err)
	}
}

// Gamertag lookup resolves through the roster custom-field values.
func TestFindProfileByGamertag(t *testing.T) {
	ds := openHarnessDatastore(t)

	got, err := ds.FindProfileByGamertag("CharlieZulu")
	if err != nil {
		t.Fatalf("FindProfileByGamertag(CharlieZulu): %v", err)
	}
	if got.User.UserId != 150 || got.User.Username != "Trooper.C" {
		t.Errorf("got user %d %q, want 150 Trooper.C", got.User.UserId, got.User.Username)
	}
	if got.ConsoleGamertag != "CharlieZulu" {
		t.Errorf("ConsoleGamertag = %q, want CharlieZulu", got.ConsoleGamertag)
	}

	_, err = ds.FindProfileByGamertag("NoSuchTag")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("unknown gamertag: want gorm.ErrRecordNotFound, got %v", err)
	}
}
