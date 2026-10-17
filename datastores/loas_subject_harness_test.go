package datastores_test

import (
	"context"
	"testing"
	"time"

	"github.com/7cav/api/datastores"
	"github.com/7cav/api/types"
)

// subjectOf returns the Subject of the LOA on threadID, failing the test when
// the thread is not returned.
func subjectOf(t *testing.T, ds datastores.Mysql, threadID uint64) types.LoaSubject {
	t.Helper()
	loa := loaByThread(findLoas(t, ds), threadID)
	if loa == nil {
		t.Fatalf("thread %d: not returned", threadID)
	}
	return loa.Subject
}

// A Username label that holds a user mention names the mentioned member,
// even when the mention's text is now another member's current username.
// The mention's text is the name the member had when the post was written.
func TestFindLoas_MentionResolvesTheSubjectById(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha, loaBravo)
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoa1_7,
		message: edited(t, loaPostForm2Node180, "[USER=9001]Loa.Alpha[/USER]", "[USER=9001]Loa.Bravo[/USER]")})

	if got, want := subjectOf(t, ds, 7001), loaAlpha.subject(); got != want {
		t.Errorf("subject = %+v, want the mentioned member %+v", got, want)
	}
}

// loaRename is one row of the forum's record of username changes.
type loaRename struct {
	member   loaMember
	from, to string
	state    string // change_state; "": approved
	at       time.Time
}

func insertLoaRenames(t *testing.T, ds datastores.Mysql, renames ...loaRename) {
	t.Helper()
	for _, r := range renames {
		state := r.state
		if state == "" {
			state = "approved"
		}
		mustExec(t, ds, `INSERT INTO xf_username_change
			(user_id, old_username, new_username, change_state, change_user_id, change_date)
			VALUES (?, ?, ?, ?, ?, ?)`,
			r.member.userID, r.from, r.to, state, r.member.userID, r.at.Unix())
	}
}

// handPostedNaming is the open hand-posted sample with its typed Username
// value replaced by label.
func handPostedNaming(t *testing.T, label string) string {
	t.Helper()
	return edited(t, handPostedOpen(t), "[B]Username[/B]: Loa.Alpha", "[B]Username[/B]: "+label)
}

// A typed label that matches no current username names the member who
// renamed away from it, as the forum's approved username changes record.
// FindLoas reports that Subject with the member's current username.
func TestFindLoas_PreviousUsernameResolvesTheSubject(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label string
	}{
		{"label as the change recorded it", "Loa.Formerly"},
		{"label typed in another case", "LOA.FORMERLY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha)
			// Alpha posted as Loa.Formerly, then renamed twice.
			insertLoaRenames(t, ds,
				loaRename{member: loaAlpha, from: "Loa.Formerly", to: "Loa.Middle", at: loaPosted.AddDate(0, 0, 2)},
				loaRename{member: loaAlpha, from: "Loa.Middle", to: "Loa.Alpha", at: loaPosted.AddDate(0, 0, 5)})
			insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID,
				message: handPostedNaming(t, tc.label)})

			if got, want := subjectOf(t, ds, 7001), loaAlpha.subject(); got != want {
				t.Errorf("subject = %+v, want %+v", got, want)
			}
		})
	}
}

// A username change that was never approved doesn't count. A label holding
// the old name of a member whose change is still pending, or was rejected,
// doesn't name that member.
func TestFindLoas_UnapprovedRenameDoesNotResolveTheSubject(t *testing.T) {
	for _, state := range []string{"moderated", "rejected"} {
		t.Run(state, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha)
			insertLoaRenames(t, ds,
				loaRename{member: loaAlpha, from: "Loa.Formerly", to: "Loa.Alpha", state: state, at: loaPosted.AddDate(0, 0, 2)})
			insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID,
				message: handPostedNaming(t, "Loa.Formerly")})

			if loa := loaByThread(findLoas(t, ds), 7001); loa != nil && loa.Subject.UserId == loaAlpha.userID {
				t.Errorf("subject = %+v, want the %s change not to name %s", loa.Subject, state, loaAlpha.username)
			}
		})
	}
}

// When a typed label is one member's current username and another member's
// previous one, the current username wins.
func TestFindLoas_CurrentUsernameWinsOverPreviousUsername(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha, loaBravo)
	insertLoaRenames(t, ds,
		loaRename{member: loaAlpha, from: "Loa.Bravo", to: "Loa.Alpha", at: loaPosted.AddDate(-1, 0, 0)})
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaBravo.userID,
		message: handPostedNaming(t, "Loa.Bravo")})

	if got, want := subjectOf(t, ds, 7001), loaBravo.subject(); got != want {
		t.Errorf("subject = %+v, want the member currently named Loa.Bravo %+v", got, want)
	}
}

// When several members renamed away from a typed label, the Subject is the
// member who held that name when the thread was posted: the one whose change
// away from it is the earliest after the post. When every change is before
// the post, it is the member who renamed away from it last. Each case posts
// its thread on loaPosted, Oct 9, 2026.
func TestFindLoas_PreviousUsernameOfSeveralMembersNamesWhoHeldItAtPostTime(t *testing.T) {
	for _, tc := range []struct {
		name    string
		renames []loaRename // in insert order
		want    loaMember
	}{
		{"held at post time", []loaRename{
			{member: loaCharlie, from: "Loa.Formerly", to: "Loa.Charlie", at: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)},
			{member: loaBravo, from: "Loa.Formerly", to: "Loa.Bravo", at: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)},
			{member: loaAlpha, from: "Loa.Formerly", to: "Loa.Alpha", at: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)},
		}, loaBravo},
		{"every change before the post", []loaRename{
			{member: loaBravo, from: "Loa.Formerly", to: "Loa.Bravo", at: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)},
			{member: loaAlpha, from: "Loa.Formerly", to: "Loa.Alpha", at: time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)},
		}, loaBravo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := openHarnessDatastore(t)
			insertLoaMembers(t, ds, loaAlpha, loaBravo, loaCharlie)
			insertLoaRenames(t, ds, tc.renames...)
			insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: tc.want.userID,
				message: handPostedNaming(t, "Loa.Formerly")})

			if got, want := subjectOf(t, ds, 7001), tc.want.subject(); got != want {
				t.Errorf("subject = %+v, want %+v", got, want)
			}
		})
	}
}

// A failure to read the username changes is an error, never a list that
// leaves out the LOAs whose label holds an old name.
func TestFindLoas_UsernameChangeReadFailureIsAnError(t *testing.T) {
	ds := openHarnessDatastore(t)
	insertLoaMembers(t, ds, loaAlpha)
	insertLoaRenames(t, ds,
		loaRename{member: loaAlpha, from: "Loa.Formerly", to: "Loa.Alpha", at: loaPosted.AddDate(0, 0, 2)})
	insertLoaThread(t, ds, loaThread{id: 7001, node: nodeLoaACD, starter: loaAlpha.userID,
		message: handPostedNaming(t, "Loa.Formerly")})
	mustExec(t, ds, "DROP TABLE xf_username_change")

	loas, err := ds.FindLoas(context.Background(), datastores.LoaQuery{Now: loaNow})
	if err == nil {
		t.Fatalf("FindLoas with xf_username_change gone: got %d LOAs and no error, want an error", len(loas))
	}
}
