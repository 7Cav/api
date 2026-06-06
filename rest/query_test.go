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

// Same key repeated on a scalar field is the old gateway's deterministic
// too-many-values 400 (runtime checks len(values) > 1 per form key BEFORE
// parsing) — never a silent last-wins bind. The error quotes the offending
// key's values, comma-joined.
func TestQueryBinder_RepeatedScalarKeyIsTooManyValues(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "per_page=1&per_page=2"))
	assert.Zero(t, b.uint32Field("per_page"))
	require.Error(t, b.err)
	assert.Equal(t, `too many values for field "per_page": 1, 2`, b.err.Error())

	b = newQueryBinder(mustQuery(t, "after_cursor=a&after_cursor=b"))
	assert.Empty(t, b.stringField("after_cursor"))
	require.Error(t, b.err)
	assert.Equal(t, `too many values for field "after_cursor": a, b`, b.err.Error())
}

// Both spellings present on a scalar: EVERY value parses (the old gateway
// processed each form key; the bad key always errored — a deterministic 400
// whichever map order it iterated in).
func TestQueryBinder_DualSpellingParsesEveryValue(t *testing.T) {
	for _, raw := range []string{"per_page=abc&perPage=5", "perPage=5&per_page=abc"} {
		b := newQueryBinder(mustQuery(t, raw))
		assert.Zero(t, b.uint32Field("per_page"), raw)
		require.Error(t, b.err, raw)
		assert.Equal(t,
			`parsing field "per_page": strconv.ParseUint: parsing "abc": invalid syntax`,
			b.err.Error(), raw)
	}
}

// Both spellings valid on a scalar: the camel value wins, in EITHER URL
// order — a documented RULING (cross-branch, converged with #126) standing
// in for the old gateway's map-order nondeterminism (the winner genuinely
// flip-flopped run to run), NOT parity. The reversed-order case is what
// distinguishes camel-wins from URL-order-last-wins.
func TestQueryBinder_ScalarCamelSpellingWinsBothOrders(t *testing.T) {
	for _, raw := range []string{"per_page=1&perPage=2", "perPage=2&per_page=1"} {
		b := newQueryBinder(mustQuery(t, raw))
		assert.Equal(t, uint32(2), b.uint32Field("per_page"), raw)
		require.NoError(t, b.err, raw)
	}
	for _, raw := range []string{"include_hidden=1&includeHidden=0", "includeHidden=0&include_hidden=1"} {
		b := newQueryBinder(mustQuery(t, raw))
		assert.False(t, b.boolField("include_hidden"), raw)
		require.NoError(t, b.err, raw)
	}
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

// Repeated (list) fields wrap parse failures in the gateway's "parsing list"
// tier — the old stack 400'd these, it never silently dropped them
// (runtime.populateRepeatedField, verified against grpc-gateway v2.29.0).
func TestQueryBinder_InvalidUint32InListIsParsingList(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "status_id=abc"))
	b.uint32SliceField("status_id")
	require.Error(t, b.err)
	assert.Equal(t,
		`parsing list "status_id": strconv.ParseUint: parsing "abc": invalid syntax`,
		b.err.Error())
}

// Scalar fields wrap parse failures in the gateway's "parsing field" tier;
// the field name is always the snake_case proto name even for camel input.
func TestQueryBinder_InvalidBoolIsParsingField(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "excludeSubcategories=bogus"))
	b.boolField("exclude_subcategories")
	require.Error(t, b.err)
	assert.Equal(t,
		`parsing field "exclude_subcategories": strconv.ParseBool: parsing "bogus": invalid syntax`,
		b.err.Error())
}

func TestQueryBinder_FirstErrorWins(t *testing.T) {
	b := newQueryBinder(mustQuery(t, "per_page=abc&include_hidden=bogus"))
	b.uint32Field("per_page")
	b.boolField("include_hidden")
	require.Error(t, b.err)
	assert.Contains(t, b.err.Error(), `parsing field "per_page"`)
}

func TestSnakeToCamel(t *testing.T) {
	assert.Equal(t, "perPage", snakeToCamel("per_page"))
	assert.Equal(t, "afterCursor", snakeToCamel("after_cursor"))
	assert.Equal(t, "excludeSubcategories", snakeToCamel("exclude_subcategories"))
	assert.Equal(t, "starterUserId", snakeToCamel("starter_user_id"))
	assert.Equal(t, "ticketState", snakeToCamel("ticket_state"))
}
