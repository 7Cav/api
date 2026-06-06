// Package contract holds the golden contract corpus for the public HTTP API
// (PRD #112, issue #116) and the machinery that makes it executable: a
// canonicalizer, a semantic JSON differ, and the recorded request battery.
//
// The corpus freezes the observable behavior of the current gRPC + gateway
// stack — status codes, contract-relevant headers, and response bodies — so
// the stdlib net/http rewrite can be driven red→green against it, and so it
// can live on permanently as the contract regression net.
//
// Comparison is semantic JSON equality: parse → canonicalize → diff. Byte
// equality is explicitly NOT the contract (protojson randomizes whitespace
// per build, so today's API is already not byte-stable).
//
// Exactly one transform is applied between the wire and the goldens:
// stripKeycloakID. See its doc comment.
package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// canonicalize parses raw JSON into a canonical in-memory form: objects are
// map[string]any, arrays []any, numbers json.Number (preserving the exact
// wire literal — critical because protojson emits 64-bit integers as strings
// and 32-bit ones as numbers, and that distinction is part of the contract).
// Trailing garbage after the document is rejected.
func canonicalize(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("canonicalize: %w", err)
	}
	// Reject trailing non-whitespace so a JSON prefix of a text body can't
	// masquerade as a JSON response.
	if dec.More() {
		return nil, fmt.Errorf("canonicalize: trailing data after JSON document")
	}
	return v, nil
}

// marshalCanonical renders a canonical value as deterministic bytes: object
// keys sorted (encoding/json sorts map keys), 2-space indent, HTML escaping
// off so URLs stay readable. Two semantically equal documents always yield
// identical bytes. Used for golden files and diff display only — equality is
// decided by diff, never by comparing these bytes.
func marshalCanonical(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		// Canonical values come from canonicalize and contain only JSON-safe
		// types; failure here is a programming error.
		panic(fmt.Sprintf("contract: marshal canonical: %v", err))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// stripKeycloakID is the single documented corpus transform (issue #116).
//
// The profile shapes (Profile, LiteProfile) carry a deprecated "keycloakId"
// field that is removed at cutover, together with the keycloak lookup route.
// Goldens record the truth the new stack must reproduce, so the field is
// stripped — from recorded goldens at record time and from live responses at
// replay time, keeping the comparison symmetric while the old stack still
// emits it. It removes every object key named exactly "keycloakId" at any
// depth and touches nothing else. No other transform exists.
func stripKeycloakID(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "keycloakId" {
				continue
			}
			out[k] = stripKeycloakID(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = stripKeycloakID(val)
		}
		return out
	default:
		return v
	}
}

// diff reports the semantic differences between two canonical values as
// human-readable strings, each prefixed with the dot-joined path of the
// mismatch. An empty result means the documents are contract-equal.
//
// Equality rules: object key sets and values must match (key order never
// matters); array order and length matter; number form matters ("3" the
// string and 3 the number are different — protojson's 64-bit string form is
// part of the wire contract); null, empty object/array, and absent key are
// three distinct states.
func diff(want, got any) []string {
	var diffs []string
	diffValue("$", want, got, &diffs)
	return diffs
}

func diffValue(path string, want, got any, diffs *[]string) {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("%s: want object, got %s", path, render(got)))
			return
		}
		keys := make([]string, 0, len(w)+len(g))
		for k := range w {
			keys = append(keys, k)
		}
		for k := range g {
			if _, dup := w[k]; !dup {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			wv, inW := w[k]
			gv, inG := g[k]
			kp := path + "." + k
			switch {
			case !inG:
				*diffs = append(*diffs, fmt.Sprintf("%s: missing (want %s)", kp, render(wv)))
			case !inW:
				*diffs = append(*diffs, fmt.Sprintf("%s: unexpected key (got %s)", kp, render(gv)))
			default:
				diffValue(kp, wv, gv, diffs)
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			*diffs = append(*diffs, fmt.Sprintf("%s: want array, got %s", path, render(got)))
			return
		}
		if len(w) != len(g) {
			*diffs = append(*diffs, fmt.Sprintf("%s: array length %d != %d", path, len(w), len(g)))
			return
		}
		for i := range w {
			diffValue(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], diffs)
		}
	default:
		if !scalarEqual(want, got) {
			*diffs = append(*diffs, fmt.Sprintf("%s: want %s, got %s", path, render(want), render(got)))
		}
	}
}

// scalarEqual compares JSON scalars. json.Number compares by literal so the
// wire form is preserved; types must match exactly (string "3" ≠ number 3).
func scalarEqual(a, b any) bool {
	switch av := a.(type) {
	case json.Number:
		bv, ok := b.(json.Number)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		// Composites reaching here mean the caller compared mismatched kinds.
		return false
	}
}

// render shows a value compactly for diff messages.
func render(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	if len(b) > 80 {
		b = append(b[:77], "..."...)
	}
	return string(b)
}
