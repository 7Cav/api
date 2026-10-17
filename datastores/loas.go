package datastores

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/7cav/api/types"
	"github.com/7cav/api/xenforo"
)

// LoaQuery describes the window FindLoas reads. Now is the request instant,
// and today is its UTC calendar date.
type LoaQuery struct {
	Now time.Time
}

// loaForums maps each LOA forum's node id to the forum it reports. Adding an
// LOA forum is a code change.
var loaForums = map[uint32]types.LoaForum{
	180: types.LoaForum1_7,
	178: types.LoaForum2_7,
	540: types.LoaForum3_7,
	400: types.LoaForumACD,
	369: types.LoaForumRegimental,
}

func loaForumNodes() []uint32 {
	nodes := make([]uint32, 0, len(loaForums))
	for node := range loaForums {
		nodes = append(nodes, node)
	}
	return nodes
}

// FindLoas reads the visible threads in the LOA forums and returns one LOA
// per thread, ordered by thread id. It reads each opening post as it reads
// now and resolves the Subject from the post's last Username label by
// mention id, current username or approved previous username. It keeps the
// open and upcoming LOAs, and the needs-review ones whose thread was posted
// in the last 14 days.
func (ds *Mysql) FindLoas(ctx context.Context, q LoaQuery) ([]*types.Loa, error) {
	today := utcDate(q.Now)
	// Read only threads posted in the year before the window starts. The
	// window starts today.
	lookback := today.AddDate(-1, 0, 0)
	// A needs-review LOA comes back when its thread was posted on or after
	// midnight UTC 14 days before today.
	reviewSince := today.AddDate(0, 0, -loaReviewWindowDays)

	var threads []struct {
		ThreadID uint32 `gorm:"column:thread_id"`
		NodeID   uint32 `gorm:"column:node_id"`
		PostDate uint32 `gorm:"column:post_date"`
		Message  string `gorm:"column:message"`
	}
	err := ds.Db.WithContext(ctx).Model(&xenforo.Thread{}).
		Select("xf_thread.thread_id, xf_thread.node_id, xf_thread.post_date, xf_post.message").
		Joins("JOIN xf_post ON xf_post.post_id = xf_thread.first_post_id").
		Where("xf_thread.node_id IN ?", loaForumNodes()).
		Where("xf_thread.discussion_state = ?", "visible").
		Where("xf_thread.post_date >= ?", lookback.Unix()).
		Order("xf_thread.thread_id").
		Scan(&threads).Error
	if err != nil {
		return nil, fmt.Errorf("reading LOA threads: %w", err)
	}

	posts := make([]loaPost, len(threads))
	for i, th := range threads {
		posts[i] = parseLoaPost(th.Message)
	}
	subjects, err := ds.loaSubjects(ctx, posts)
	if err != nil {
		return nil, err
	}

	base := ds.forumBaseURL()
	loas := make([]*types.Loa, 0, len(threads))
	for i, th := range threads {
		post := posts[i]
		var link string
		if base != "" {
			link = fmt.Sprintf("%s/threads/%d/", base, th.ThreadID)
		}
		loa := &types.Loa{
			ThreadId:  uint64(th.ThreadID),
			ForumUrl:  link,
			PostDate:  th.PostDate,
			Forum:     loaForums[th.NodeID],
			StartDate: loaDateString(post.start),
			EndDate:   loaDateString(post.end),
		}
		subject, known := subjects.resolve(post, th.PostDate)
		if !known {
			// A Subject who matches no member shows the name as written,
			// with user and milpac ids of 0. A post with no Username label
			// names no one.
			subject = types.LoaSubject{Username: post.subject}
		}
		loa.Subject = subject
		if reason := loaReviewReason(post, known); reason != types.LoaReviewReasonUnspecified {
			// A needs-review LOA may give no End Date to window on, so its
			// thread's post date decides instead.
			if int64(th.PostDate) < reviewSince.Unix() {
				continue
			}
			loa.State = types.LoaStateNeedsReview
			loa.ReviewReason = reason
			loas = append(loas, loa)
			continue
		}
		loa.State = loaState(today, post.start, post.end)
		if loa.State == types.LoaStateEnded {
			continue
		}
		loas = append(loas, loa)
	}
	return loas, nil
}

// loaSubjectIndex holds the members the posts' Username labels can resolve
// to.
type loaSubjectIndex struct {
	// byID holds the members the labels mention, and the members who
	// renamed away from a typed label, by user id.
	byID map[uint64]types.LoaSubject
	// byName holds the members whose current username a typed label
	// names, by lowercased username.
	byName map[string]types.LoaSubject
	// renamedFrom holds the approved changes away from a typed label, by
	// the lowercased old username.
	renamedFrom map[string][]loaRename
}

// loaRename is one member's approved change away from a username.
type loaRename struct {
	userID uint64
	date   uint32
}

// resolve returns the Subject a post's last Username label names. A mention
// names the member by id, whatever its text says now, because the text is
// the name the member had when the post was written. A typed label names
// the member with that current username, without regard to case, and
// otherwise the member who renamed away from it.
func (s loaSubjectIndex) resolve(post loaPost, posted uint32) (types.LoaSubject, bool) {
	if post.mentionID != 0 {
		subject, ok := s.byID[post.mentionID]
		return subject, ok
	}
	name := strings.ToLower(post.subject)
	if subject, ok := s.byName[name]; ok {
		return subject, true
	}
	if renames := s.renamedFrom[name]; len(renames) > 0 {
		subject, ok := s.byID[heldAt(renames, posted)]
		return subject, ok
	}
	return types.LoaSubject{}, false
}

// heldAt picks, among the members who renamed away from one name, the member
// who held it when the thread was posted. That is the member whose change
// away from it is the earliest after the post. When every change is before
// the post, it is the member who renamed away from it last.
func heldAt(renames []loaRename, posted uint32) uint64 {
	var after, before *loaRename
	for i := range renames {
		r := &renames[i]
		if r.date > posted {
			if after == nil || r.date < after.date {
				after = r
			}
		} else if before == nil || r.date > before.date {
			before = r
		}
	}
	if after != nil {
		return after.userID
	}
	return before.userID
}

// loaSubjects reads the members the posts' Username labels can resolve to.
func (ds *Mysql) loaSubjects(ctx context.Context, posts []loaPost) (loaSubjectIndex, error) {
	idx := loaSubjectIndex{
		byID:        map[uint64]types.LoaSubject{},
		byName:      map[string]types.LoaSubject{},
		renamedFrom: map[string][]loaRename{},
	}
	var ids []uint64
	var names []string
	for _, p := range posts {
		switch {
		case p.mentionID != 0:
			ids = append(ids, p.mentionID)
		case p.hasSubject:
			names = append(names, p.subject)
		}
	}
	if len(names) > 0 {
		// xf_user.username is unique under a case-insensitive collation,
		// so a lowercased key names one member.
		members, err := ds.loaMembers(ctx, "u.username IN ?", names)
		if err != nil {
			return idx, err
		}
		for _, m := range members {
			idx.byName[strings.ToLower(m.Username)] = m
		}
	}
	var unmatched []string
	for _, name := range names {
		if _, ok := idx.byName[strings.ToLower(name)]; !ok {
			unmatched = append(unmatched, name)
		}
	}
	if len(unmatched) > 0 {
		// Only approved changes count. A pending or rejected one never
		// renamed the member. Reading them in change order keeps a tie
		// between change dates resolving to the same member every time.
		var changes []xenforo.UsernameChange
		err := ds.Db.WithContext(ctx).
			Where("old_username IN ?", unmatched).
			Where("change_state = ?", "approved").
			Order("change_id").
			Find(&changes).Error
		if err != nil {
			return idx, fmt.Errorf("reading previous usernames: %w", err)
		}
		for _, c := range changes {
			name := strings.ToLower(c.OldUsername)
			idx.renamedFrom[name] = append(idx.renamedFrom[name], loaRename{userID: uint64(c.UserID), date: c.ChangeDate})
			ids = append(ids, uint64(c.UserID))
		}
	}
	if len(ids) > 0 {
		members, err := ds.loaMembers(ctx, "u.user_id IN ?", ids)
		if err != nil {
			return idx, err
		}
		for _, m := range members {
			idx.byID[m.UserId] = m
		}
	}
	return idx, nil
}

// loaMembers reads the members a condition on xf_user selects, each with
// their current username and milpac id. A member with no milpac record gets
// a milpac id of 0.
func (ds *Mysql) loaMembers(ctx context.Context, where string, args ...any) ([]types.LoaSubject, error) {
	var rows []struct {
		UserID     uint64 `gorm:"column:user_id"`
		Username   string `gorm:"column:username"`
		RelationID uint64 `gorm:"column:relation_id"`
	}
	// xf_nf_rosters_user.user_id isn't unique by schema. Should a member
	// ever hold two milpac records, the lowest relation id wins.
	err := ds.Db.WithContext(ctx).Table("xf_user AS u").
		Select("u.user_id, u.username, COALESCE(m.relation_id, 0) AS relation_id").
		Joins("LEFT JOIN xf_nf_rosters_user AS m ON m.user_id = u.user_id").
		Where(where, args...).
		Order("m.relation_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("resolving LOA subjects: %w", err)
	}
	seen := map[uint64]bool{}
	members := make([]types.LoaSubject, 0, len(rows))
	for _, r := range rows {
		if !seen[r.UserID] {
			seen[r.UserID] = true
			members = append(members, types.LoaSubject{UserId: r.UserID, Username: r.Username, MilpacId: r.RelationID})
		}
	}
	return members, nil
}

// loaMaxDays is how many days past its Start Date an LOA's End Date may
// fall. A Saturday-to-Saturday LOA ends exactly 14 days after it starts, and
// the API doesn't flag it.
const loaMaxDays = 14

// loaReviewWindowDays is how many days back, by post date, the default
// window reaches for needs-review threads.
const loaReviewWindowDays = 14

// loaReviewReason names the rule a thread trips, or returns
// LoaReviewReasonUnspecified when the API can take it as written. known
// reports whether the post's Username label matched a member. When several
// rules apply it reports the first in this order, a choice the spec leaves
// open.
func loaReviewReason(post loaPost, known bool) types.LoaReviewReason {
	switch {
	case post.start.IsZero() || post.end.IsZero():
		return types.LoaReviewReasonDatesUnreadable
	case post.end.Before(post.start):
		return types.LoaReviewReasonEndBeforeStart
	case post.end.After(post.start.AddDate(0, 0, loaMaxDays)):
		return types.LoaReviewReasonTooLong
	case !post.hasSubject:
		return types.LoaReviewReasonNoSubject
	case !known:
		return types.LoaReviewReasonUnknownSubject
	default:
		return types.LoaReviewReasonUnspecified
	}
}

// loaDateString writes a date the post gave as YYYY-MM-DD, and a date it
// didn't as "".
func loaDateString(d time.Time) string {
	if d.IsZero() {
		return ""
	}
	return d.Format(layoutISO)
}

// utcDate returns midnight UTC on t's UTC calendar date.
func utcDate(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// loaState places an LOA in time. It is open from the start of its Start
// Date through the end of its End Date, upcoming before that, and ended
// after.
func loaState(today, start, end time.Time) types.LoaState {
	switch {
	case today.Before(start):
		return types.LoaStateUpcoming
	case today.After(end):
		return types.LoaStateEnded
	default:
		return types.LoaStateOpen
	}
}
