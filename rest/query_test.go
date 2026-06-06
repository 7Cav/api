package rest

// Inner-loop unit tests for the query binder — the request-side leniency
// quirks the goldens pin end-to-end (dual spellings, repetition, lenient
// bools) plus the binder-level corners no golden reaches (both spellings at
// once, parse-failure text).

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	require.NoError(t, err)
	return v
}

func TestQueryBinder_Uint32BothSpellings(t *testing.T) {
	for _, raw := range []string{"per_page=7", "perPage=7"} {
		b := newQueryBinder(mustQuery(t, raw))
		got := b.uint32Field("per_page")
		require.NoError(t, b.err)
		assert.Equal(t, uint32(7), got, raw)
	}
}

func TestQueryBinder_AbsentKeysBindZeroValues(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "utterly_unknown=42"))
	assert.Equal(t, uint32(0), b.uint32Field("per_page"))
	assert.Equal(t, "", b.stringField("after_cursor"))
	assert.False(t, b.boolField("include_hidden"))
	assert.Empty(t, b.uint32SliceField("status_id"))
	assert.Empty(t, b.stringSliceField("ticket_state"))
	assert.NoError(t, b.err)
}

func TestQueryBinder_RepeatedByKeyRepetition(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "status_id=1&status_id=5"))
	assert.Equal(t, []uint32{1, 5}, b.uint32SliceField("status_id"))
	require.NoError(t, b.err)
}

func TestQueryBinder_RepeatedAcrossBothSpellings(t *testing.T) {
	// Both spellings feed the same repeated field (gateway behavior); this
	// binder orders snake values before camel ones, deterministically.
	b := newQueryBinder(mustQuery(t, "status_id=1&statusId=5"))
	assert.Equal(t, []uint32{1, 5}, b.uint32SliceField("status_id"))
	require.NoError(t, b.err)
}

func TestQueryBinder_ScalarLastValueWins(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "per_page=1&perPage=2"))
	assert.Equal(t, uint32(2), b.uint32Field("per_page"))
	require.NoError(t, b.err)
}

func TestQueryBinder_LenientBools(t *testing.T) {
	for raw, want := range map[string]bool{
		"include_hidden=1":     true,
		"include_hidden=t":     true,
		"include_hidden=TRUE":  true,
		"includeHidden=True":   true,
		"include_hidden=0":     false,
		"include_hidden=f":     false,
		"include_hidden=FALSE": false,
	} {
		b := newQueryBinder(mustQuery(t, raw))
		assert.Equal(t, want, b.boolField("include_hidden"), raw)
		assert.NoError(t, b.err, raw)
	}
}

func TestQueryBinder_InvalidUint32IsTypeMismatch(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "status_id=abc"))
	b.uint32SliceField("status_id")
	require.Error(t, b.err)
	assert.Equal(t,
		`type mismatch, parameter: status_id, error: strconv.ParseUint: parsing "abc": invalid syntax`,
		b.err.Error())
}

func TestQueryBinder_InvalidBoolIsTypeMismatch(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "excludeSubcategories=bogus"))
	b.boolField("exclude_subcategories")
	require.Error(t, b.err)
	assert.Equal(t,
		`type mismatch, parameter: exclude_subcategories, error: strconv.ParseBool: parsing "bogus": invalid syntax`,
		b.err.Error())
}

func TestQueryBinder_FirstErrorWins(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "per_page=abc&include_hidden=bogus"))
	b.uint32Field("per_page")
	b.boolField("include_hidden")
	require.Error(t, b.err)
	assert.Contains(t, b.err.Error(), "parameter: per_page")
}

func TestSnakeToCamel(t *testing.T) {
	assert.Equal(t, "perPage", snakeToCamel("per_page"))
	assert.Equal(t, "afterCursor", snakeToCamel("after_cursor"))
	assert.Equal(t, "excludeSubcategories", snakeToCamel("exclude_subcategories"))
	assert.Equal(t, "starterUserId", snakeToCamel("starter_user_id"))
	assert.Equal(t, "ticketState", snakeToCamel("ticket_state"))
}
