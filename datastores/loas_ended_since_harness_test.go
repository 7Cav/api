package datastores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// findLoasEndedSince is findLoas with endedSince set to a UTC date.
func findLoasEndedSince(t *testing.T, ds datastores.Mysql, endedSince time.Time) []*types.Loa {
	t.Helper()
	loas, err := ds.FindLoas(context.Background(), datastores.LoaQuery{Now: loaNow, EndedSince: endedSince})
	if err != nil {
		t.Fatalf("FindLoas: %v", err)
	}
	return loas
}

func utcDay(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// With endedSince, an LOA whose End Date is on or after it comes back as
// ended, even when it started before it. One that ended before it doesn't.
// Open and upcoming LOAs still come back.
func TestFindLoas_EndedSinceAddsLoasEndedOnOrAfterIt(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	august := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	// The form-2 post runs Oct 10 to Oct 23. moved gives it other dates.
	moved := func(start, end string) string {
		return edited(t, edited(t, loaPostForm2Node180, "Oct 10, 2026", start), "Oct 23, 2026", end)
	}
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, postDate: august, message: moved("Aug 25, 2026", "Sep 1, 2026")})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoa1_7, postDate: august, message: moved("Aug 25, 2026", "Aug 31, 2026")})
	insertLoaThread(t, ds, loaThread{id: 7003, node: nodeLoa1_7, postDate: august, message: loaPostForm2Node180})
	insertLoaThread(t, ds, loaThread{id: 7004, node: nodeLoa1_7, postDate: august,
		message: edited(t, loaPostForm2Node180, "Oct 10, 2026", "Oct 18, 2026")})

	loas := findLoasEndedSince(t, ds, utcDay(2026, time.September, 1))
	if loa := loaByThread(loas, 7001); loa == nil {
		t.Error("ended on endedSince: not returned")
	} else if loa.State != types.LoaStateEnded {
		t.Errorf("ended on endedSince: state = %v, want %v", loa.State, types.LoaStateEnded)
	}
	if loa := loaByThread(loas, 7002); loa != nil {
		t.Errorf("ended the day before endedSince: returned as %v, want it left out", loa.State)
	}
	if loaByThread(loas, 7003) == nil {
		t.Error("open LOA: not returned alongside the ended ones")
	}
	if loaByThread(loas, 7004) == nil {
		t.Error("upcoming LOA: not returned alongside the ended ones")
	}
}

// unknownSubjectOpen is the hand-posted sample, open on loaNow, with a
// Username label that names no member, so it needs review.
func unknownSubjectOpen(t *testing.T) string {
	t.Helper()
	return edited(t, handPostedOpen(t), "[B]Username[/B]: Loa.Alpha", "[B]Username[/B]: Loa.Nobody")
}

// With endedSince, a needs-review thread comes back when it was posted on or
// after midnight UTC on that date, though it is older than the default 14
// days. One posted before that doesn't.
func TestFindLoas_EndedSinceAddsNeedsReviewPostedOnOrAfterIt(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID,
		postDate: time.Date(2026, 9, 1, 0, 30, 0, 0, time.UTC), message: unknownSubjectOpen(t)})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoaACD, starter: loaAlpha.userID,
		postDate: time.Date(2026, 8, 31, 23, 30, 0, 0, time.UTC), message: unknownSubjectOpen(t)})

	loas := findLoasEndedSince(t, ds, utcDay(2026, time.September, 1))
	if loa := loaByThread(loas, 7001); loa == nil {
		t.Error("posted on endedSince: not returned")
	} else if loa.State != types.LoaStateNeedsReview {
		t.Errorf("posted on endedSince: state = %v, want %v", loa.State, types.LoaStateNeedsReview)
	}
	if loa := loaByThread(loas, 7002); loa != nil {
		t.Errorf("posted the day before endedSince: returned as %v, want it left out", loa.State)
	}
}

// endedSince only adds to the default window. A needs-review thread posted 8
// days ago still comes back when endedSince is 5 days ago.
func TestFindLoas_EndedSinceKeepsTheDefaultReviewWindow(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID, message: unknownSubjectOpen(t)})

	if loaByThread(findLoasEndedSince(t, ds, utcDay(2026, time.October, 12)), 7001) == nil {
		t.Error("a needs-review thread posted 8 days ago must come back with endedSince 5 days ago")
	}
}

// endedSince may go back 365 days from today's UTC date, and no further.
// The instant is late on Feb 29 locally and already Mar 1 in UTC, so reading
// the local date, or counting one calendar year instead of 365 days, moves
// the limit by a day.
func TestFindLoas_EndedSinceGoesBackAtMost365Days(t *testing.T) {
	ds := openHarnessDatastore(t)
	now := time.Date(2028, 2, 29, 21, 30, 0, 0, time.FixedZone("UTC-4", -4*60*60))
	find := func(endedSince time.Time) error {
		_, err := ds.FindLoas(context.Background(), datastores.LoaQuery{Now: now, EndedSince: endedSince})
		return err
	}

	if err := find(utcDay(2027, time.March, 2)); err != nil {
		t.Errorf("endedSince 365 days back: %v, want no error", err)
	}
	if err := find(utcDay(2027, time.March, 1)); !errors.Is(err, datastores.ErrEndedSinceTooFarBack) {
		t.Errorf("endedSince 366 days back: error %v, want ErrEndedSinceTooFarBack", err)
	}
}

// With endedSince the window starts on it, and the request reads threads
// posted in the year before that. endedSince is Jul 9, 100 days back, and
// the post gives dates that are open now.
func TestFindLoas_EndedSinceReadsThreadsPostedInTheYearBeforeIt(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	endedSince := utcDay(2026, time.July, 9)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, postDate: endedSince.AddDate(0, 0, -300), message: loaPostForm2Node180})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoa1_7, postDate: endedSince.AddDate(0, 0, -400), message: loaPostForm2Node180})

	loas := findLoasEndedSince(t, ds, endedSince)
	if loaByThread(loas, 7001) == nil {
		t.Error("a thread posted 300 days before endedSince must be read")
	}
	if loaByThread(loas, 7002) != nil {
		t.Error("a thread posted 400 days before endedSince must not be read")
	}
}
