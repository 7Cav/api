package datastores_test

import (
	"sort"
	"testing"
)

// FindForumGroups returns every xf_user_group row as a {groupId, groupName}
// pair, ordered by groupId ascending and unfiltered (issue #203, ADR 0007).
// The fixture seeds five groups out of insert order (testdb/fixtures.sql), so
// this also proves the ORDER BY rather than relying on insert order.
func TestFindForumGroups_WholeDirectoryOrderedByGroupId(t *testing.T) {
	ds := openHarnessDatastore(t)

	groups, err := ds.FindForumGroups()
	if err != nil {
		t.Fatalf("FindForumGroups: %v", err)
	}

	// Every seeded row, none filtered out.
	want := map[uint32]string{
		1:  "Unregistered / Unconfirmed",
		2:  "Registered",
		3:  "Administrative",
		4:  "Moderating",
		10: "Rank - Major General",
	}
	if len(groups) != len(want) {
		t.Fatalf("forum group directory: want %d groups, got %d", len(want), len(groups))
	}

	ids := make([]uint32, len(groups))
	for i, g := range groups {
		if g == nil {
			t.Fatalf("group %d is nil", i)
		}
		ids[i] = g.GroupId
		if title, ok := want[g.GroupId]; !ok {
			t.Errorf("unexpected groupId %d in directory", g.GroupId)
		} else if g.GroupName != title {
			t.Errorf("groupId %d: want name %q, got %q", g.GroupId, title, g.GroupName)
		}
	}

	if !sort.SliceIsSorted(ids, func(i, j int) bool { return ids[i] < ids[j] }) {
		t.Errorf("forum groups must be ordered by groupId ascending, got %v", ids)
	}
}
