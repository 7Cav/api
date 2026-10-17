package types

import "strconv"

// Loa is one LOA as GET /api/v1/loas serves it, read from the opening post
// of its thread in an LOA forum. GLOSSARY.md defines the LOA terms. Each
// thread gives one record, and ThreadId is its stable key. Deleting the
// thread withdraws the LOA.
//
// StartDate and EndDate are YYYY-MM-DD calendar dates, and the datastore
// computes State from them against the request's UTC date. ReviewReason
// ships at its empty value until the needs-review rules land.
//
// Reason is the reason the trooper gave in the post, as plain text with the
// post's line breaks kept. It is "" when the post gives none.
type Loa struct {
	ThreadId     uint64          `json:"threadId,string"`
	ForumUrl     string          `json:"forumUrl"`
	PostDate     uint32          `json:"postDate"`
	Forum        LoaForum        `json:"forum"`
	Subject      LoaSubject      `json:"subject"`
	StartDate    string          `json:"startDate"`
	EndDate      string          `json:"endDate"`
	State        LoaState        `json:"state"`
	ReviewReason LoaReviewReason `json:"reviewReason"`
	Reason       string          `json:"reason"`
}

// LoaSubject is the trooper on leave, by forum user id, current forum
// username and milpac relation id. MilpacId is 0 when the trooper has no
// milpac record.
type LoaSubject struct {
	UserId   uint64 `json:"userId,string"`
	Username string `json:"username"`
	MilpacId uint64 `json:"milpacId,string"`
}

// LoasResponse is the GET /api/v1/loas envelope. Loas is a List, so an empty
// window serializes as {"loas":[]}, never null.
type LoasResponse struct {
	Loas List[*Loa] `json:"loas"`
}

// LoaForum names the LOA forum a thread sits in. A served LOA always carries
// one of the five named forums. The zero value exists only as the enum's
// unset value.
type LoaForum int32

const (
	LoaForumUnspecified LoaForum = 0
	LoaForum1_7         LoaForum = 1
	LoaForum2_7         LoaForum = 2
	LoaForum3_7         LoaForum = 3
	LoaForumACD         LoaForum = 4
	LoaForumRegimental  LoaForum = 5
)

var loaForumNames = map[LoaForum]string{
	LoaForumUnspecified: "LOA_FORUM_UNSPECIFIED",
	LoaForum1_7:         "LOA_FORUM_1_7",
	LoaForum2_7:         "LOA_FORUM_2_7",
	LoaForum3_7:         "LOA_FORUM_3_7",
	LoaForumACD:         "LOA_FORUM_ACD",
	LoaForumRegimental:  "LOA_FORUM_REGIMENTAL",
}

func (f LoaForum) String() string { return enumString(loaForumNames, f) }

func (f LoaForum) MarshalJSON() ([]byte, error) { return enumJSON(loaForumNames, f) }

// LoaState is an LOA's place in time. An LOA is open from the start of its
// Start Date through the end of its End Date in UTC calendar days, upcoming
// before that, and ended after. Needs review marks a thread whose dates or
// Subject can't be taken as written.
type LoaState int32

const (
	LoaStateUnspecified LoaState = 0
	LoaStateOpen        LoaState = 1
	LoaStateUpcoming    LoaState = 2
	LoaStateEnded       LoaState = 3
	LoaStateNeedsReview LoaState = 4
)

var loaStateNames = map[LoaState]string{
	LoaStateUnspecified: "LOA_STATE_UNSPECIFIED",
	LoaStateOpen:        "LOA_STATE_OPEN",
	LoaStateUpcoming:    "LOA_STATE_UPCOMING",
	LoaStateEnded:       "LOA_STATE_ENDED",
	LoaStateNeedsReview: "LOA_STATE_NEEDS_REVIEW",
}

func (s LoaState) String() string { return enumString(loaStateNames, s) }

func (s LoaState) MarshalJSON() ([]byte, error) { return enumJSON(loaStateNames, s) }

// LoaReviewReason names the rule a needs-review LOA tripped. Every LOA not in
// review carries the zero value.
type LoaReviewReason int32

const (
	LoaReviewReasonUnspecified     LoaReviewReason = 0
	LoaReviewReasonDatesUnreadable LoaReviewReason = 1
	LoaReviewReasonEndBeforeStart  LoaReviewReason = 2
	LoaReviewReasonTooLong         LoaReviewReason = 3
	LoaReviewReasonNoSubject       LoaReviewReason = 4
	LoaReviewReasonUnknownSubject  LoaReviewReason = 5
)

var loaReviewReasonNames = map[LoaReviewReason]string{
	LoaReviewReasonUnspecified:     "LOA_REVIEW_REASON_UNSPECIFIED",
	LoaReviewReasonDatesUnreadable: "LOA_REVIEW_REASON_DATES_UNREADABLE",
	LoaReviewReasonEndBeforeStart:  "LOA_REVIEW_REASON_END_BEFORE_START",
	LoaReviewReasonTooLong:         "LOA_REVIEW_REASON_TOO_LONG",
	LoaReviewReasonNoSubject:       "LOA_REVIEW_REASON_NO_SUBJECT",
	LoaReviewReasonUnknownSubject:  "LOA_REVIEW_REASON_UNKNOWN_SUBJECT",
}

func (r LoaReviewReason) String() string { return enumString(loaReviewReasonNames, r) }

func (r LoaReviewReason) MarshalJSON() ([]byte, error) { return enumJSON(loaReviewReasonNames, r) }

// enumString and enumJSON apply the enum convention that RosterType spells
// out by hand. They emit the name when the catalog has one and the bare
// number otherwise.
func enumString[E ~int32](names map[E]string, v E) string {
	if name, ok := names[v]; ok {
		return name
	}
	return strconv.FormatInt(int64(v), 10)
}

func enumJSON[E ~int32](names map[E]string, v E) ([]byte, error) {
	if name, ok := names[v]; ok {
		return strconv.AppendQuote(nil, name), nil
	}
	return strconv.AppendInt(nil, int64(v), 10), nil
}
