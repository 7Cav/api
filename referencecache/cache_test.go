package referencecache

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeLoader struct {
	mu         sync.Mutex
	calls      int
	statuses   map[uint32]string
	priorities map[uint32]string
	prefixes   map[uint32]string
	categories []*CategoryRecord
	err        error
}

func (f *fakeLoader) LoadStatusNames(_ context.Context) (map[uint32]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.statuses, f.err
}
func (f *fakeLoader) LoadPriorityNames(_ context.Context) (map[uint32]string, error) {
	return f.priorities, f.err
}
func (f *fakeLoader) LoadPrefixNames(_ context.Context) (map[uint32]string, error) {
	return f.prefixes, f.err
}
func (f *fakeLoader) LoadCategories(_ context.Context) ([]*CategoryRecord, error) {
	return f.categories, f.err
}

func TestRefresh_PopulatesAllMaps(t *testing.T) {
	loader := &fakeLoader{
		statuses:   map[uint32]string{1: "Open", 3: "Resolved"},
		priorities: map[uint32]string{1: "Low", 4: "Critical"},
		prefixes:   map[uint32]string{1: "[Bug]"},
		categories: []*CategoryRecord{
			{ID: 5, Title: "S1 Personnel Admin", ParentID: 0, Depth: 0, Lft: 1, Rgt: 12},
			{ID: 17, Title: "S1 Citations", ParentID: 5, Depth: 1, Lft: 2, Rgt: 3},
		},
	}
	c := New(loader)
	err := c.Refresh(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, "Open", c.StatusName(1))
	assert.Equal(t, "Resolved", c.StatusName(3))
	assert.Equal(t, "", c.StatusName(99), "unknown id returns empty")
	assert.Equal(t, "Critical", c.PriorityName(4))
	assert.Equal(t, "[Bug]", c.PrefixName(1))
	assert.Equal(t, "S1 Citations", c.Category(17).Title)
	assert.Equal(t, []uint32{5}, c.CategoryAncestors(17))
	assert.Nil(t, c.CategoryAncestors(99), "unknown id returns nil")
}

func TestExpandCategorySubtree(t *testing.T) {
	loader := &fakeLoader{
		categories: []*CategoryRecord{
			{ID: 5, ParentID: 0, Lft: 1, Rgt: 12, Depth: 0},
			{ID: 17, ParentID: 5, Lft: 2, Rgt: 3, Depth: 1},
			{ID: 16, ParentID: 5, Lft: 4, Rgt: 5, Depth: 1},
			{ID: 19, ParentID: 0, Lft: 13, Rgt: 16, Depth: 0},
		},
	}
	c := New(loader)
	_ = c.Refresh(context.Background())

	got := c.ExpandSubtree([]uint32{5})
	assert.ElementsMatch(t, []uint32{5, 17, 16}, got)
	got = c.ExpandSubtree([]uint32{5, 19})
	assert.ElementsMatch(t, []uint32{5, 17, 16, 19}, got)
	got = c.ExpandSubtree([]uint32{99})
	assert.Equal(t, []uint32{99}, got, "unknown id passes through unchanged")
}

func TestRefresh_FailurePreservesPreviousData(t *testing.T) {
	loader := &fakeLoader{statuses: map[uint32]string{1: "Open"}}
	c := New(loader)
	_ = c.Refresh(context.Background())
	assert.Equal(t, "Open", c.StatusName(1))

	loader.err = assert.AnError
	err := c.Refresh(context.Background())
	assert.Error(t, err)
	assert.Equal(t, "Open", c.StatusName(1), "failed refresh must keep old data, not blank it")
}
