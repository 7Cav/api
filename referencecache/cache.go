// Package referencecache holds small, slow-changing lookup data
// (ticket status/priority/prefix names, category tree) in memory so the
// tickets endpoints don't pay the JOIN cost on every request.
//
// Refreshes are periodic, not write-driven. Callers tolerate cache miss as
// empty string / nil — never as error — because new addon-side records can
// land between refresh ticks.
package referencecache

import (
	"context"
	"sync"
)

// CategoryRecord mirrors the row shape we care about from
// xf_nf_tickets_category. lft/rgt support subtree expansion.
type CategoryRecord struct {
	ID           uint32
	Title        string
	Description  string
	ParentID     uint32
	Depth        uint32
	DisplayOrder uint32
	TicketCount  uint32
	Lft          uint32
	Rgt          uint32
}

// Loader fetches data the cache holds. The cache implementation calls it
// at startup and on every refresh tick. Implementations live in the
// datastore package (against MariaDB).
type Loader interface {
	LoadStatusNames(ctx context.Context) (map[uint32]string, error)
	LoadPriorityNames(ctx context.Context) (map[uint32]string, error)
	LoadPrefixNames(ctx context.Context) (map[uint32]string, error)
	LoadCategories(ctx context.Context) ([]*CategoryRecord, error)
}

// ReferenceCache is what consumers (datastore methods, handlers) call.
type ReferenceCache interface {
	StatusName(id uint32) string
	PriorityName(id uint32) string
	PrefixName(id uint32) string
	Category(id uint32) *CategoryRecord
	CategoryAncestors(id uint32) []uint32
	CategoryTree() []*CategoryRecord
	ExpandSubtree(ids []uint32) []uint32
	Refresh(ctx context.Context) error
}

// Cache is the canonical implementation.
type Cache struct {
	loader Loader

	mu         sync.RWMutex
	statuses   map[uint32]string
	priorities map[uint32]string
	prefixes   map[uint32]string
	categories map[uint32]*CategoryRecord
	tree       []*CategoryRecord
}

// New returns an unpopulated cache. Callers must run Refresh once before
// serving traffic.
func New(loader Loader) *Cache {
	return &Cache{
		loader:     loader,
		statuses:   map[uint32]string{},
		priorities: map[uint32]string{},
		prefixes:   map[uint32]string{},
		categories: map[uint32]*CategoryRecord{},
	}
}

func (c *Cache) Refresh(ctx context.Context) error {
	statuses, err := c.loader.LoadStatusNames(ctx)
	if err != nil {
		return err
	}
	priorities, err := c.loader.LoadPriorityNames(ctx)
	if err != nil {
		return err
	}
	prefixes, err := c.loader.LoadPrefixNames(ctx)
	if err != nil {
		return err
	}
	tree, err := c.loader.LoadCategories(ctx)
	if err != nil {
		return err
	}

	cats := make(map[uint32]*CategoryRecord, len(tree))
	for _, cat := range tree {
		cats[cat.ID] = cat
	}

	c.mu.Lock()
	c.statuses = statuses
	c.priorities = priorities
	c.prefixes = prefixes
	c.categories = cats
	c.tree = tree
	c.mu.Unlock()
	return nil
}

func (c *Cache) StatusName(id uint32) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.statuses[id]
}

func (c *Cache) PriorityName(id uint32) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.priorities[id]
}

func (c *Cache) PrefixName(id uint32) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.prefixes[id]
}

func (c *Cache) Category(id uint32) *CategoryRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.categories[id]
}

func (c *Cache) CategoryAncestors(id uint32) []uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cat, ok := c.categories[id]
	if !ok {
		return nil
	}
	seen := map[uint32]struct{}{id: {}}
	var chain []uint32
	parent := cat.ParentID
	for parent != 0 {
		if _, dup := seen[parent]; dup {
			break // cycle in data — stop here, return what we have
		}
		seen[parent] = struct{}{}
		chain = append([]uint32{parent}, chain...) // root-first
		p, ok := c.categories[parent]
		if !ok {
			break
		}
		parent = p.ParentID
	}
	return chain
}

func (c *Cache) CategoryTree() []*CategoryRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*CategoryRecord, len(c.tree))
	copy(out, c.tree)
	return out
}

// ExpandSubtree returns ids plus every descendant category id reachable
// through the cached tree. Unknown ids are passed through unchanged so the
// caller's SQL IN(...) clause behaves the same as if the cache were warm.
func (c *Cache) ExpandSubtree(ids []uint32) []uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := map[uint32]struct{}{}
	var out []uint32
	add := func(id uint32) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range ids {
		root, ok := c.categories[id]
		if !ok {
			add(id)
			continue
		}
		add(id)
		for _, cat := range c.tree {
			if cat.Lft > root.Lft && cat.Rgt < root.Rgt {
				add(cat.ID)
			}
		}
	}
	return out
}
