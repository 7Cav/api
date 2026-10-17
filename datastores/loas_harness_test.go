package datastores_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
	"github.com/spf13/viper"
)

// loaNow is the fixed request instant every LOA test passes. It is 21:30 on
// Oct 16 in a UTC-4 zone, which is 01:30 UTC on Oct 17. Today is the UTC
// date, Oct 17, while the local date is still Oct 16, so code that reads
// the local date instead of the UTC one turns a state row red.
var loaNow = time.Date(2026, 10, 16, 21, 30, 0, 0, time.FixedZone("UTC-4", -4*60*60))

// loaPosted is the default thread post time, about a week before loaNow and
// well inside the one-year lookback.
var loaPosted = time.Date(2026, 10, 9, 18, 0, 0, 0, time.UTC)

// LOA forum nodes, and one forum that is not an LOA forum.
const (
	nodeLoa1_7        = 180
	nodeLoa2_7        = 178
	nodeLoa3_7        = 540
	nodeLoaACD        = 400
	nodeLoaRegimental = 369
	nodeEloa          = 341
)

// loaFormsBot is the account the LOA forms post their threads as. A
// hand-posted thread is started by the member instead.
const loaFormsBot = 9000

type loaMember struct {
	userID     uint64
	username   string
	relationID uint64 // 0: no milpac record
}

// Members the fixture posts name. User ids and milpac relation ids differ so
// a lookup that confuses the two keys goes red.
var (
	loaAlpha = loaMember{userID: 9001, username: "Loa.Alpha", relationID: 9501}
	loaBravo = loaMember{userID: 9002, username: "Loa.Bravo", relationID: 9502}
	// loaCharlie has no milpac record.
	loaCharlie = loaMember{userID: 9003, username: "Loa.Charlie"}
)

// subject is the wire Subject a member resolves to.
func (m loaMember) subject() types.LoaSubject {
	return types.LoaSubject{UserId: m.userID, Username: m.username, MilpacId: m.relationID}
}

func insertLoaMembers(t *testing.T, ds datastores.Mysql, members ...loaMember) {
	t.Helper()
	for _, m := range members {
		mustExec(t, ds, `INSERT INTO xf_user
			(user_id, username, email, language_id, style_id, timezone,
			 user_group_id, secondary_group_ids, permission_combination_id, secret_key)
			VALUES (?, ?, ?, 1, 0, 'UTC', 2, '', 1, 'k')`,
			m.userID, m.username, m.username+"@example.test")
		if m.relationID != 0 {
			mustExec(t, ds, `INSERT INTO xf_nf_rosters_user
				(relation_id, roster_id, user_id, username, position_id,
				 secondary_position_ids, rank_id, bio, custom_fields)
				VALUES (?, 1, ?, ?, 10, '', 27, '', '{}')`,
				m.relationID, m.userID, m.username)
		}
	}
}

type loaThread struct {
	id       uint64
	node     uint32
	postDate time.Time // zero: loaPosted
	state    string    // discussion_state; "": visible
	starter  uint64    // thread starter's user id; 0: loaFormsBot
	message  string    // the opening post
}

// insertLoaThread inserts a thread and its opening post.
func insertLoaThread(t *testing.T, ds datastores.Mysql, th loaThread) {
	t.Helper()
	posted := th.postDate
	if posted.IsZero() {
		posted = loaPosted
	}
	state := th.state
	if state == "" {
		state = "visible"
	}
	starter := th.starter
	if starter == 0 {
		starter = loaFormsBot
	}
	postID := th.id + 500000
	mustExec(t, ds, `INSERT INTO xf_thread
		(thread_id, node_id, title, user_id, username, post_date, discussion_state,
		 discussion_type, type_data, first_post_id, last_post_date, last_post_id,
		 last_post_user_id, last_post_username, tags, custom_fields, vote_score)
		VALUES (?, ?, 'LOA', ?, '', ?, ?, 'discussion', '', ?, ?, ?, ?, '', '', '', 0)`,
		th.id, th.node, starter, posted.Unix(), state, postID, posted.Unix(), postID, starter)
	mustExec(t, ds, `INSERT INTO xf_post
		(post_id, thread_id, user_id, username, post_date, message, position,
		 type_data, reaction_users, vote_score)
		VALUES (?, ?, ?, '', ?, ?, 0, '', '', 0)`,
		postID, th.id, starter, posted.Unix(), th.message)
}

func mustExec(t *testing.T, ds datastores.Mysql, query string, args ...any) {
	t.Helper()
	if err := ds.Db.Exec(query, args...).Error; err != nil {
		t.Fatalf("seeding LOA fixtures: %v", err)
	}
}

// edited returns post with its one occurrence of from replaced by to. That is
// the one-field change a row makes to a stored post.
func edited(t *testing.T, post, from, to string) string {
	t.Helper()
	if n := strings.Count(post, from); n != 1 {
		t.Fatalf("fixture edit: %q occurs %d times in the post, want exactly 1", from, n)
	}
	return strings.Replace(post, from, to, 1)
}

// handPostedOpen is the hand-posted sample with its Sep 13 to Sep 27 dates
// moved a month later, so it is open on loaNow.
func handPostedOpen(t *testing.T) string {
	t.Helper()
	post := edited(t, loaPostHandPostedTemplateCopy, "Sep 13, 2026", "Oct 13, 2026")
	return edited(t, post, "Sep 27, 2026", "Oct 27, 2026")
}

func findLoas(t *testing.T, ds datastores.Mysql) []*types.Loa {
	t.Helper()
	loas, err := ds.FindLoas(context.Background(), datastores.LoaQuery{Now: loaNow})
	if err != nil {
		t.Fatalf("FindLoas: %v", err)
	}
	return loas
}

func loaByThread(loas []*types.Loa, threadID uint64) *types.Loa {
	for _, l := range loas {
		if l.ThreadId == threadID {
			return l
		}
	}
	return nil
}

// A form-posted thread in an LOA forum is an LOA.
func TestFindLoas_FormPostedThreadInLoaForumCounts(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, message: loaPostForm2Node180})

	if loaByThread(findLoas(t, ds), 7001) == nil {
		t.Error("a form-posted thread in the 1-7 LOA forum must be returned")
	}
}

// A hand-posted thread counts the same as a form-posted one. The member
// started this one, it copies the form's text without the COLOR markup, and
// its lines end in bare LF.
func TestFindLoas_HandPostedThreadCounts(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID, message: handPostedOpen(t)})

	if loaByThread(findLoas(t, ds), 7001) == nil {
		t.Error("a hand-posted thread in the ACD LOA forum must be returned")
	}
}

// Deleted and moderated threads are not LOAs, and neither is a thread in a
// forum that is not an LOA forum.
func TestFindLoas_OnlyVisibleThreadsInLoaForumsCount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		node  uint32
		state string
	}{
		{"deleted thread", nodeLoa1_7, "deleted"},
		{"moderated thread", nodeLoa1_7, "moderated"},
		{"thread in the ELOA forum", nodeEloa, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha)
			insertLoaThread(t, ds, loaThread{id: 7001, node: tc.node, state: tc.state, message: loaPostForm2Node180})

			if loaByThread(findLoas(t, ds), 7001) != nil {
				t.Errorf("a %s must not be returned", tc.name)
			}
		})
	}
}

// Each LOA names the LOA forum its thread sits in.
func TestFindLoas_ForumNamesTheLoaForum(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha, loaBravo)
	// The 2-7 and 3-7 samples ended on Oct 16. Their End Date moves so they
	// are open on loaNow.
	threads := []struct {
		thread loaThread
		want   types.LoaForum
	}{
		{loaThread{id: 7001, node: nodeLoa1_7, message: loaPostForm2Node180}, types.LoaForum1_7},
		{loaThread{id: 7002, node: nodeLoa2_7, message: edited(t, loaPostForm4Node178, "Oct 16, 2026", "Oct 23, 2026")}, types.LoaForum2_7},
		{loaThread{id: 7003, node: nodeLoa3_7, message: edited(t, loaPostForm16Node540, "Oct 16, 2026", "Oct 17, 2026")}, types.LoaForum3_7},
		{loaThread{id: 7004, node: nodeLoaACD, message: loaPostForm12Node400FiledForOther}, types.LoaForumACD},
		{loaThread{id: 7005, node: nodeLoaRegimental, message: loaPostForm11Node369}, types.LoaForumRegimental},
	}
	for _, th := range threads {
		insertLoaThread(t, ds, th.thread)
	}

	loas := findLoas(t, ds)
	for _, th := range threads {
		loa := loaByThread(loas, th.thread.id)
		if loa == nil {
			t.Errorf("thread %d in node %d: not returned", th.thread.id, th.thread.node)
			continue
		}
		if loa.Forum != th.want {
			t.Errorf("thread %d in node %d: forum = %v, want %v", th.thread.id, th.thread.node, loa.Forum, th.want)
		}
	}
}

// An LOA is open from the start of its Start Date through the end of its End
// Date, counted in UTC calendar days, and upcoming before that. Ended LOAs
// are not returned. loaNow is Oct 17 in UTC but still Oct 16 locally, so the
// first and last rows flip if the local date is read instead.
func TestFindLoas_StateCountsUtcCalendarDays(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	// The sample runs Oct 10 to Oct 23, and each row moves one of its dates.
	rows := []struct {
		name     string
		thread   uint64
		from, to string
		want     types.LoaState // unspecified: not returned
	}{
		{"starts today in UTC, tomorrow locally", 7001, "Oct 10, 2026", "Oct 17, 2026", types.LoaStateOpen},
		{"on its End Date", 7002, "Oct 23, 2026", "Oct 17, 2026", types.LoaStateOpen},
		{"starts tomorrow", 7003, "Oct 10, 2026", "Oct 18, 2026", types.LoaStateUpcoming},
		{"ended yesterday in UTC, today locally", 7004, "Oct 23, 2026", "Oct 16, 2026", types.LoaStateUnspecified},
	}
	for _, r := range rows {
		insertLoaThread(t, ds, loaThread{id: r.thread, node: nodeLoa1_7, message: edited(t, loaPostForm2Node180, r.from, r.to)})
	}

	loas := findLoas(t, ds)
	for _, r := range rows {
		loa := loaByThread(loas, r.thread)
		switch {
		case r.want == types.LoaStateUnspecified && loa != nil:
			t.Errorf("%s: returned as %v, want it left out", r.name, loa.State)
		case r.want != types.LoaStateUnspecified && loa == nil:
			t.Errorf("%s: not returned, want %v", r.name, r.want)
		case loa != nil && loa.State != r.want:
			t.Errorf("%s: state = %v, want %v", r.name, loa.State, r.want)
		}
	}
}

// The Subject is the member named by the post's last Username label, matched
// by current username without regard to case, with their current username
// and milpac id. On an LOA filed for someone else the first label names the
// Submitter, who is never the Subject.
func TestFindLoas_SubjectIsTheLastUsernameLabelsMember(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha, loaBravo, loaCharlie)
	rows := []struct {
		name   string
		thread loaThread
		want   types.LoaSubject
	}{
		{"filed for someone else", loaThread{id: 7001, node: nodeLoaACD, message: loaPostForm12Node400FiledForOther},
			loaBravo.subject()},
		{"label typed in another case", loaThread{id: 7002, node: nodeLoaACD, starter: loaAlpha.userID,
			message: edited(t, handPostedOpen(t), "[B]Username[/B]: Loa.Alpha", "[B]Username[/B]: LOA.alpha")},
			loaAlpha.subject()},
		{"no milpac record", loaThread{id: 7003, node: nodeLoa1_7,
			message: edited(t, loaPostForm2Node180, "[USER=9001]Loa.Alpha[/USER]", "[USER=9003]Loa.Charlie[/USER]")},
			types.LoaSubject{UserId: 9003, Username: "Loa.Charlie", MilpacId: 0}},
	}
	for _, r := range rows {
		insertLoaThread(t, ds, r.thread)
	}

	loas := findLoas(t, ds)
	for _, r := range rows {
		loa := loaByThread(loas, r.thread.id)
		if loa == nil {
			t.Errorf("%s: not returned", r.name)
			continue
		}
		if loa.Subject != r.want {
			t.Errorf("%s: subject = %+v, want %+v", r.name, loa.Subject, r.want)
		}
	}
}

// The dates come from the post as it reads now. A leader who extends an LOA
// strikes the old date and writes the new one after it, and the new date
// wins. The full-month spelling reads the same as the form's abbreviation.
func TestFindLoas_DatesReadFromThePostAsItReadsNow(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7,
		message: edited(t, loaPostForm2Node180, "Oct 23, 2026", "[S]Oct 16, 2026[/S] -> Oct 23, 2026")})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoa1_7,
		message: edited(t, loaPostForm2Node180, "Oct 10, 2026", "October 10, 2026")})

	loas := findLoas(t, ds)
	if loa := loaByThread(loas, 7001); loa == nil {
		t.Error("struck-through End Date: not returned")
	} else if loa.EndDate != "2026-10-23" {
		t.Errorf("struck-through End Date: endDate = %q, want the edited 2026-10-23", loa.EndDate)
	}
	if loa := loaByThread(loas, 7002); loa == nil {
		t.Error("full-month Start Date: not returned")
	} else if loa.StartDate != "2026-10-10" {
		t.Errorf("full-month Start Date: startDate = %q, want 2026-10-10", loa.StartDate)
	}
}

// Each LOA carries its thread id, the thread's post time, and a link to the
// thread under the configured forum base URL, or "" when none is configured.
func TestFindLoas_CarriesThreadIdPostTimeAndLink(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	posted := time.Date(2026, 10, 9, 22, 41, 7, 0, time.UTC)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, postDate: posted, message: loaPostForm2Node180})

	loa := loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("thread 7001: not returned")
	}
	if want := uint32(posted.Unix()); loa.PostDate != want {
		t.Errorf("postDate = %d, want the thread's post time %d", loa.PostDate, want)
	}
	if loa.ForumUrl != "" {
		t.Errorf("without FORUM_BASE_URL forumUrl must be empty, got %q", loa.ForumUrl)
	}

	prior := viper.GetString("FORUM_BASE_URL")
	viper.Set("FORUM_BASE_URL", "https://forum.example.test")
	t.Cleanup(func() { viper.Set("FORUM_BASE_URL", prior) })

	loa = loaByThread(findLoas(t, ds), 7001)
	if loa == nil {
		t.Fatal("thread 7001 with FORUM_BASE_URL set: not returned")
	}
	if want := "https://forum.example.test/threads/7001/"; loa.ForumUrl != want {
		t.Errorf("forumUrl = %q, want %q", loa.ForumUrl, want)
	}
}

// Records come back ordered by thread id, whatever their forums, post times
// or insert order.
func TestFindLoas_OrderedByThreadId(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7003, node: nodeLoa2_7, postDate: loaPosted.Add(-2 * time.Hour),
		message: edited(t, loaPostForm4Node178, "Oct 16, 2026", "Oct 23, 2026")})
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa3_7, postDate: loaPosted,
		message: edited(t, loaPostForm16Node540, "Oct 16, 2026", "Oct 17, 2026")})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoa1_7, postDate: loaPosted.Add(-time.Hour),
		message: loaPostForm2Node180})

	var got []uint64
	for _, loa := range findLoas(t, ds) {
		got = append(got, loa.ThreadId)
	}
	if want := []uint64{7001, 7002, 7003}; !slices.Equal(got, want) {
		t.Errorf("thread ids = %v, want %v", got, want)
	}
}

// A database failure is an error, never an empty list. A consumer that
// replaces its copy on every sync would otherwise wipe it during an outage.
func TestFindLoas_DatabaseFailureIsAnError(t *testing.T) {
	for _, table := range []string{"xf_thread", "xf_user"} {
		t.Run("without "+table, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha)
			insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, message: loaPostForm2Node180})
			mustExec(t, ds, "DROP TABLE "+table)

			loas, err := ds.FindLoas(context.Background(), datastores.LoaQuery{Now: loaNow})
			if err == nil {
				t.Fatalf("FindLoas with %s gone: got %d LOAs and no error, want an error", table, len(loas))
			}
		})
	}
}

// Each request reads only threads posted in the year before today, so a
// thread posted 400 days ago stays out even when its post now gives dates
// that are open today.
func TestFindLoas_ReadsThreadsPostedInTheLastYear(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7, postDate: loaNow.AddDate(0, 0, -400), message: loaPostForm2Node180})
	insertLoaThread(t, ds, loaThread{id: 7002, node: nodeLoa1_7, postDate: loaNow.AddDate(0, 0, -300), message: loaPostForm2Node180})

	loas := findLoas(t, ds)
	if loaByThread(loas, 7001) != nil {
		t.Error("a thread posted 400 days ago must not be read")
	}
	if loaByThread(loas, 7002) == nil {
		t.Error("a thread posted 300 days ago must be read")
	}
}

// For now, a thread whose dates or Subject can't be read is left out.
func TestFindLoas_UnreadableThreadsAreLeftOut(t *testing.T) {
	for _, tc := range []struct {
		name   string
		thread loaThread
	}{
		// The End Date stays readable and in the future, so only the
		// unreadable Start Date keeps this thread out.
		{"start date unreadable", loaThread{id: 7001, node: nodeLoa1_7,
			message: edited(t, loaPostForm2Node180, "Oct 10, 2026", "October 10,2026")}},
		{"label names no member", loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID,
			message: edited(t, handPostedOpen(t), "[B]Username[/B]: Loa.Alpha", "[B]Username[/B]: Loa.Nobody")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha)
			insertLoaThread(t, ds, tc.thread)

			if loa := loaByThread(findLoas(t, ds), 7001); loa != nil {
				t.Errorf("returned as %+v, want it left out", loa)
			}
		})
	}
}
