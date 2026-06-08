package datastores

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pure unit tests for the zero-row guard on phrase-map families (#195).
// The DB read itself (loadPhraseMap against a real xf_phrase) is pinned
// end-to-end on the MariaDB harness in tickets_harness_test.go; here we
// pin the decision: a *must-be-populated* family that comes back empty is
// surfaced loudly (Warn, naming the family), while the legitimately-sparse
// prefix family stays silent and the healthy populated path never warns.

// captureWarn swaps datastores.Warn's output to a buffer for the duration
// of fn, restoring it afterwards, and returns what was written.
func captureWarn(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := Warn.Writer()
	prevFlags := Warn.Flags()
	prevPrefix := Warn.Prefix()
	Warn.SetOutput(&buf)
	Warn.SetFlags(0)
	t.Cleanup(func() {
		Warn.SetOutput(prevOut)
		Warn.SetFlags(prevFlags)
		Warn.SetPrefix(prevPrefix)
	})
	fn()
	return buf.String()
}

func TestPhraseFamily_EmptyMustBePopulatedFamilyWarnsNamingFamily(t *testing.T) {
	cases := []struct {
		fam      phraseFamily
		wantName string
	}{
		{familyStatus, "status"},
		{familyPriority, "priority"},
	}
	for _, c := range cases {
		t.Run(c.wantName, func(t *testing.T) {
			out := captureWarn(t, func() {
				c.fam.warnIfEmpty(map[uint32]string{})
			})
			require.NotEmpty(t, out, "an empty must-be-populated family must surface a warning, got silence")
			assert.Contains(t, strings.ToLower(out), c.wantName,
				"the warning must name the %q family so the drift is diagnosable", c.wantName)
		})
	}
}

func TestPhraseFamily_EmptyPrefixFamilyIsSilent(t *testing.T) {
	out := captureWarn(t, func() {
		familyPrefix.warnIfEmpty(map[uint32]string{})
	})
	assert.Empty(t, out,
		"the prefix family may be legitimately sparse — an empty read must not warn")
}

func TestPhraseFamily_PopulatedFamilyNeverWarns(t *testing.T) {
	// A populated map is the healthy path — no warning regardless of which
	// family, and individual missing ids inside it are NOT this guard's
	// concern (they intentionally resolve to "" via the cache).
	for _, fam := range []phraseFamily{familyStatus, familyPriority, familyPrefix} {
		out := captureWarn(t, func() {
			fam.warnIfEmpty(map[uint32]string{1: "Open"})
		})
		assert.Empty(t, out,
			"a family that returned rows must behave exactly as today (no warning)")
	}
}
