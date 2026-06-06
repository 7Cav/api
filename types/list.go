package types

import "encoding/json"

// List is a nil-safe JSON array: a nil List marshals as [] — never null —
// and an allocated one delegates to encoding/json byte-identically to the
// plain slice. It moves the "empty collections serialize as []" wire
// convention (see the package doc) from mapper discipline into the type
// itself: handlers keep their make() calls for capacity, but a zero-value
// struct is wire-valid without them — load-bearing from #127 on, where
// profile shapes become map values built outside the mappers.
//
// Use it for every repeated field in a wire type. It changes nothing for
// allocated slices, so adopting it on an existing type is contract-neutral
// (the golden replay suite proves it).
type List[T any] []T

// MarshalJSON emits [] for a nil list and delegates otherwise.
func (l List[T]) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]T(l))
}
