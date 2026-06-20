package datastores_test

import (
	"testing"

	"github.com/7cav/api/types"
)

// Secondary-lookup and forum-post-date outages propagate to the route's
// outage path (the contract 500) instead of degrading to a 200 with a
// fabricated blank position or a blanked lastForumPostDate.
//
// Reversed ruling (issues #154/#155, 2026-06-20): a silent hole is worse
// than an honest error — a consumer cannot tell a degraded outage response
// from real data and may act on a fabricated member or a stale-looking
// blank. The 2026-06-06 parity ruling (keep prod's blank-on-200 degrade) is
// superseded. The golden corpus only witnesses happy paths, so these outage
// paths were never contract-pinned; pinning them here is new-stack-only.
//
// FAULT ISOLATION technique (mirrors the column-drop in auth_harness_test.go):
//
//   - #154: DELETE only the secondary position row a member references
//     (relation 1's secondary id 20, "Military Police"), leaving its PRIMARY
//     position row (id 11, "Squad Leader") and xf_nf_rosters_user intact. The
//     roster query and the Primary preload still resolve; only the per-member
//     collectSecondaryPositions First(&position, 20) fails (ErrRecordNotFound).
//     Per the reversed ruling we propagate ALL error classes including
//     not-found — a fabricated blank member is worse than an honest 500.
//   - #155: DROP xf_post so the getLatestForumPostDates aggregation errors
//     while xf_nf_rosters_user still resolves — the primary roster query
//     succeeds, then the post-date join fails and must propagate.

// #154 — the FULL roster route propagates a failed secondary-position lookup.
func TestFindRosterByType_SecondaryPositionLookupFailurePropagates(t *testing.T) {
	ds := openHarnessDatastore(t)

	// Drop ONLY relation 1's secondary position row; its primary (id 11) stays.
	if err := ds.Db.Exec(`DELETE FROM xf_nf_rosters_position WHERE position_id = 20`).Error; err != nil {
		t.Fatalf("deleting the secondary position row to fault only the secondary lookup: %v", err)
	}

	_, err := ds.FindRosterByType(types.RosterTypeCombat)
	if err == nil {
		t.Fatal("a failed secondary-position lookup must propagate (contract 500), not append a blank position on a 200")
	}
}

// #154 — the by-id FULL profile route propagates the same failure.
func TestFindProfilesById_SecondaryPositionLookupFailurePropagates(t *testing.T) {
	ds := openHarnessDatastore(t)

	if err := ds.Db.Exec(`DELETE FROM xf_nf_rosters_position WHERE position_id = 20`).Error; err != nil {
		t.Fatalf("deleting the secondary position row: %v", err)
	}

	// Relation 1 (Trooper.A) is the member with a secondary position.
	_, err := ds.FindProfilesById(1)
	if err == nil {
		t.Fatal("by-id profile with a broken secondary lookup must propagate, not fabricate a blank position")
	}
}

// #154 — the LITE roster route (collectSecondaryPositions via
// generateLiteProtoProfile) propagates too.
func TestFindLiteRosterByType_SecondaryPositionLookupFailurePropagates(t *testing.T) {
	ds := openHarnessDatastore(t)

	if err := ds.Db.Exec(`DELETE FROM xf_nf_rosters_position WHERE position_id = 20`).Error; err != nil {
		t.Fatalf("deleting the secondary position row: %v", err)
	}

	_, err := ds.FindLiteRosterByType(types.RosterTypeCombat)
	if err == nil {
		t.Fatal("lite roster with a broken secondary lookup must propagate, not fabricate a blank position")
	}
}

// #154 — the S1 uniforms route (collectS1UniformsSecondaryPositions)
// propagates its own secondary lookup failure.
func TestFindS1UniformsRosterByType_SecondaryPositionLookupFailurePropagates(t *testing.T) {
	ds := openHarnessDatastore(t)

	if err := ds.Db.Exec(`DELETE FROM xf_nf_rosters_position WHERE position_id = 20`).Error; err != nil {
		t.Fatalf("deleting the secondary position row: %v", err)
	}

	_, err := ds.FindS1UniformsRosterByType(types.RosterTypeCombat)
	if err == nil {
		t.Fatal("S1 uniforms roster with a broken secondary lookup must propagate, not fabricate a blank position")
	}
}

// #154 — first-error-aborts / no-partial-200. A member with MULTIPLE secondary
// ids where one RESOLVES and a later one ERRORS must fail the WHOLE call, not
// return a partial 1-element success. collectSecondaryPositions returns on the
// first failing id with a nil slice; a future "skip the bad one, keep the good
// ones" change would silently reintroduce the degraded-200 (the exact bug class
// #154 fixes), and this test would flip from the asserted nil-result error to a
// partial profile.
//
// Relation 1 normally carries a single secondary (id 20). Within this test's
// own disposable database (each openHarnessDatastore call gets a fresh seed),
// rewrite it to "20,99999": id 20 resolves first, id 99999 has no row and
// fails second. The fixtures are untouched outside this transaction, so the
// existing single-secondary assertions on relation 1 still hold elsewhere.
func TestFindProfilesById_SecondaryLookupFirstErrorAbortsNoPartial(t *testing.T) {
	ds := openHarnessDatastore(t)

	// 20 resolves (Military Police); 99999 is absent — the second id fails.
	if err := ds.Db.Exec(
		`UPDATE xf_nf_rosters_user SET secondary_position_ids = '20,99999' WHERE relation_id = 1`,
	).Error; err != nil {
		t.Fatalf("rewriting relation 1's secondary ids to a resolves-then-fails pair: %v", err)
	}

	profiles, err := ds.FindProfilesById(1)
	if err == nil {
		t.Fatal("a multi-secondary member whose SECOND id fails must error the whole call, not return a partial profile (no degraded 200)")
	}
	if profiles != nil {
		t.Fatalf("the failing call must return a nil result, not a partial slice; got %d profile(s)", len(profiles))
	}
}

// #155 — the FULL roster route propagates a failed forum-post-date
// aggregation (getLatestForumPostDates via processProfiles).
func TestFindRosterByType_ForumPostDateFailurePropagates(t *testing.T) {
	ds := openHarnessDatastore(t)

	// Drop the post table so the aggregation errors; the roster query itself
	// (xf_nf_rosters_user + preloads) still resolves.
	if err := ds.Db.Exec(`DROP TABLE xf_post`).Error; err != nil {
		t.Fatalf("dropping xf_post to fault only the post-date aggregation: %v", err)
	}

	_, err := ds.FindRosterByType(types.RosterTypeCombat)
	if err == nil {
		t.Fatal("a failed forum post-date aggregation must propagate (contract 500), not blank every lastForumPostDate on a 200")
	}
}

// #155 — the LITE roster route propagates the same aggregation failure
// (getLatestForumPostDates via processLiteProfiles).
func TestFindLiteRosterByType_ForumPostDateFailurePropagates(t *testing.T) {
	ds := openHarnessDatastore(t)

	if err := ds.Db.Exec(`DROP TABLE xf_post`).Error; err != nil {
		t.Fatalf("dropping xf_post: %v", err)
	}

	_, err := ds.FindLiteRosterByType(types.RosterTypeCombat)
	if err == nil {
		t.Fatal("lite roster with a failed post-date aggregation must propagate, not blank every lastForumPostDate on a 200")
	}
}
