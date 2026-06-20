package rest_test

// The positions/AWOL half of fakeDatastore (#128): a copy of the recording
// seed (contract/fake_datastore_test.go) for the position-groups, position-
// search and AWOL routes — the two seeds MUST agree or golden replay against
// the new stack proves nothing. The recording seed lives in package
// contract's test files, so it cannot be imported; this file mirrors it
// verbatim (one position group, the Jarvis lite profile keyed by relation 1,
// one AWOL row).

import (
	"github.com/7cav/api/types"
)

func (f *fakeDatastore) FindAllPositionGroups() ([]*types.PositionGroup, error) {
	if f.findAllPositionGroups != nil {
		return f.findAllPositionGroups()
	}
	return []*types.PositionGroup{
		{
			GroupId:           9,
			GroupTitle:        "Regimental HQ",
			GroupDisplayOrder: 1,
			Positions: []*types.PositionExpanded{
				{PositionTitle: "Regimental Technical Aide", PositionId: 773, PositionDisplayOrder: 3, PositionPossibleSecondary: false},
				{PositionTitle: "S6 Web Developer", PositionId: 812, PositionDisplayOrder: 7, PositionPossibleSecondary: true},
			},
		},
	}, nil
}

func (f *fakeDatastore) FindProfilesByPosition(positionQuery string) (*types.LiteRoster, error) {
	if f.findProfilesByPosition != nil {
		return f.findProfilesByPosition(positionQuery)
	}
	if positionQuery == "Regimental Technical Aide" {
		return &types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{1: seedJarvisLite()}}, nil
	}
	// Frozen #137 behavior: plausible queries come back empty, not 404.
	return &types.LiteRoster{Profiles: map[uint64]*types.LiteProfile{}}, nil
}

func (f *fakeDatastore) FindAwol() ([]*types.Awol, error) {
	if f.findAwol != nil {
		return f.findAwol()
	}
	return []*types.Awol{
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

// FindForumGroups mirrors the recording seed (contract/fake_datastore_test.go):
// three forum groups, groupId ascending — the new-stack golden replay against
// the forum/groups golden depends on the two seeds agreeing.
func (f *fakeDatastore) FindForumGroups() ([]*types.ForumGroup, error) {
	if f.findForumGroups != nil {
		return f.findForumGroups()
	}
	return []*types.ForumGroup{
		{GroupId: 2, GroupName: "Registered"},
		{GroupId: 3, GroupName: "Administrative"},
		{GroupId: 10, GroupName: "Rank - Major General"},
	}, nil
}
