package datastores

import (
	"context"
	"fmt"

	"github.com/7cav/api/xenforo"
)

// loaForm describes an LOA form by the LOA forum it posts its threads to and
// the ids of its Start Date, End Date and reason questions.
type loaForm struct {
	node               uint32
	start, end, reason uint32
}

// loaForms maps each LOA form's id to its forum and questions. The ACD forum
// takes two forms, reservist form 5 and ACD form 12. Adding an LOA form is a
// code change.
var loaForms = map[uint32]loaForm{
	2:  {node: 180, start: 29, end: 30, reason: 28},
	4:  {node: 178, start: 46, end: 47, reason: 45},
	16: {node: 540, start: 364, end: 365, reason: 363},
	5:  {node: 400, start: 55, end: 56, reason: 140},
	12: {node: 400, start: 172, end: 173, reason: 171},
	11: {node: 369, start: 156, end: 324, reason: 166},
}

// loaMatchSeconds is the most seconds a submission's submit time may fall
// before or after its thread's post time.
const loaMatchSeconds = 5

// loaGap is a thread whose opening post leaves a field for its form
// submission to fill.
type loaGap struct {
	node   uint32
	posted uint32
	post   *loaPost
}

// hasGap reports whether the post lacks a readable Start Date, a readable End
// Date or a reason, which its submission can then fill in.
func (p loaPost) hasGap() bool {
	return p.start.IsZero() || p.end.IsZero() || p.reason == ""
}

// fillFromSubmissions fills in each gap's post from the form submission that
// created its thread. Nothing stored links the two, so it matches them by
// time. Among the forms that post to the thread's forum, the match is the
// submission whose submit time is closest to the thread's post time, at most
// 5 seconds before or after it. Of two submissions equally close, the lower
// submission id wins. Each field the post lacks fills on its own, so a date
// the post gives stays. A thread with no submission that close, such as a
// hand-posted one, comes back as its post reads.
func (ds *Mysql) fillFromSubmissions(ctx context.Context, gaps []loaGap) error {
	if len(gaps) == 0 {
		return nil
	}
	forms := make([]uint32, 0, len(loaForms))
	for id := range loaForms {
		forms = append(forms, id)
	}
	lo, hi := gaps[0].posted, gaps[0].posted
	for _, g := range gaps {
		lo, hi = min(lo, g.posted), max(hi, g.posted)
	}
	var subs []xenforo.FormSubmission
	err := ds.Db.WithContext(ctx).
		Where("form_id IN ?", forms).
		Where("log_date BETWEEN ? AND ?", int64(lo)-loaMatchSeconds, int64(hi)+loaMatchSeconds).
		Order("log_id").
		Find(&subs).Error
	if err != nil {
		return fmt.Errorf("reading LOA form submissions: %w", err)
	}

	matched := make([]*xenforo.FormSubmission, len(gaps))
	var logIDs []uint32
	for i, g := range gaps {
		best := int64(loaMatchSeconds + 1)
		for j := range subs {
			if loaForms[subs[j].FormID].node != g.node {
				continue
			}
			if d := secondsApart(subs[j].LogDate, g.posted); d < best {
				best, matched[i] = d, &subs[j]
			}
		}
		if matched[i] != nil {
			logIDs = append(logIDs, matched[i].LogID)
		}
	}
	if len(logIDs) == 0 {
		return nil
	}

	var answers []xenforo.FormAnswer
	err = ds.Db.WithContext(ctx).
		Select("answer_id, log_id, questionid, COALESCE(answer, '') AS answer").
		Where("log_id IN ?", logIDs).
		Find(&answers).Error
	if err != nil {
		return fmt.Errorf("reading LOA form answers: %w", err)
	}
	byLog := map[uint32]map[uint32]string{}
	for _, a := range answers {
		if byLog[a.LogID] == nil {
			byLog[a.LogID] = map[uint32]string{}
		}
		byLog[a.LogID][a.QuestionID] = a.Answer
	}

	for i, g := range gaps {
		sub := matched[i]
		if sub == nil {
			continue
		}
		form := loaForms[sub.FormID]
		got := byLog[sub.LogID]
		if g.post.start.IsZero() {
			g.post.start = parseLoaDate(got[form.start])
		}
		if g.post.end.IsZero() {
			g.post.end = parseLoaDate(got[form.end])
		}
		if g.post.reason == "" {
			g.post.reason = plainText(got[form.reason])
		}
	}
	return nil
}

// secondsApart returns how many seconds lie between two unix times.
func secondsApart(a, b uint32) int64 {
	if d := int64(a) - int64(b); d >= 0 {
		return d
	}
	return int64(b) - int64(a)
}
