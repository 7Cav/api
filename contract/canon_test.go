package contract

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalize_ParsesAndPreservesNumberForms(t *testing.T) {
	// protojson emits 64-bit ints as strings and 32-bit ints as numbers.
	// Canonicalization must keep each form exactly as the wire produced it:
	// "3" must stay a string, 42 must stay the literal 42.
	v, err := Canonicalize([]byte(`{"userId":"3", "ticketId":42, "big":9007199254740993}`))
	require.NoError(t, err)

	out := string(MarshalCanonical(v))
	assert.Contains(t, out, `"userId": "3"`)
	assert.Contains(t, out, `"ticketId": 42`)
	// Numbers beyond float64 precision must round-trip verbatim.
	assert.Contains(t, out, `"big": 9007199254740993`)
}

func TestCanonicalize_RejectsNonJSON(t *testing.T) {
	_, err := Canonicalize([]byte("Unauthorized\n"))
	require.Error(t, err)
}

func TestCanonicalize_RejectsTrailingGarbage(t *testing.T) {
	_, err := Canonicalize([]byte(`{"a":1} trailing`))
	require.Error(t, err)
}

func TestMarshalCanonical_SortsKeysAndIsWhitespaceStable(t *testing.T) {
	a, err := Canonicalize([]byte(`{"b":1,"a":{"d":[1,2],"c":"x"}}`))
	require.NoError(t, err)
	b, err := Canonicalize([]byte("{\n  \"a\": {\"c\":\"x\", \"d\":[1, 2]},\n  \"b\": 1\n}"))
	require.NoError(t, err)

	assert.Equal(t, string(MarshalCanonical(a)), string(MarshalCanonical(b)),
		"same semantic document must canonicalize to identical bytes")
}

func TestMarshalCanonical_DoesNotEscapeHTML(t *testing.T) {
	v, err := Canonicalize([]byte(`{"url":"https://7cav.us/a?x=1&y=2"}`))
	require.NoError(t, err)
	assert.Contains(t, string(MarshalCanonical(v)), "x=1&y=2",
		"URLs must stay readable; no \\u0026 escaping")
}

func TestStripKeycloakID_RemovesKeyAtAnyDepth(t *testing.T) {
	// The single documented transform: profile shapes lose keycloakId at
	// cutover, so goldens are recorded without it. Nothing else is touched.
	v, err := Canonicalize([]byte(`{
		"keycloakId": "top",
		"user": {"keycloakId": "nested", "userId": "3"},
		"profiles": {"1": {"keycloakId": "deep", "realName": "Adam"}},
		"list": [{"keycloakId": "in-array", "keep": true}]
	}`))
	require.NoError(t, err)

	out := string(MarshalCanonical(StripKeycloakID(v)))
	assert.NotContains(t, out, "keycloakId")
	assert.Contains(t, out, `"userId": "3"`)
	assert.Contains(t, out, `"realName": "Adam"`)
	assert.Contains(t, out, `"keep": true`)
}

func TestStripKeycloakID_LeavesValuesNamedKeycloakAlone(t *testing.T) {
	v, err := Canonicalize([]byte(`{"note":"keycloakId lives in values"}`))
	require.NoError(t, err)
	out := string(MarshalCanonical(StripKeycloakID(v)))
	assert.Contains(t, out, "keycloakId lives in values")
}

func TestDiff_EqualDocumentsRegardlessOfFormatting(t *testing.T) {
	a, _ := Canonicalize([]byte(`{"x":[1,2,3],"y":{"z":"s"}}`))
	b, _ := Canonicalize([]byte("{\"y\": {\"z\": \"s\"}, \"x\": [1, 2, 3]}"))
	assert.Empty(t, Diff(a, b))
}

func TestDiff_ReportsPathOfMismatch(t *testing.T) {
	a, _ := Canonicalize([]byte(`{"profiles":{"1":{"user":{"userId":"3"}}}}`))
	b, _ := Canonicalize([]byte(`{"profiles":{"1":{"user":{"userId":"4"}}}}`))
	diffs := Diff(a, b)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0], "profiles.1.user.userId")
	assert.Contains(t, diffs[0], `"3"`)
	assert.Contains(t, diffs[0], `"4"`)
}

func TestDiff_NumberFormMatters(t *testing.T) {
	// "3" (string) vs 3 (number) is a contract break: protojson's 64-bit
	// string form is part of the wire contract.
	a, _ := Canonicalize([]byte(`{"id":"3"}`))
	b, _ := Canonicalize([]byte(`{"id":3}`))
	assert.NotEmpty(t, Diff(a, b))
}

func TestDiff_MissingAndExtraKeys(t *testing.T) {
	a, _ := Canonicalize([]byte(`{"a":1,"b":2}`))
	b, _ := Canonicalize([]byte(`{"a":1,"c":3}`))
	diffs := Diff(a, b)
	assert.Len(t, diffs, 2)
}

func TestDiff_ArrayLengthAndOrder(t *testing.T) {
	a, _ := Canonicalize([]byte(`{"r":[1,2]}`))
	b, _ := Canonicalize([]byte(`{"r":[2,1]}`))
	assert.NotEmpty(t, Diff(a, b), "array order is part of the contract")

	c, _ := Canonicalize([]byte(`{"r":[1]}`))
	assert.NotEmpty(t, Diff(a, c))
}

func TestDiff_NullVersusAbsentVersusEmpty(t *testing.T) {
	// Emit-everything semantics: null, {}, and absent are three distinct
	// contract states.
	null1, _ := Canonicalize([]byte(`{"primary":null}`))
	empty, _ := Canonicalize([]byte(`{"primary":{}}`))
	absent, _ := Canonicalize([]byte(`{}`))

	assert.NotEmpty(t, Diff(null1, empty))
	assert.NotEmpty(t, Diff(null1, absent))
	assert.NotEmpty(t, Diff(empty, absent))

	null2, _ := Canonicalize([]byte(`{"primary": null}`))
	assert.Empty(t, Diff(null1, null2))
}
