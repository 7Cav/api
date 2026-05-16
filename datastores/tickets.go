package datastores

import (
	"context"
	"encoding/base64"
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

// Compile-time assertion: Mysql must implement referencecache.Loader.
var _ referencecache.Loader = (*Mysql)(nil)

func (ds *Mysql) LoadStatusNames(ctx context.Context) (map[uint32]string, error) {
	return ds.loadPhraseMap(ctx, "nf_tickets_ticket_status.")
}
func (ds *Mysql) LoadPriorityNames(ctx context.Context) (map[uint32]string, error) {
	return ds.loadPhraseMap(ctx, "nf_tickets_ticket_priority.")
}
func (ds *Mysql) LoadPrefixNames(ctx context.Context) (map[uint32]string, error) {
	return ds.loadPhraseMap(ctx, "nf_tickets_ticket_prefix.")
}

// loadPhraseMap reads rows from xf_phrase whose title starts with the given
// prefix (e.g. "nf_tickets_ticket_status."), parses the trailing integer id,
// and returns id -> phrase_text. Rows where the trailing part isn't a
// uint32 are skipped (not an error — the phrase table is shared, so
// unrelated rows can be in scope of the LIKE pattern at the edges).
func (ds *Mysql) loadPhraseMap(ctx context.Context, prefix string) (map[uint32]string, error) {
	var rows []struct {
		Title      string `gorm:"column:title"`
		PhraseText string `gorm:"column:phrase_text"`
	}
	tx := ds.Db.WithContext(ctx).
		Raw(`SELECT title, phrase_text FROM xf_phrase WHERE title LIKE ?`, prefix+"%").
		Scan(&rows)
	if tx.Error != nil {
		return nil, tx.Error
	}
	out := map[uint32]string{}
	for _, r := range rows {
		idStr := r.Title[len(prefix):]
		parsed, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			continue
		}
		out[uint32(parsed)] = r.PhraseText
	}
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
			return nil, "", false, fmt.Errorf("invalid cursor: %w", err)
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
		return 0, 0, err
	}
	var ts, id uint32
	if _, err := fmt.Sscanf(string(raw), "%d:%d", &ts, &id); err != nil {
		return 0, 0, err
	}
	return ts, id, nil
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

func (ds *Mysql) ListTicketMessages(ctx context.Context, ticketID, afterPosition, perPage uint32, includeHidden bool) ([]*proto.Message, uint32, bool, error) {
	if perPage == 0 || perPage > 100 {
		if perPage == 0 {
			perPage = 50
		} else {
			perPage = 100
		}
	}
	q := ds.Db.WithContext(ctx).Where("ticket_id = ? AND position > ?", ticketID, afterPosition)
	if !includeHidden {
		q = q.Where("message_state = ?", "visible")
	}
	var rows []xenforo.TicketMessage
	tx := q.Order("position ASC").Limit(int(perPage) + 1).Find(&rows)
	if tx.Error != nil {
		return nil, 0, false, tx.Error
	}
	hasMore := len(rows) > int(perPage)
	if hasMore {
		rows = rows[:perPage]
	}
	out := make([]*proto.Message, 0, len(rows))
	for i := range rows {
		out = append(out, messageToProto(&rows[i]))
	}
	var next uint32
	if hasMore && len(rows) > 0 {
		next = rows[len(rows)-1].Position
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
