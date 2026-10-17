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
// now, resolves the Subject by current username, and keeps the open and
// upcoming LOAs.
func (ds *Mysql) FindLoas(ctx context.Context, q LoaQuery) ([]*types.Loa, error) {
	today := utcDate(q.Now)
	// Read only threads posted in the year before the window starts. The
	// window starts today.
	lookback := today.AddDate(-1, 0, 0)

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
	names := make([]string, 0, len(threads))
	for i, th := range threads {
		posts[i] = parseLoaPost(th.Message)
		if posts[i].hasSubject {
			names = append(names, posts[i].subject)
		}
	}
	members, err := ds.loaMembersByUsername(ctx, names)
	if err != nil {
		return nil, err
	}

	base := ds.forumBaseURL()
	loas := make([]*types.Loa, 0, len(threads))
	for i, th := range threads {
		post := posts[i]
		subject, known := members[strings.ToLower(post.subject)]
		// For now a thread whose dates or Subject can't be read is left
		// out. Later it comes back as needs review.
		if post.start.IsZero() || post.end.IsZero() || !post.hasSubject || !known {
			continue
		}
		state := loaState(today, post.start, post.end)
		if state == types.LoaStateEnded {
			continue
		}
		var link string
		if base != "" {
			link = fmt.Sprintf("%s/threads/%d/", base, th.ThreadID)
		}
		loas = append(loas, &types.Loa{
			ThreadId:  uint64(th.ThreadID),
			ForumUrl:  link,
			PostDate:  th.PostDate,
			Forum:     loaForums[th.NodeID],
			Subject:   subject,
			StartDate: post.start.Format(layoutISO),
			EndDate:   post.end.Format(layoutISO),
			State:     state,
			Reason:    post.reason,
		})
	}
	return loas, nil
}

// loaMembersByUsername resolves names to members by current username,
// without regard to case, keyed by the lowercased name. xf_user.username is
// unique under a case-insensitive collation, so a key names one member. A
// member with no milpac record gets a milpac id of 0.
func (ds *Mysql) loaMembersByUsername(ctx context.Context, names []string) (map[string]types.LoaSubject, error) {
	members := map[string]types.LoaSubject{}
	if len(names) == 0 {
		return members, nil
	}
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
		Where("u.username IN ?", names).
		Order("m.relation_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("resolving LOA subjects: %w", err)
	}
	for _, r := range rows {
		key := strings.ToLower(r.Username)
		if _, seen := members[key]; !seen {
			members[key] = types.LoaSubject{UserId: r.UserID, Username: r.Username, MilpacId: r.RelationID}
		}
	}
	return members, nil
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
