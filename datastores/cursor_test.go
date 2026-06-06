package datastores

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pure unit tests for the opaque cursor codecs (PRD #112 "unit seam").
// The cursor semantics themselves — where a page resumes, position 0
// reachability — are pinned behaviorally on the MariaDB harness in
// tickets_harness_test.go.

func TestDecodeCursor_GarbageReturnsErrInvalidCursor(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"not base64", "garbage-not-base64"},
		{"base64 but not ts:id", base64URL("nope")},
		{"only one int", base64URL("1234")},
		{"empty string after base64 decode", base64URL("")},
		{"trailing garbage on ts", base64URL("100abc:5")},
		{"trailing garbage on id", base64URL("100:5junk")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := decodeCursor(c.in)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidCursor),
				"want errors.Is(err, ErrInvalidCursor); got %v", err)
		})
	}
}

func TestDecodeCursor_ValidRoundTrip(t *testing.T) {
	enc := encodeCursor(1736294298, 7499)
	ts, id, err := decodeCursor(enc)
	require.NoError(t, err)
	assert.Equal(t, uint32(1736294298), ts)
	assert.Equal(t, uint32(7499), id)
}

// base64URL builds near-miss garbage via the same encoding real cursors
// use, so decode failures exercise the payload parsing, not the base64
// layer.
func base64URL(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestEncodeDecodeMessageCursor_RoundTrip(t *testing.T) {
	enc := encodeMessageCursor(42)
	pos, err := decodeMessageCursor(enc)
	require.NoError(t, err)
	assert.Equal(t, uint32(42), pos)
}

func TestDecodeMessageCursor_EmptyMeansBeginning(t *testing.T) {
	pos, err := decodeMessageCursor("")
	require.NoError(t, err)
	assert.Equal(t, uint32(0), pos)
}

func TestDecodeMessageCursor_GarbageReturnsErrInvalidCursor(t *testing.T) {
	// Note: base64URL("") == "" which is the documented "start from beginning"
	// sentinel (see TestDecodeMessageCursor_EmptyMeansBeginning), so it is
	// intentionally NOT in the garbage list here.
	cases := []string{
		"garbage-not-base64",
		base64URL("notanumber"),
		base64URL("42abc"),
		base64URL("  10"),
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := decodeMessageCursor(in)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidCursor),
				"want errors.Is(err, ErrInvalidCursor); got %v", err)
		})
	}
}

func TestEncodeDecodeMessageCursor_PositionZeroRoundTrips(t *testing.T) {
	enc := encodeMessageCursor(0)
	require.NotEmpty(t, enc, "position=0 must encode to a non-empty cursor (regression: smoke ticket 6899 — empty cursor conflated with position 0)")
	pos, err := decodeMessageCursor(enc)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), pos)
}
