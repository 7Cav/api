package datastores_test

import (
	"slices"
	"strings"
	"testing"
)

// reasonLines splits a reason into its non-blank lines, each as its words.
// Rows compare reasons this way, so they check the words and the line breaks
// and nothing about spacing.
func reasonLines(reason string) [][]string {
	var lines [][]string
	for _, line := range strings.Split(reason, "\n") {
		if words := strings.Fields(line); len(words) > 0 {
			lines = append(lines, words)
		}
	}
	return lines
}

// Each LOA carries the reason its post gives under the reason label, up to
// the next label. A leader who corrects the reason edits the post, so the
// post holds the correction.
func TestFindLoas_ReasonIsReadUnderTheReasonLabel(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	rows := []struct {
		name   string
		thread loaThread
	}{
		{"hand-posted copy", loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID, message: handPostedOpen(t)}},
		// The reservist form labels its reason "Details". The sample ended
		// on Oct 10, so its End Date moves to keep it open on loaNow.
		{"reservist form", loaThread{id: 7002, node: nodeLoaACD,
			message: edited(t, loaPostForm5Node400Reservist, "Oct 10, 2026", "Oct 17, 2026")}},
	}
	for _, r := range rows {
		insertLoaThread(t, ds, r.thread)
	}

	loas := findLoas(t, ds)
	want := [][]string{{"Family", "trip."}}
	for _, r := range rows {
		loa := loaByThread(loas, r.thread.id)
		if loa == nil {
			t.Errorf("%s: not returned", r.name)
			continue
		}
		if got := reasonLines(loa.Reason); !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("%s: reason = %q, want the words %q", r.name, loa.Reason, want)
		}
	}
}

// The reason is plain text. No BBCode remains, the words keep the order the
// trooper wrote them in, and each line of the post's reason stays a line of
// its own.
func TestFindLoas_ReasonIsPlainTextWithItsLines(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	// The Regimental sample's reason is "Family trip.", a blank line, then
	// "Thanks,". Its first line gets markup the way stored reasons carry it.
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaRegimental,
		message: edited(t, loaPostForm11Node369, "Family trip.\n",
			"[i]Family[/i] [URL='https://example.test/trip/']trip[/URL].\n")})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("thread 7001: not returned")
	}
	want := [][]string{{"Family", "trip."}, {"Thanks,"}}
	if got := reasonLines(loa.Reason); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("reason = %q, want the lines %q", loa.Reason, want)
	}
}

// The parser reads a Start Date label as the Start Date field even without
// bold markup, so the reason ends there instead of taking in the dates.
func TestFindLoas_ReasonStopsAtAnUnboldedStartDateLabel(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID,
		message: edited(t, handPostedOpen(t), "[B]Start Date[/B]\n", "Start Date\n")})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("thread 7001: not returned")
	}
	want := [][]string{{"Family", "trip."}}
	if got := reasonLines(loa.Reason); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("reason = %q, want the words %q", loa.Reason, want)
	}
}

// A post with no reason field gives an empty reason.
func TestFindLoas_PostWithoutReasonFieldGivesEmptyReason(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7,
		message: edited(t, loaPostForm2Node180, "[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\nFamily trip.\r\n\r\n", "")})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("thread 7001: not returned")
	}
	if loa.Reason != "" {
		t.Errorf("reason = %q, want \"\"", loa.Reason)
	}
}
