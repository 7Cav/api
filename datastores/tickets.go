package datastores

import (
	"context"
	"fmt"

	"github.com/7cav/api/referencecache"
)

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
		var id uint32
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			continue
		}
		out[id] = r.PhraseText
	}
	return out, nil
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
