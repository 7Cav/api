package rest

import (
	"net/url"
	"testing"
)

// FuzzQueryBinder drives the query binder the way handlers do — bind a
// representative spread of field types, then read Err — against arbitrary
// query strings. It pins two invariants the lenient-binding surface (PRD #112:
// name-or-number, repeated-by-repetition, snake/camel dual spelling, bracket
// folds) must never violate, whatever the input:
//
//   - no accessor panics (the binder digests any url.Values, including
//     bracket keys, mismatched spellings, and pathological repetition);
//   - first-failure-wins is monotone: once Err() is non-nil it stays the SAME
//     error for the rest of the bind. The handler protocol (bind everything,
//     check Err once) is only safe if a later successful read can't clear an
//     earlier failure — this is the property that makes a forgotten field read
//     harmless rather than a silently-dropped 400.
//
// Field names are chosen to exercise every code path: a multi-word name
// (snakeToCamel + camel-spelling fold), scalar vs slice, and all three parsed
// types (uint32, bool, string). Run: go test ./rest -run x -fuzz FuzzQueryBinder
func FuzzQueryBinder(f *testing.F) {
	for _, seed := range []string{
		"",
		"status_id=1&status_id=5",          // repeated scalar → too-many-values
		"status_id=1&statusId=5",           // both spellings → camel wins
		"per_page=notanumber",              // uint32 parse failure
		"include_hidden=maybe",             // bool parse failure
		"status_id[0]=5",                   // bracket fold
		"status_id[x]=5",                   // non-numeric bracket content
		"per_page=1&per_page=2&per_page=3", // pathological repetition
		"ticket_state=open&ticket_state=closed",
		"a[b][c]=1", // greedy bracket fold to unknown base
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		values, err := url.ParseQuery(raw)
		if err != nil {
			return // malformed escaping is stdlib's contract, not the binder's
		}
		b := newQueryBinder(values)

		// Bind a spread that hits scalar/slice × uint32/bool/string and a
		// multi-word name (camel-fold path). After each read, the first error
		// must never change once set.
		var firstErr error
		check := func() {
			if got := b.Err(); got != nil {
				if firstErr == nil {
					firstErr = got
				} else if got != firstErr {
					t.Fatalf("Err() mutated after first failure: was %v, now %v", firstErr, got)
				}
			} else if firstErr != nil {
				t.Fatalf("Err() reverted to nil after failure %v", firstErr)
			}
		}

		b.uint32Field("per_page")
		check()
		b.uint32SliceField("status_id")
		check()
		b.boolField("include_hidden")
		check()
		b.stringField("ticket_state")
		check()
		b.stringSliceField("ticket_state")
		check()
	})
}
