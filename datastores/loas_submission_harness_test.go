package datastores_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// storedLoaForm is an LOA form as the forum's forms add-on stores it: its
// form id, the forum it posts to, and the question ids of its Start Date,
// End Date and reason questions. The ids are the production forum's, copied
// from its question catalog. They are written out here, not taken from the
// code's own table, so a wrong id in that table turns a row red.
type storedLoaForm struct {
	id                 uint32
	node               uint32
	start, end, reason uint32
}

var (
	loaForm2  = storedLoaForm{id: 2, node: nodeLoa1_7, start: 29, end: 30, reason: 28}
	loaForm4  = storedLoaForm{id: 4, node: nodeLoa2_7, start: 46, end: 47, reason: 45}
	loaForm16 = storedLoaForm{id: 16, node: nodeLoa3_7, start: 364, end: 365, reason: 363}
	loaForm5  = storedLoaForm{id: 5, node: nodeLoaACD, start: 55, end: 56, reason: 140}
	loaForm12 = storedLoaForm{id: 12, node: nodeLoaACD, start: 172, end: 173, reason: 171}
	loaForm11 = storedLoaForm{id: 11, node: nodeLoaRegimental, start: 156, end: 324, reason: 166}
)

// loaSubmission is one filing of an LOA form and the answers it stored. The
// forms write date answers as "Oct 10, 2026" and the reason as BBCode with
// "\n" line breaks. The helper stores no row for an empty answer.
type loaSubmission struct {
	id                 uint32
	form               storedLoaForm
	at                 time.Time
	start, end, reason string
}

// insertLoaSubmission inserts a submission, filed by loaAlpha, and its
// answers.
func insertLoaSubmission(t *testing.T, ds datastores.Mysql, s loaSubmission) {
	t.Helper()
	mustExec(t, ds, `INSERT INTO xf_snog_forms_logs (log_id, form_id, user_id, ip_address, log_date)
		VALUES (?, ?, ?, '', ?)`, s.id, s.form.id, loaAlpha.userID, s.at.Unix())
	for _, a := range []struct {
		question uint32
		answer   string
	}{{s.form.start, s.start}, {s.form.end, s.end}, {s.form.reason, s.reason}} {
		if a.answer == "" {
			continue
		}
		mustExec(t, ds, `INSERT INTO xf_snog_forms_answers (posid, questionid, answer_date, user_id, answer, log_id)
			VALUES (?, ?, ?, ?, ?, ?)`, s.form.id, a.question, s.at.Unix(), loaAlpha.userID, a.answer, s.id)
	}
}

// withoutFields returns post with each field removed. A field is a label and
// its value, exactly as the post writes them.
func withoutFields(t *testing.T, post string, fields ...string) string {
	t.Helper()
	for _, f := range fields {
		post = edited(t, post, f, "")
	}
	return post
}

// When a post gives no readable dates and no reason, the submission that
// created its thread fills them in, and the state follows the submission's
// dates. Each LOA form stores its answers under its own question ids, and the
// ACD forum takes two forms.
func TestFindLoas_SubmissionFillsWhatThePostLacks(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha, loaBravo)
	rows := []struct {
		name string
		form storedLoaForm
		post string
	}{
		{"form 2", loaForm2, withoutFields(t, loaPostForm2Node180,
			"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\nFamily trip.\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \nOct 10, 2026\r\n",
			"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 23, 2026\r\n")},
		{"form 4", loaForm4, withoutFields(t, loaPostForm4Node178,
			"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\nFamily trip.\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \nOct 10, 2026\r\n",
			"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 16, 2026\r\n")},
		{"form 16", loaForm16, withoutFields(t, loaPostForm16Node540,
			"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B] \nFamily trip.\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \nOct 3, 2026\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 16, 2026\r\n\r\n")},
		{"form 5", loaForm5, withoutFields(t, loaPostForm5Node400Reservist,
			"[B][COLOR=rgb(213, 185, 0)]Details[/COLOR][/B]: Family trip.\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \nOct 3, 2026\r\n",
			"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 10, 2026\r\n")},
		{"form 12", loaForm12, withoutFields(t, loaPostForm12Node400FiledForOther,
			"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B]\nFamily trip.\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \nOct 9, 2026\r\n",
			"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 23, 2026\r\n")},
		{"form 11", loaForm11, withoutFields(t, loaPostForm11Node369,
			"[B][COLOR=rgb(213, 185, 0)]Reason:[/COLOR][/B] \nFamily trip.\n\nThanks,\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]Start Date[/COLOR][/B] \nOct 10, 2026\r\n\r\n",
			"[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 24, 2026\r\n")},
	}
	for i, r := range rows {
		// A minute apart, so no thread sits within seconds of another's
		// submission.
		posted := loaPosted.Add(time.Duration(i) * time.Minute)
		insertLoaThread(t, ds, loaThread{id: uint64(7001 + i), node: r.form.node, postDate: posted, message: r.post})
		insertLoaSubmission(t, ds, loaSubmission{id: uint32(1 + i), form: r.form, at: posted,
			start: "Oct 11, 2026", end: "Oct 24, 2026", reason: "[i]Moving[/i] house.\n\nBack on the 24th."})
	}

	loas := findLoas(t, ds)
	wantReason := [][]string{{"Moving", "house."}, {"Back", "on", "the", "24th."}}
	for i, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			loa := loaByThread(loas, uint64(7001+i))
			if loa == nil {
				t.Fatal("not returned")
			}
			if loa.StartDate != "2026-10-11" || loa.EndDate != "2026-10-24" || loa.State != types.LoaStateOpen {
				t.Errorf("startDate %q, endDate %q, state %v; want the submission's 2026-10-11, 2026-10-24 and %v",
					loa.StartDate, loa.EndDate, loa.State, types.LoaStateOpen)
			}
			if got := reasonLines(loa.Reason); !slices.EqualFunc(got, wantReason, slices.Equal) {
				t.Errorf("reason = %q, want the submission's lines %q", loa.Reason, wantReason)
			}
		})
	}
}

// The post stays the record. A leader who extends an LOA or corrects its
// reason edits the post, and the submission keeps the answers as filed, so
// dates and a reason the post gives win over the submission's.
func TestFindLoas_PostDatesAndReasonWinOverTheSubmission(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	// The post runs Oct 10 to Oct 23 with the reason "Family trip.".
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, message: loaPostForm2Node180})
	insertLoaSubmission(t, ds, loaSubmission{id: 1, form: loaForm2, at: loaPosted,
		start: "Oct 10, 2026", end: "Oct 16, 2026", reason: "Famly trp."})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("not returned")
	}
	if loa.EndDate != "2026-10-23" {
		t.Errorf("endDate = %q, want the post's 2026-10-23", loa.EndDate)
	}
	if got, want := reasonLines(loa.Reason), [][]string{{"Family", "trip."}}; !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("reason = %q, want the post's words %q", loa.Reason, want)
	}
}

// postWithoutEndDate is the form-2 sample, which runs Oct 10 to Oct 23, with
// its End Date field removed.
func postWithoutEndDate(t *testing.T) string {
	t.Helper()
	return withoutFields(t, loaPostForm2Node180, "[B][COLOR=rgb(213, 185, 0)]End Date[/COLOR][/B] \nOct 23, 2026\r\n")
}

// The submission fills in only what the post can't supply. A post that gives
// its Start Date but no End Date keeps its own Start Date and takes the End
// Date from the submission.
func TestFindLoas_SubmissionFillsEachDateOnItsOwn(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, message: postWithoutEndDate(t)})
	insertLoaSubmission(t, ds, loaSubmission{id: 1, form: loaForm2, at: loaPosted,
		start: "Oct 9, 2026", end: "Oct 23, 2026"})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("not returned")
	}
	if loa.StartDate != "2026-10-10" || loa.EndDate != "2026-10-23" {
		t.Errorf("startDate %q, endDate %q; want the post's 2026-10-10 and the submission's 2026-10-23",
			loa.StartDate, loa.EndDate)
	}
}

// A thread matches a submission filed up to 5 seconds before or after it was
// posted. A thread with no submission that close, such as a hand-posted one,
// comes back exactly as it would with no submission at all. The four threads
// are posted a minute apart and each is missing its End Date, so the first
// and last sit at the edges of the threads that need a submission, and the
// middle two sit among them.
func TestFindLoas_SubmissionMatchesWithinFiveSeconds(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	rows := []struct {
		name    string
		thread  uint64
		offset  time.Duration // submit time minus the thread's post time
		matches bool
	}{
		{"filed 5 s before the thread", 7001, -5 * time.Second, true},
		{"filed 6 s before the thread", 7002, -6 * time.Second, false},
		{"filed 6 s after the thread", 7003, 6 * time.Second, false},
		{"filed 5 s after the thread", 7004, 5 * time.Second, true},
	}
	posted := func(i int) time.Time { return loaPosted.Add(time.Duration(i) * time.Minute) }
	for i, r := range rows {
		insertLoaThread(t, ds, loaThread{id: r.thread, node: nodeLoa1_7, postDate: posted(i), message: postWithoutEndDate(t)})
	}
	before := findLoas(t, ds)
	for i, r := range rows {
		insertLoaSubmission(t, ds, loaSubmission{id: uint32(1 + i), form: loaForm2, at: posted(i).Add(r.offset),
			start: "Oct 10, 2026", end: "Oct 23, 2026"})
	}

	after := findLoas(t, ds)
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			was, loa := loaByThread(before, r.thread), loaByThread(after, r.thread)
			switch {
			case was == nil || loa == nil:
				t.Fatalf("returned without the submission: %v, with it: %v; want both", was != nil, loa != nil)
			case r.matches && loa.EndDate != "2026-10-23":
				t.Errorf("endDate = %q, want the submission's 2026-10-23", loa.EndDate)
			case !r.matches && *loa != *was:
				t.Errorf("got %+v, want the record with no submission %+v", *loa, *was)
			}
		})
	}
}

// A double submit files the form twice, seconds apart, and posts two
// threads. Each submission falls within 5 seconds of both threads, and each
// thread takes the one closest to its own post time.
func TestFindLoas_DoubleSubmitMatchesTheClosestSubmission(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	second := loaPosted.Add(4 * time.Second)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, postDate: loaPosted, message: postWithoutEndDate(t)})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoa1_7, postDate: second, message: postWithoutEndDate(t)})
	insertLoaSubmission(t, ds, loaSubmission{id: 1, form: loaForm2, at: loaPosted, end: "Oct 22, 2026"})
	insertLoaSubmission(t, ds, loaSubmission{id: 2, form: loaForm2, at: second, end: "Oct 23, 2026"})

	loas := findLoas(t, ds)
	for _, want := range []struct {
		thread  uint64
		endDate string
	}{{7001, "2026-10-22"}, {7002, "2026-10-23"}} {
		loa := loaByThread(loas, want.thread)
		if loa == nil {
			t.Errorf("thread %d: not returned", want.thread)
		} else if loa.EndDate != want.endDate {
			t.Errorf("thread %d: endDate = %q, want %q from the submission filed with it", want.thread, loa.EndDate, want.endDate)
		}
	}
}

// A thread matches only a submission of a form that posts to its forum. A
// 2-7 LOA filed the same second as a 1-7 thread is posted is another
// trooper's LOA.
func TestFindLoas_SubmissionComesFromTheThreadsForumsForms(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, message: postWithoutEndDate(t)})
	insertLoaSubmission(t, ds, loaSubmission{id: 1, form: loaForm4, at: loaPosted, end: "Oct 30, 2026"})
	insertLoaSubmission(t, ds, loaSubmission{id: 2, form: loaForm2, at: loaPosted.Add(3 * time.Second), end: "Oct 23, 2026"})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("not returned")
	}
	if loa.EndDate != "2026-10-23" {
		t.Errorf("endDate = %q, want 2026-10-23 from the 1-7 form's submission", loa.EndDate)
	}
}

// A failed read of the submissions or their answers is an error. Serving the
// thread without the submission's dates would show a trooper on leave as
// needing review.
func TestFindLoas_SubmissionReadFailureIsAnError(t *testing.T) {
	for _, table := range []string{"xf_snog_forms_logs", "xf_snog_forms_answers"} {
		t.Run("without "+table, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha)
			insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, message: postWithoutEndDate(t)})
			insertLoaSubmission(t, ds, loaSubmission{id: 1, form: loaForm2, at: loaPosted, end: "Oct 23, 2026"})
			mustExec(t, ds, "DROP TABLE "+table)

			loas, err := ds.FindLoas(context.Background(), datastores.LoaQuery{Now: loaNow})
			if err == nil {
				t.Fatalf("FindLoas with %s gone: got %d LOAs and no error, want an error", table, len(loas))
			}
		})
	}
}
