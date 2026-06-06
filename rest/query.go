package rest

// The query binder: typed access to a request's query string with the
// request-side leniency the goldens freeze (PRD #112):
//
//   - every key is accepted in both snake_case and lowerCamelCase — fields
//     are declared by their snake_case name and the camel spelling is derived;
//   - repeated fields bind by key repetition (?status_id=1&status_id=5);
//     the comma-separated form is NOT supported (values pass through intact);
//   - bools parse leniently via strconv.ParseBool (1/t/TRUE/...);
//   - unknown parameters are ignored by construction — the binder only ever
//     reads the keys handlers ask for.
//
// Parse failures surface via err in the gateway's frozen query-parse wire
// text (verified against grpc-gateway v2.29.0 runtime.PopulateQueryParameters
// — the old stack 400'd these, it did NOT silently drop them):
//
//   - scalar fields:   parsing field "<snake>": <strconv error>
//   - repeated fields: parsing list "<snake>": <strconv error>
//
// The field name is always the snake_case proto name, whichever spelling the
// request used. The "type mismatch, parameter:" tier is the gateway's
// PATH-param wrapping and lives in bindTicketID only — never here. (PRD
// #112's enumerated break about invalid enum query values being silently
// dropped is real but moot on this surface: no tickets route declares an
// enum-typed query parameter.) First failure wins; later reads still return
// zero values so handlers can bind every field then check Err once.
//
// Scalar fields mirror the old gateway's per-form-key protocol exactly
// (runtime.populateFieldValueFromPath), plus one ruling:
//
//   - the same key repeated (?per_page=1&per_page=2) is the deterministic
//     too-many-values 400, quoting that key's values (parity — the check ran
//     per form key BEFORE parsing);
//   - both spellings at once: EVERY value parses; any failure is the
//     parsing-field 400 (parity — the bad key always errored, deterministic
//     either map order); all valid → the camel value wins. Camel-wins is a
//     documented RULING (cross-branch, converged with #126) standing in for
//     the old gateway's genuine map-order nondeterminism, NOT parity.
//
// Repeated fields see snake values before camel ones (same ruling tier — no
// golden pins cross-spelling order); same-key repetition binding by
// repetition is golden-pinned parity.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type queryBinder struct {
	values url.Values
	err    error
}

func newQueryBinder(values url.Values) *queryBinder {
	return &queryBinder{values: values}
}

// raw returns the values bound to the field across both spellings, snake
// first, nil when absent. Always a fresh slice: appending camel values onto
// b.values[snake] directly would write into the url.Values backing array
// when it has spare capacity (and stringSliceField hands the result to
// callers).
func (b *queryBinder) raw(snake string) []string {
	vals := b.values[snake]
	if camel := snakeToCamel(snake); camel != snake {
		if camelVals := b.values[camel]; len(camelVals) > 0 {
			return append(append([]string(nil), vals...), camelVals...)
		}
	}
	return vals
}

// scalar returns a scalar field's values in binding order (snake first,
// camel last — the camel value wins) after enforcing the old gateway's
// too-many-values check: either spelling carrying more than one value fails
// with that spelling's values quoted, before any parsing (parity). nil after
// a failure.
func (b *queryBinder) scalar(snake string) []string {
	snakeVals := b.values[snake]
	var camelVals []string
	if camel := snakeToCamel(snake); camel != snake {
		camelVals = b.values[camel]
	}
	for _, vals := range [][]string{snakeVals, camelVals} {
		if len(vals) > 1 {
			if b.err == nil {
				b.err = fmt.Errorf("too many values for field %q: %s", snake, strings.Join(vals, ", "))
			}
			return nil
		}
	}
	if len(camelVals) == 0 {
		return snakeVals
	}
	return append(append([]string(nil), snakeVals...), camelVals...)
}

// failField records the first binding failure in the gateway's scalar
// query-parse wire text (runtime.populateField).
func (b *queryBinder) failField(snake string, err error) {
	if b.err == nil {
		b.err = fmt.Errorf("parsing field %q: %v", snake, err)
	}
}

// failList records the first binding failure in the gateway's repeated-field
// query-parse wire text (runtime.populateRepeatedField).
func (b *queryBinder) failList(snake string, err error) {
	if b.err == nil {
		b.err = fmt.Errorf("parsing list %q: %v", snake, err)
	}
}

func (b *queryBinder) stringField(snake string) string {
	vals := b.scalar(snake)
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1] // camel wins (ruling, see package comment)
}

func (b *queryBinder) stringSliceField(snake string) []string {
	return b.raw(snake)
}

func (b *queryBinder) uint32Field(snake string) uint32 {
	var out uint32
	for _, raw := range b.scalar(snake) {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			b.failField(snake, err)
			return 0
		}
		out = uint32(v) // every value parses; camel (last) wins
	}
	return out
}

func (b *queryBinder) uint32SliceField(snake string) []uint32 {
	vals := b.raw(snake)
	out := make([]uint32, 0, len(vals))
	for _, raw := range vals {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			b.failList(snake, err)
			return nil
		}
		out = append(out, uint32(v))
	}
	return out
}

func (b *queryBinder) boolField(snake string) bool {
	var out bool
	for _, raw := range b.scalar(snake) {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			b.failField(snake, err)
			return false
		}
		out = v // every value parses; camel (last) wins
	}
	return out
}

// snakeToCamel derives the lowerCamelCase spelling of a snake_case query key
// (per_page → perPage), mirroring protobuf JSON-name derivation for the
// field names this API uses.
func snakeToCamel(snake string) string {
	parts := strings.Split(snake, "_")
	var sb strings.Builder
	sb.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		sb.WriteString(strings.ToUpper(p[:1]))
		sb.WriteString(p[1:])
	}
	return sb.String()
}
