package rest_test

// FindLoas on fakeDatastore copies the recording seed in
// contract/fake_datastore_test.go for GET /api/v1/loas. The two seeds must
// agree, or golden replay against the new stack proves nothing.

import (
	"context"
	"fmt"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// loaSeedToday is the LOA seed's today, the recording seed's fixed clock.
var loaSeedToday = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

func (f *fakeDatastore) FindLoas(ctx context.Context, q datastores.LoaQuery) ([]*types.Loa, error) {
	if f.findLoas != nil {
		return f.findLoas(ctx, q)
	}
	if !q.EndedSince.IsZero() && q.EndedSince.Before(loaSeedToday.AddDate(0, 0, -datastores.LoaMaxEndedSinceDays)) {
		return nil, fmt.Errorf("%w: seed limit", datastores.ErrEndedSinceTooFarBack)
	}
	var loas []*types.Loa
	if !q.EndedSince.IsZero() && !q.EndedSince.After(time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)) {
		loas = append(loas, &types.Loa{
			ThreadId:  105497,
			ForumUrl:  "https://7cav.us/threads/105497/",
			PostDate:  1790272800,
			Forum:     types.LoaForum3_7,
			Subject:   types.LoaSubject{UserId: 8, Username: "John.Doe", MilpacId: 2},
			StartDate: "2026-09-26",
			EndDate:   "2026-10-09",
			State:     types.LoaStateEnded,
			Reason:    "Moving house.",
		})
	}
	return append(loas, []*types.Loa{
		{
			ThreadId:  105512,
			ForumUrl:  "https://7cav.us/threads/105512/",
			PostDate:  1791568800,
			Forum:     types.LoaForum1_7,
			Subject:   types.LoaSubject{UserId: 3, Username: "Jarvis.A", MilpacId: 1},
			StartDate: "2026-10-10",
			EndDate:   "2026-10-23",
			State:     types.LoaStateOpen,
			Reason:    "Family trip.\nBack on the 24th.",
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
			Reason:    "Work travel.",
		},
		{
			ThreadId:     105541,
			ForumUrl:     "https://7cav.us/threads/105541/",
			PostDate:     1791741600,
			Forum:        types.LoaForum2_7,
			Subject:      types.LoaSubject{UserId: 3, Username: "Jarvis.A", MilpacId: 1},
			StartDate:    "2026-10-31",
			EndDate:      "",
			State:        types.LoaStateNeedsReview,
			ReviewReason: types.LoaReviewReasonDatesUnreadable,
		},
	}...), nil
}
