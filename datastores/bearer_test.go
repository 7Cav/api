package datastores

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseBearerToken(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		maxLen int
		want   string
	}{
		{"canonical scheme", "Bearer abc", 128, "abc"},
		{"lowercase scheme", "bearer abc", 128, "abc"},
		{"uppercase scheme", "BEARER abc", 128, "abc"},
		{"mixed-case scheme", "bEaReR abc", 128, "abc"},
		{"basic scheme rejected", "Basic abc", 128, ""},
		{"empty input", "", 128, ""},
		{"whitespace only", "   ", 128, ""},
		{"scheme with no token", "Bearer ", 128, ""},
		{"scheme with whitespace token", "Bearer    ", 128, ""},
		{"oversize token", "Bearer " + strings.Repeat("x", 200), 128, ""},
		{"max-len boundary", "Bearer " + strings.Repeat("x", 128), 128, strings.Repeat("x", 128)},
		{"one over max-len", "Bearer " + strings.Repeat("x", 129), 128, ""},
		{"outer whitespace tolerated", "  Bearer abc  ", 128, "abc"},
		// Note: whitespace is only trimmed (around the header value and
		// around the token), never collapsed — with "Bearer a  b" the
		// token's inner spaces survive as "a  b".
		{"extra spaces after scheme trimmed", "Bearer   abc", 128, "abc"},
		{"missing scheme separator", "Bearerabc", 128, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseBearerToken(c.raw, c.maxLen)
			assert.Equal(t, c.want, got)
		})
	}
}
