package contract

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func textGolden(status int, body string) *Golden {
	return &Golden{
		Case: "t", Status: status,
		Header:   map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		BodyText: &body,
	}
}

func jsonGolden(status int, body string) *Golden {
	return &Golden{
		Case: "t", Status: status,
		Header: map[string]string{"Content-Type": "application/json"},
		Body:   json.RawMessage(body),
	}
}

// fullGolden builds a golden with all request metadata populated, as RunCase
// and the committed corpus produce them.
func fullGolden() *Golden {
	g := jsonGolden(200, `{"a":1}`)
	g.Method = "GET"
	g.Path = "/api/v1/milpacs/ranks"
	g.Auth = AuthRead
	return g
}

func TestCompareGolden_RequestMetadataMismatch(t *testing.T) {
	tampers := []struct {
		field  string
		mutate func(g *Golden)
	}{
		{"case", func(g *Golden) { g.Case = "tampered" }},
		{"method", func(g *Golden) { g.Method = "POST" }},
		{"path", func(g *Golden) { g.Path = "/api/v1/tampered" }},
		{"auth", func(g *Golden) { g.Auth = AuthNoScopes }},
	}
	for _, tc := range tampers {
		t.Run(tc.field, func(t *testing.T) {
			want, got := fullGolden(), fullGolden()
			tc.mutate(got)
			diffs := CompareGolden(want, got)
			require.NotEmpty(t, diffs, "recorded request metadata must be compared, not write-only")
			assert.Contains(t, strings.Join(diffs, "\n"), tc.field)
		})
	}
}

func TestCompareGolden_NonAllowlistedRecordedHeaderIsADiff(t *testing.T) {
	want, got := fullGolden(), fullGolden()
	want.Header["X-Made-Up"] = "1"
	diffs := CompareGolden(want, got)
	require.NotEmpty(t, diffs)
	assert.Contains(t, strings.Join(diffs, "\n"), "recorded but not contract-compared")
	assert.Contains(t, strings.Join(diffs, "\n"), "X-Made-Up")
}

func TestCompareGolden_BodyAndBodyTextBothSetIsADiff(t *testing.T) {
	for _, side := range []string{"want", "got"} {
		t.Run(side, func(t *testing.T) {
			want, got := fullGolden(), fullGolden()
			text := "also text"
			if side == "want" {
				want.BodyText = &text
			} else {
				got.BodyText = &text
			}
			diffs := CompareGolden(want, got)
			require.NotEmpty(t, diffs, "a golden with both body and bodyText is malformed")
			joined := strings.Join(diffs, "\n")
			assert.Contains(t, joined, "exactly one of")
			assert.Contains(t, joined, side)
		})
	}
}

func TestCompareGolden_NeitherBodyNorBodyTextIsADiff(t *testing.T) {
	want, got := fullGolden(), fullGolden()
	want.Body, got.Body = nil, nil
	diffs := CompareGolden(want, got)
	require.NotEmpty(t, diffs, "a golden with neither body nor bodyText is malformed")
	joined := strings.Join(diffs, "\n")
	assert.Contains(t, joined, "exactly one of")
	assert.Contains(t, joined, "want")
	assert.Contains(t, joined, "got")
}

func TestCompareGolden_Equal(t *testing.T) {
	assert.Empty(t, CompareGolden(jsonGolden(200, `{"a":1}`), jsonGolden(200, `{"a": 1}`)),
		"whitespace must not matter")
}

func TestCompareGolden_StatusMismatch(t *testing.T) {
	diffs := CompareGolden(jsonGolden(200, `{"a":1}`), jsonGolden(404, `{"a":1}`))
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0], "status")
}

func TestCompareGolden_HeaderMismatch(t *testing.T) {
	a := jsonGolden(200, `{"a":1}`)
	b := jsonGolden(200, `{"a":1}`)
	b.Header["Content-Type"] = "text/plain; charset=utf-8"
	diffs := CompareGolden(a, b)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0], "Content-Type")
}

func TestCompareGolden_BodyKindMismatch(t *testing.T) {
	diffs := CompareGolden(jsonGolden(401, `{}`), textGolden(401, "Unauthorized\n"))
	require.NotEmpty(t, diffs)
	// The Content-Type header also differs; the kind mismatch must be
	// reported in addition.
	assert.Contains(t, strings.Join(diffs, "\n"), "body kind")
}

func TestCompareGolden_BodyTextMismatch(t *testing.T) {
	// The plain-text 401 tier compares verbatim, trailing newline included.
	diffs := CompareGolden(textGolden(401, "Unauthorized\n"), textGolden(401, "Unauthorized"))
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0], "bodyText")
}

func TestRunCase_AppliesTransformAndAllowlist(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "MISS") // infrastructure header — must not be recorded
		_, _ = w.Write([]byte(`{"keycloakId":"secret","userId":"3"}`))
	})
	g, raw, err := RunCase(h, Case{Name: "x", Method: "GET", Path: "/api/v1/x", Auth: AuthRead})
	require.NoError(t, err)

	assert.NotContains(t, string(g.Body), "keycloakId", "single documented transform")
	assert.Contains(t, string(g.Body), `"userId": "3"`)
	assert.Contains(t, string(raw), "keycloakId", "raw bytes stay untransformed wire truth")
	_, recorded := g.Header["X-Cache"]
	assert.False(t, recorded, "only allowlisted headers are contract")
}

func TestWWWAuthenticateAbsenceIsPinned(t *testing.T) {
	// #106 pinned the 401 tiers as having NO WWW-Authenticate. A standard
	// auth middleware in the rewrite would add one; that must be a red diff.
	// Mechanism under test: RunCase skips empty headers (the recorded golden
	// has no WWW-Authenticate key), so CompareGolden sees ""-vs-set.
	c := Case{Name: "x", Method: "GET", Path: "/api/v1/x", Auth: AuthNone}

	current := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
	want, _, err := RunCase(current, c)
	require.NoError(t, err)
	_, recorded := want.Header["WWW-Authenticate"]
	require.False(t, recorded, "absent header must not be recorded as an empty value")

	rewrite := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
	got, _, err := RunCase(rewrite, c)
	require.NoError(t, err)

	diffs := CompareGolden(want, got)
	require.NotEmpty(t, diffs, "rewrite adding WWW-Authenticate must produce a red diff")
	assert.Contains(t, strings.Join(diffs, "\n"), "WWW-Authenticate")
}

func TestRunCase_NonJSONContentTypeRecordsVerbatimText(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
	g, _, err := RunCase(h, Case{Name: "x", Method: "GET", Path: "/api/v1/x", Auth: AuthNone})
	require.NoError(t, err)
	require.NotNil(t, g.BodyText)
	assert.Equal(t, "Unauthorized\n", *g.BodyText)
	assert.Nil(t, g.Body)
}

func TestRunCase_DeclaredJSONThatDoesNotParseIsAnError(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	})
	_, _, err := RunCase(h, Case{Name: "x", Method: "GET", Path: "/api/v1/x", Auth: AuthRead})
	require.Error(t, err)
}

func TestSaveLoadGolden_RoundTripsBigNumbersExactly(t *testing.T) {
	dir := t.TempDir()
	g := jsonGolden(200, `{"big":9007199254740993,"id":"42"}`)
	g.Case = "sub/dir/case"
	require.NoError(t, SaveGolden(dir, g))

	loaded, err := LoadGolden(dir, "sub/dir/case")
	require.NoError(t, err)
	assert.Empty(t, CompareGolden(g, loaded))

	raw, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "case.golden.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "9007199254740993", "float64 round-tripping would corrupt this literal")
}
