package datastores

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/7cav/api/proto"
	"github.com/7cav/api/referencecache"
	"github.com/7cav/api/xenforo"
	"github.com/spf13/viper"
)

// ListTicketsFilter carries the conjunctive filter knobs supported by
// ListTickets. Empty slices and zero-valued scalars mean "no filter."
type ListTicketsFilter struct {
	CategoryIDs          []uint32
	ExcludeSubcategories bool
	TicketStates         []string
	StatusIDs            []uint32
	PrefixIDs            []uint32
	AssignedUserIDs      []uint32
	StarterUserIDs       []uint32
	ModifiedSince        uint32
	IncludeHidden        bool

	PerPage     uint32
	AfterCursor string
}

// ErrInvalidCursor signals a malformed cursor string. Handlers should map
// errors wrapping this sentinel to codes.InvalidArgument → HTTP 400.
var ErrInvalidCursor = errors.New("invalid cursor")

// Compile-time assertion: Mysql must implement referencecache.Loader.
var _ referencecache.Loader = (*Mysql)(nil)

// phraseFamily names one NF Tickets reference family in xf_phrase: the
// LIKE prefix its titles carry, a short human name for diagnostics, and
// whether the family is ever legitimately empty.
//
// CAUTION — the add-on's phrase naming is ASYMMETRIC, not a typo in the
// familyStatus/familyPriority/familyPrefix prefixes below: status and
// priority titles carry NO `ticket_` infix (`nf_tickets_status.<id>`,
// `nf_tickets_priority.<id>`), but prefix titles DO
// (`nf_tickets_ticket_prefix.<id>`). This mirrors exactly what the NF
// Tickets add-on writes to xf_phrase in production; querying status/priority
// with the `ticket_` infix matches zero rows and silently warms an empty
// cache (every statusName/priorityName resolves to ""). Do not "normalise"
// these to a uniform shape — the inconsistency is in the source data, and
// the testdb fixtures (testdb/fixtures.sql) seed these exact forms.
//
// mustBePopulated guards the silent-degradation mode of #195: status and
// priority are never empty in production, so a zero-row read for them means
// the source data has drifted (an add-on phrase rename, a collation/charset
// change, a language_id regression) and must be surfaced loudly instead of
// warming a blank cache (every statusName/priorityName then resolves to "").
// prefix MAY be legitimately sparse, so it stays exempt.
type phraseFamily struct {
	prefix          string
	name            string
	mustBePopulated bool
}

var (
	familyStatus   = phraseFamily{prefix: "nf_tickets_status.", name: "status", mustBePopulated: true}
	familyPriority = phraseFamily{prefix: "nf_tickets_priority.", name: "priority", mustBePopulated: true}
	familyPrefix   = phraseFamily{prefix: "nf_tickets_ticket_prefix.", name: "prefix", mustBePopulated: false}
)

// warnIfEmpty surfaces a zero-row read for a must-be-populated family at
// Warn severity, naming the family. It is intentionally NOT triggered by
// individual missing ids within a populated map — those resolve to "" by
// design (new add-on records can land between refresh ticks). A legitimately
// sparse family (prefix) never warns.
func (f phraseFamily) warnIfEmpty(out map[uint32]string) {
	if f.mustBePopulated && len(out) == 0 {
		Warn.Printf(
			"reference cache: phrase family %q (%s%%) read zero rows — status/priority names will resolve blank on healthy responses; the xf_phrase titles for this family have likely drifted",
			f.name, f.prefix)
	}
}

func (ds *Mysql) LoadStatusNames(ctx context.Context) (map[uint32]string, error) {
	return ds.loadPhraseMap(ctx, familyStatus)
}
func (ds *Mysql) LoadPriorityNames(ctx context.Context) (map[uint32]string, error) {
	return ds.loadPhraseMap(ctx, familyPriority)
}
func (ds *Mysql) LoadPrefixNames(ctx context.Context) (map[uint32]string, error) {
	return ds.loadPhraseMap(ctx, familyPrefix)
}

// loadPhraseMap reads rows from xf_phrase whose title starts with the
// family's prefix (e.g. "nf_tickets_status."), parses the trailing integer
// id, and returns id -> phrase_text. Rows where the trailing part isn't a
// uint32 are skipped (not an error — the phrase table is shared, so
// unrelated rows can be in scope of the LIKE pattern at the edges).
//
// A successful read that matches zero rows is NOT an error (the refresh must
// not hard-fail on one drifted family and take down the rest of the cache),
// but for a must-be-populated family it is surfaced via warnIfEmpty (#195).
func (ds *Mysql) loadPhraseMap(ctx context.Context, fam phraseFamily) (map[uint32]string, error) {
	var rows []struct {
		Title      string `gorm:"column:title"`
		PhraseText string `gorm:"column:phrase_text"`
	}
	tx := ds.Db.WithContext(ctx).
		Raw(`SELECT title, phrase_text FROM xf_phrase WHERE title LIKE ?`, fam.prefix+"%").
		Scan(&rows)
	if tx.Error != nil {
		return nil, tx.Error
	}
	out := map[uint32]string{}
	for _, r := range rows {
		idStr := r.Title[len(fam.prefix):]
		parsed, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			continue
		}
		out[uint32(parsed)] = r.PhraseText
	}
	fam.warnIfEmpty(out)
	return out, nil
}

func (ds *Mysql) ListTickets(ctx context.Context, rc TicketReferenceCache, f *ListTicketsFilter) ([]*proto.Ticket, string, bool, error) {
	perPage := f.PerPage
	if perPage == 0 || perPage > 100 {
		if perPage == 0 {
			perPage = 50
		} else {
			perPage = 100
		}
	}

	q := ds.Db.WithContext(ctx).
		Model(&xenforo.Ticket{}).
		Preload("Participants").
		Preload("FieldValues")

	// Visibility default: only visible discussion_state unless include_hidden.
	if !f.IncludeHidden {
		q = q.Where("discussion_state = ?", "visible")
	}

	// Category filter with subtree expansion (default expands; opt-out via ExcludeSubcategories).
	if len(f.CategoryIDs) > 0 {
		ids := f.CategoryIDs
		if !f.ExcludeSubcategories {
			ids = rc.ExpandSubtree(ids)
		}
		q = q.Where("ticket_category_id IN ?", ids)
	}
	if len(f.TicketStates) > 0 {
		q = q.Where("ticket_state IN ?", f.TicketStates)
	}
	if len(f.StatusIDs) > 0 {
		q = q.Where("status_id IN ?", f.StatusIDs)
	}
	if len(f.PrefixIDs) > 0 {
		q = q.Where("prefix_id IN ?", f.PrefixIDs)
	}
	if len(f.AssignedUserIDs) > 0 {
		q = q.Where("assigned_user_id IN ?", f.AssignedUserIDs)
	}
	if len(f.StarterUserIDs) > 0 {
		q = q.Where("starter_user_id IN ?", f.StarterUserIDs)
	}
	if f.ModifiedSince > 0 {
		q = q.Where("last_modified_date >= ?", f.ModifiedSince)
	}

	if f.AfterCursor != "" {
		ts, id, err := decodeCursor(f.AfterCursor)
		if err != nil {
			return nil, "", false, err
		}
		// Tuple comparison for stable cursor under non-unique sort key.
		q = q.Where("(last_modified_date < ?) OR (last_modified_date = ? AND ticket_id < ?)", ts, ts, id)
	}

	// Fetch perPage+1 so we know whether there's another page without a count query.
	var rows []xenforo.Ticket
	q = q.Order("last_modified_date DESC, ticket_id DESC").Limit(int(perPage) + 1)
	tx := q.Find(&rows)
	if tx.Error != nil {
		return nil, "", false, tx.Error
	}

	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}

	out := make([]*proto.Ticket, 0, len(rows))
	for i := range rows {
		out = append(out, generateTicketProto(&rows[i], rc, ds.forumBaseURL()))
	}

	var nextCursor string
	if hasMore && len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = encodeCursor(last.LastModifiedDate, last.TicketID)
	}
	return out, nextCursor, hasMore, nil
}

// generateTicketProto maps a xenforo.Ticket to proto.Ticket, resolving
// reference-cached names and assembling the custom_fields map.
func generateTicketProto(t *xenforo.Ticket, rc TicketReferenceCache, forumBase string) *proto.Ticket {
	out := &proto.Ticket{
		TicketId:            t.TicketID,
		TicketRef:           t.TicketRef,
		Title:               t.Title,
		CategoryId:          t.TicketCategoryID,
		CategoryAncestorIds: rc.CategoryAncestors(t.TicketCategoryID),
		TicketState:         t.TicketState,
		StatusId:            t.StatusID,
		StatusName:          rc.StatusName(t.StatusID),
		PriorityId:          t.Priority,
		PriorityName:        rc.PriorityName(t.Priority),
		PrefixId:            t.PrefixID,
		PrefixName:          rc.PrefixName(t.PrefixID),
		DiscussionState:     t.DiscussionState,
		TicketLocked:        t.TicketLocked != 0,
		StarterUserId:       t.StarterUserID,
		StarterUsername:     t.StarterUsername,
		AssignedUserId:      t.AssignedUserID,
		AssignedUsername:    t.AssignedUsername,
		StartDate:           t.StartDate,
		LastMessageDate:     t.LastMessageDate,
		LastMessageUserId:   t.LastMessageUserID,
		LastMessageUsername: t.LastMessageUsername,
		LastModifiedDate:    t.LastModifiedDate,
		ReplyCount:          t.ReplyCount,
		TotalMessageCount:   t.ReplyCount + 1,
		CustomFields:        map[string]string{},
	}
	if cat := rc.Category(t.TicketCategoryID); cat != nil {
		out.CategoryTitle = cat.Title
	}
	for _, fv := range t.FieldValues {
		out.CustomFields[fv.FieldID] = fv.FieldValue
	}
	for _, p := range t.Participants {
		out.Participants = append(out.Participants, &proto.TicketParticipant{
			UserId: p.UserID, LastReadDate: p.LastReadDate,
		})
	}
	if forumBase != "" {
		out.ForumUrl = forumBase + "/tickets/" + t.TicketRef + "/"
	}
	return out
}

// forumBaseURL returns the configured FORUM_BASE_URL, trimmed of trailing slashes.
// Reads via viper to match the rest of the codebase's config style.
func (ds *Mysql) forumBaseURL() string {
	v := viper.GetString("FORUM_BASE_URL")
	for len(v) > 0 && v[len(v)-1] == '/' {
		v = v[:len(v)-1]
	}
	return v
}

// encodeCursor packs (last_modified_date, ticket_id) into a single opaque
// base64 string. Callers must not parse it.
func encodeCursor(lastModified, ticketID uint32) string {
	raw := fmt.Sprintf("%d:%d", lastModified, ticketID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(c string) (uint32, uint32, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: expected ts:id, got %d parts", ErrInvalidCursor, len(parts))
	}
	ts, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	id, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	return uint32(ts), uint32(id), nil
}

// encodeMessageCursor encodes the next message position to include into an
// opaque base64 cursor. Position alone is sufficient because XF's
// xf_nf_tickets_message table guarantees (ticket_id, position) is unique per
// the message_id_position index. The encoded value is the position of the
// next message to return on the next page, NOT the last position returned —
// this lets empty-cursor map to "include position 0" naturally.
func encodeMessageCursor(position uint32) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", position)))
}

// decodeMessageCursor decodes an opaque message cursor back to a position.
// Empty string is a valid input meaning "start from the beginning" (returns
// 0, which combined with the SQL boundary `position >= 0` includes position 0).
// Any other malformed input returns an error wrapping ErrInvalidCursor.
func decodeMessageCursor(c string) (uint32, error) {
	if c == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	pos, err := strconv.ParseUint(string(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	return uint32(pos), nil
}

func (ds *Mysql) GetTicket(ctx context.Context, rc TicketReferenceCache, ticketID uint32, forumBase string) (*proto.Ticket, error) {
	var row xenforo.Ticket
	tx := ds.Db.WithContext(ctx).
		Preload("Participants").
		Preload("FieldValues").
		Where("ticket_id = ?", ticketID).
		First(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	base := forumBase
	if base == "" {
		base = ds.forumBaseURL()
	}
	return generateTicketProto(&row, rc, strings.TrimRight(base, "/")), nil
}

func (ds *Mysql) GetTicketFirstMessages(ctx context.Context, ticketID uint32, n int, includeHidden bool) ([]*proto.Message, uint32, error) {
	var total int64
	q := ds.Db.WithContext(ctx).Model(&xenforo.TicketMessage{}).Where("ticket_id = ?", ticketID)
	if !includeHidden {
		q = q.Where("message_state = ?", "visible")
	}
	if tx := q.Count(&total); tx.Error != nil {
		return nil, 0, tx.Error
	}

	var rows []xenforo.TicketMessage
	q2 := ds.Db.WithContext(ctx).Where("ticket_id = ?", ticketID)
	if !includeHidden {
		q2 = q2.Where("message_state = ?", "visible")
	}
	tx := q2.Order("position ASC").Limit(n).Find(&rows)
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	out := make([]*proto.Message, 0, len(rows))
	for i := range rows {
		out = append(out, messageToProto(&rows[i]))
	}
	return out, uint32(total), nil
}

func (ds *Mysql) ListTicketMessages(ctx context.Context, ticketID uint32, afterCursor string, perPage uint32, includeHidden bool) ([]*proto.Message, string, bool, error) {
	if perPage == 0 || perPage > 100 {
		if perPage == 0 {
			perPage = 50
		} else {
			perPage = 100
		}
	}
	afterPos, err := decodeMessageCursor(afterCursor)
	if err != nil {
		return nil, "", false, err
	}
	q := ds.Db.WithContext(ctx).Where("ticket_id = ? AND position >= ?", ticketID, afterPos)
	if !includeHidden {
		q = q.Where("message_state = ?", "visible")
	}
	var rows []xenforo.TicketMessage
	tx := q.Order("position ASC").Limit(int(perPage) + 1).Find(&rows)
	if tx.Error != nil {
		return nil, "", false, tx.Error
	}
	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}
	out := make([]*proto.Message, 0, len(rows))
	for i := range rows {
		out = append(out, messageToProto(&rows[i]))
	}
	var next string
	if hasMore && len(rows) > 0 {
		next = encodeMessageCursor(rows[len(rows)-1].Position + 1)
	}
	return out, next, hasMore, nil
}

func (ds *Mysql) ListCategories(ctx context.Context, rc TicketReferenceCache) ([]*proto.Category, error) {
	tree := rc.CategoryTree()
	out := make([]*proto.Category, 0, len(tree))
	for _, c := range tree {
		out = append(out, &proto.Category{
			CategoryId:       c.ID,
			Title:            c.Title,
			Description:      c.Description,
			ParentCategoryId: c.ParentID,
			Depth:            c.Depth,
			DisplayOrder:     c.DisplayOrder,
			TicketCount:      c.TicketCount,
		})
	}
	return out, nil
}

func messageToProto(m *xenforo.TicketMessage) *proto.Message {
	return &proto.Message{
		MessageId:    m.MessageID,
		TicketId:     m.TicketID,
		UserId:       m.UserID,
		Username:     m.Username,
		MessageDate:  m.MessageDate,
		Message:      m.Message,
		MessageState: m.MessageState,
		Position:     m.Position,
		AttachCount:  m.AttachCount,
		LastEditDate: m.LastEditDate,
		EditCount:    m.EditCount,
	}
}

func (ds *Mysql) GetTicketByRef(ctx context.Context, rc TicketReferenceCache, ref string, forumBase string) (*proto.Ticket, error) {
	var row xenforo.Ticket
	tx := ds.Db.WithContext(ctx).
		Preload("Participants").
		Preload("FieldValues").
		Where("ticket_ref = ?", ref).
		First(&row)
	if tx.Error != nil {
		return nil, tx.Error
	}
	base := forumBase
	if base == "" {
		base = ds.forumBaseURL()
	}
	return generateTicketProto(&row, rc, strings.TrimRight(base, "/")), nil
}

func (ds *Mysql) LoadCategories(ctx context.Context) ([]*referencecache.CategoryRecord, error) {
	var rows []struct {
		ID           uint32 `gorm:"column:ticket_category_id"`
		Title        string `gorm:"column:title"`
		Description  string `gorm:"column:description"`
		ParentID     uint32 `gorm:"column:parent_category_id"`
		Depth        uint32 `gorm:"column:depth"`
		DisplayOrder uint32 `gorm:"column:display_order"`
		TicketCount  uint32 `gorm:"column:ticket_count"`
		Lft          uint32 `gorm:"column:lft"`
		Rgt          uint32 `gorm:"column:rgt"`
	}
	tx := ds.Db.WithContext(ctx).
		Raw(`SELECT ticket_category_id, title, description, parent_category_id,
		            depth, display_order, ticket_count, lft, rgt
		     FROM xf_nf_tickets_category
		     ORDER BY lft`).
		Scan(&rows)
	if tx.Error != nil {
		return nil, tx.Error
	}
	out := make([]*referencecache.CategoryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, &referencecache.CategoryRecord{
			ID: r.ID, Title: r.Title, Description: r.Description,
			ParentID: r.ParentID, Depth: r.Depth, DisplayOrder: r.DisplayOrder,
			TicketCount: r.TicketCount, Lft: r.Lft, Rgt: r.Rgt,
		})
	}
	return out, nil
}
