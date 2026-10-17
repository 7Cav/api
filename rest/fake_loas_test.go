package rest_test

// FindLoas on fakeDatastore copies the recording seed in
// contract/fake_datastore_test.go for GET /api/v1/loas. The two seeds must
// agree, or golden replay against the new stack proves nothing.

import (
	"context"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

func (f *fakeDatastore) FindLoas(ctx context.Context, q datastores.LoaQuery) ([]*types.Loa, error) {
	if f.findLoas != nil {
		return f.findLoas(ctx, q)
	}
	return []*types.Loa{
		{
			ThreadId:  105512,
			ForumUrl:  "https://7cav.us/threads/105512/",
			PostDate:  1791568800,
			Forum:     types.LoaForum1_7,
			Subject:   types.LoaSubject{UserId: 3, Username: "Jarvis.A", MilpacId: 1},
			StartDate: "2026-10-10",
			EndDate:   "2026-10-23",
			State:     types.LoaStateOpen,
		},
		{
			ThreadId:  105530,
			ForumUrl:  "https://7cav.us/threads/105530/",
			PostDate:  1791655200,
			Forum:     types.LoaForumACD,
			Subject:   types.LoaSubject{UserId: 8, Username: "John.Doe", MilpacId: 2},
			StartDate: "2026-10-24",
			EndDate:   "2026-11-06",
			State:     types.LoaStateUpcoming,
		},
	}, nil
}
