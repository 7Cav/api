package contract

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Re-record the corpus from the current stack (only possible while the old
// stack remains in-tree):
//
//	go test ./contract -run TestContractCorpus -update
//
// Review the goldens diff like any other code change before committing.
var update = flag.Bool("update", false, "re-record contract goldens from the current stack")

const goldensDir = "goldens"

// TestContractCorpus replays the full battery against the in-process current
// stack and compares each response semantically (parse → canonicalize → diff)
// against the committed golden. Status code and allowlisted headers are part
// of every comparison. Byte-level output is informational only.
func TestContractCorpus(t *testing.T) {
	h := currentStack(t)

	for _, c := range Cases() {
		t.Run(c.Name, func(t *testing.T) {
			got, raw, err := RunCase(h, c)
			require.NoError(t, err)

			if *update {
				require.NoError(t, SaveGolden(goldensDir, got))
				return
			}

			want, err := LoadGolden(goldensDir, c.Name)
			require.NoError(t, err,
				"missing golden for %s — re-record with: go test ./contract -run TestContractCorpus -update", c.Name)

			diffs := CompareGolden(want, got)
			if len(diffs) > 0 {
				for _, d := range diffs {
					t.Error(d)
				}
				t.Logf("informational raw response (%d bytes, NOT the basis of comparison):\n%s",
					len(raw), truncateForLog(raw))
				return
			}

			// Informational byte-diff: semantic equality is the contract;
			// canonical-byte drift (e.g. a hand-edited golden) is only noted.
			if got.Body != nil && want.Body != nil && !bytes.Equal(normalizeRaw(t, want.Body), normalizeRaw(t, got.Body)) {
				t.Logf("informational: golden bytes differ from observed canonical bytes (semantically equal)")
			}
		})
	}
}

// normalizeRaw renders a stored golden body through the canonical encoder so
// the informational byte comparison isn't tripped by file formatting.
func normalizeRaw(t *testing.T, raw []byte) []byte {
	t.Helper()
	v, err := canonicalize(raw)
	require.NoError(t, err)
	return marshalCanonical(v)
}

func truncateForLog(b []byte) string {
	const max = 2000
	if len(b) > max {
		return string(b[:max]) + "…(truncated)"
	}
	return string(b)
}

// TestCorpusHasNoOrphanGoldens fails when a committed golden no longer has a
// battery case — stale goldens would silently stop guarding anything.
func TestCorpusHasNoOrphanGoldens(t *testing.T) {
	if *update {
		t.Skip("recording")
	}
	known := map[string]bool{}
	for _, c := range Cases() {
		known[c.Name] = true
	}
	err := filepath.WalkDir(goldensDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(goldensDir, path)
		require.NoError(t, err)
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".golden.json")
		assert.True(t, known[name], "orphan golden %s has no battery case", path)
		return nil
	})
	if os.IsNotExist(err) {
		t.Fatalf("goldens directory missing — record the corpus first")
	}
	require.NoError(t, err)
}

// TestBatteryIsWellFormed pins structural invariants of the battery itself:
// unique names, GET-only, the keycloak route deliberately absent, and at
// least one case per public route (all 17).
func TestBatteryIsWellFormed(t *testing.T) {
	cases := Cases()
	seen := map[string]bool{}
	for _, c := range cases {
		assert.False(t, seen[c.Name], "duplicate case name %s", c.Name)
		seen[c.Name] = true
		assert.Equal(t, "GET", c.Method, "%s: corpus is GET-only today", c.Name)
		assert.NotContains(t, c.Path, "/milpac/keycloak/",
			"%s: the keycloak route dies at cutover and must not be recorded", c.Name)
		assert.NotEmpty(t, c.Notes, "%s: every case documents why it exists", c.Name)
	}

	// One anchored pattern per surviving route — publicRoutes in
	// routes_test.go, the single test-side route truth shared with the
	// OpenAPI spec coverage tests.
	counts := map[string]int{}
	for _, c := range cases {
		path := c.Path
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
		for _, r := range publicRoutes {
			if r.pattern.MatchString(path) {
				counts[r.label]++
				break
			}
		}
	}
	for _, r := range publicRoutes {
		assert.Positive(t, counts[r.label],
			"no battery case covers route: %s (%s)", r.label, r.pattern)
	}
}
