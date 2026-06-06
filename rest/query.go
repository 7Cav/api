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
// Parse failures surface as the frozen "type mismatch" wire text via err —
// the enumerated cutover break (PRD #112 breaks list): the new stack answers
// 400 where the old gateway silently dropped invalid enum query values.
// First failure wins; later reads still return zero values so handlers can
// bind every field then check err once.
//
// Where both spellings appear at once (no golden pins it — the old gateway's
// map iteration made it nondeterministic): repeated fields see snake values
// before camel ones; scalars take the last value.

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
// first, nil when absent.
func (b *queryBinder) raw(snake string) []string {
	vals := b.values[snake]
	if camel := snakeToCamel(snake); camel != snake {
		vals = append(vals, b.values[camel]...)
	}
	return vals
}

// fail records the first binding failure in the frozen wire text.
func (b *queryBinder) fail(snake string, err error) {
	if b.err == nil {
		b.err = fmt.Errorf("type mismatch, parameter: %s, error: %v", snake, err)
	}
}

func (b *queryBinder) stringField(snake string) string {
	vals := b.raw(snake)
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1]
}

func (b *queryBinder) stringSliceField(snake string) []string {
	return b.raw(snake)
}

func (b *queryBinder) uint32Field(snake string) uint32 {
	vals := b.raw(snake)
	if len(vals) == 0 {
		return 0
	}
	v, err := strconv.ParseUint(vals[len(vals)-1], 10, 32)
	if err != nil {
		b.fail(snake, err)
		return 0
	}
	return uint32(v)
}

func (b *queryBinder) uint32SliceField(snake string) []uint32 {
	vals := b.raw(snake)
	out := make([]uint32, 0, len(vals))
	for _, raw := range vals {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			b.fail(snake, err)
			return nil
		}
		out = append(out, uint32(v))
	}
	return out
}

func (b *queryBinder) boolField(snake string) bool {
	vals := b.raw(snake)
	if len(vals) == 0 {
		return false
	}
	v, err := strconv.ParseBool(vals[len(vals)-1])
	if err != nil {
		b.fail(snake, err)
		return false
	}
	return v
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
