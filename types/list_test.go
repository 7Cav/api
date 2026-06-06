package types_test

import (
	"encoding/json"
	"testing"

	"github.com/7cav/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A nil List marshals as [] — the allocation discipline (empty collections
// are [], never null) enforced by the TYPE, so a forgotten make() in a
// mapper can no longer leak null onto the wire.
func TestList_NilMarshalsAsEmptyArray(t *testing.T) {
	var l types.List[*types.Position]
	b, err := json.Marshal(l)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b))
}

// An allocated List delegates to encoding/json — byte-identical to the plain
// slice, so adopting the type changes nothing on the wire (the golden replay
// suite proves it end-to-end).
func TestList_AllocatedDelegatesByteIdentical(t *testing.T) {
	plain := []*types.Position{
		{PositionTitle: "S6 Web Developer", PositionId: 812},
		nil,
	}
	wantBytes, err := json.Marshal(plain)
	require.NoError(t, err)

	got, err := json.Marshal(types.List[*types.Position](plain))
	require.NoError(t, err)
	assert.Equal(t, string(wantBytes), string(got))

	empty, err := json.Marshal(types.List[*types.Position]{})
	require.NoError(t, err)
	assert.Equal(t, "[]", string(empty))
}

// A zero-value Profile is wire-valid: every collection marshals as [], not
// null — relevant from #127 on, where Profile becomes a roster map value and
// zero values can exist outside the mappers' make() discipline.
func TestProfile_ZeroValueMarshalsWireValid(t *testing.T) {
	b, err := json.Marshal(types.Profile{})
	require.NoError(t, err)

	// Unset nested messages (user, rank, primary) stay null — that part of
	// the wire convention is pointer-field behavior, untouched here.
	s := string(b)
	assert.Contains(t, s, `"secondaries":[]`)
	assert.Contains(t, s, `"records":[]`)
	assert.Contains(t, s, `"awards":[]`)
	assert.Contains(t, s, `"roster":"ROSTER_TYPE_UNSPECIFIED"`)
}

// Zero-value RanksResponse: ranks is [], not null.
func TestRanksResponse_ZeroValueMarshalsWireValid(t *testing.T) {
	b, err := json.Marshal(types.RanksResponse{})
	require.NoError(t, err)
	assert.Equal(t, `{"ranks":[]}`, string(b))
}
