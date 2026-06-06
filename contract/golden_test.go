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
