package rest_test

// The positions/AWOL half of fakeDatastore (#128): a copy of the recording
// seed (contract/fake_datastore_test.go) for the position-groups, position-
// search and AWOL routes — the two seeds MUST agree or golden replay against
// the new stack proves nothing. The recording seed lives in package
// contract's test files, so it cannot be imported; this file mirrors it
// verbatim (one position group, the Jarvis lite profile keyed by relation 1,
// one AWOL row).

import (
	"github.com/7cav/api/proto"
)

func (f *fakeDatastore) FindAllPositionGroups() ([]*proto.PositionGroup, error) {
	if f.findAllPositionGroups != nil {
		return f.findAllPositionGroups()
	}
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

func (f *fakeDatastore) FindProfilesByPosition(positionQuery string) (*proto.LiteRoster, error) {
	if f.findProfilesByPosition != nil {
		return f.findProfilesByPosition(positionQuery)
	}
	if positionQuery == "Regimental Technical Aide" {
		return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{1: seedJarvisLite()}}, nil
	}
	// Frozen #137 behavior: plausible queries come back empty, not 404.
	return &proto.LiteRoster{Profiles: map[uint64]*proto.LiteProfile{}}, nil
}

func (f *fakeDatastore) FindAwol() ([]*proto.Awol, error) {
	if f.findAwol != nil {
		return f.findAwol()
	}
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
