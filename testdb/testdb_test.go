package testdb_test

import (
	"testing"

	"github.com/7cav/api/testdb"
)

// Tracer: Open hands back a connection to a seeded, forum-shaped database.
func TestOpen_YieldsSeededDatabase(t *testing.T) {
	db, _ := testdb.Open(t)

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM xf_user`).Scan(&n); err != nil {
		t.Fatalf("querying xf_user: %v", err)
	}
	if n == 0 {
		t.Fatal("expected seeded xf_user rows, got none")
	}
}

// The by-id profile route resolves its id against the milpac relation
// key, not the forum user id. The fixtures must make the two seams
// distinguishable: a relation_id that exists as some OTHER member's
// user_id. Looking up "205" must yield different members depending on
// which key is used.
func TestFixtures_RelationKeyDiffersFromForumUserId(t *testing.T) {
	db, _ := testdb.Open(t)

	var byRelation, byUser uint64
	if err := db.QueryRow(
		`SELECT user_id FROM xf_nf_rosters_user WHERE relation_id = 205`,
	).Scan(&byRelation); err != nil {
		t.Fatalf("looking up member by relation_id 205: %v", err)
	}
	if err := db.QueryRow(
		`SELECT relation_id FROM xf_nf_rosters_user WHERE user_id = 205`,
	).Scan(&byUser); err != nil {
		t.Fatalf("looking up member by user_id 205: %v", err)
	}

	if byRelation == 205 {
		t.Error("member at relation_id 205 must have a different user_id, got 205")
	}
	if byUser == 205 {
		t.Error("member with user_id 205 must have a different relation_id, got 205")
	}
}

// Members must come with the relational payload the profile queries
// preload: rank, position (with group), service records, awards (with
// catalog row), connected accounts, and roster field values.
func TestFixtures_MemberRelationsResolve(t *testing.T) {
	db, _ := testdb.Open(t)

	var orphans int
	for _, q := range []struct{ label, sql string }{
		{"rank", `SELECT COUNT(*) FROM xf_nf_rosters_user m LEFT JOIN xf_nf_rosters_rank r ON r.rank_id = m.rank_id WHERE r.rank_id IS NULL`},
		{"position", `SELECT COUNT(*) FROM xf_nf_rosters_user m LEFT JOIN xf_nf_rosters_position p ON p.position_id = m.position_id WHERE p.position_id IS NULL`},
		{"position group", `SELECT COUNT(*) FROM xf_nf_rosters_position p LEFT JOIN xf_nf_rosters_position_group g ON g.position_group_id = p.position_group_id WHERE g.position_group_id IS NULL`},
		{"forum user", `SELECT COUNT(*) FROM xf_nf_rosters_user m LEFT JOIN xf_user u ON u.user_id = m.user_id WHERE u.user_id IS NULL`},
		{"award catalog", `SELECT COUNT(*) FROM xf_nf_rosters_user_award ua LEFT JOIN xf_nf_rosters_award a ON a.award_id = ua.award_id WHERE a.award_id IS NULL`},
	} {
		if err := db.QueryRow(q.sql).Scan(&orphans); err != nil {
			t.Fatalf("checking %s linkage: %v", q.label, err)
		}
		if orphans != 0 {
			t.Errorf("%d member rows with dangling %s reference", orphans, q.label)
		}
	}

	for _, q := range []struct{ label, sql string }{
		{"service records", `SELECT COUNT(*) FROM xf_nf_rosters_service_record`},
		{"awards", `SELECT COUNT(*) FROM xf_nf_rosters_user_award`},
		{"connected accounts", `SELECT COUNT(*) FROM xf_user_connected_account`},
		{"field values", `SELECT COUNT(*) FROM xf_nf_rosters_field_value`},
	} {
		var n int
		if err := db.QueryRow(q.sql).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", q.label, err)
		}
		if n == 0 {
			t.Errorf("expected seeded %s, got none", q.label)
		}
	}
}
