package types_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the mechanical tag-lint for the wire conventions in the
// package doc (types.go), issue #148. The goldens and the spec replay loop
// prove names, kinds, and coverage — but only where the corpus reaches:
// `omitempty` on a field the goldens always populate slips through, and a
// missing `,string` is caught only on golden-covered fields (the contract
// differ preserves the number-vs-string distinction via UseNumber, see
// contract/canon.go, and the spec types 64-bit fields as Uint64String). The
// lint's value is declaration-time enforcement, before a type has goldens at
// all. It walks every struct in the package source and converts the prose
// into enforcement:
//
//   - every field carries an explicit json tag with a lowerCamelCase name
//   - no `omitempty` anywhere (emit-everything convention)
//   - `,string` present iff the Go type is int64/uint64 (protojson
//     convention); 64-bit pointers/slices demand an explicit decision
//   - structural honesty: fields are exported, declared one per line, wire
//     names are unique per struct, and anonymous struct types are forbidden

// lowerCamel is the wire-name shape: leading lowercase letter, then letters
// and digits only. It also rejects the empty name and the `json:"-"` opt-out
// (wire structs have no hidden fields).
var lowerCamel = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// isInt64Kind reports whether the declared field type is literally the ident
// int64 or uint64 — the two kinds the `,string` tag must annotate. Pointers
// and slices of (u)int64 do NOT fall out naturally (protojson serializes
// optional and repeated 64-bit values as strings too, and `,string` cannot
// express that for slices), so needs64BitDecision flags them separately.
// Named types carry their own marshalers and uint64 MAP KEYS stringify
// without a tag, so both stay exempt.
func isInt64Kind(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && (id.Name == "int64" || id.Name == "uint64")
}

// needs64BitDecision reports whether the field type is a pointer or slice
// (at any depth) of a builtin (u)int64 — shapes where `,string` semantics
// diverge from protojson and an explicit marshaling decision is required.
func needs64BitDecision(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return isInt64Kind(e.X) || needs64BitDecision(e.X)
	case *ast.ArrayType:
		return isInt64Kind(e.Elt) || needs64BitDecision(e.Elt)
	}
	return false
}

// containsAnonStruct reports whether a struct type literal appears anywhere
// inside the field's declared type.
func containsAnonStruct(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if _, ok := n.(*ast.StructType); ok {
			found = true
		}
		return !found
	})
	return found
}

// fieldLabel renders a field's declared name(s) for violation messages.
func fieldLabel(fld *ast.Field) string {
	if len(fld.Names) == 0 {
		return "(embedded)"
	}
	names := make([]string, len(fld.Names))
	for i, n := range fld.Names {
		names[i] = n.Name
	}
	return strings.Join(names, ", ")
}

// wireViolations walks every struct type in f — named, anonymous, var-decl,
// function-local; matching bare *ast.StructType nodes means nothing escapes
// — and returns one human-readable violation per non-conforming field, plus
// the number of declared field names seen (min one per declaration, so
// embedded fields count) for the caller's vacuous-pass floor guard.
func wireViolations(fset *token.FileSet, f *ast.File) (violations []string, fields int) {
	// TypeSpec nodes are visited before the struct types they declare, so
	// names are always recorded by the time the StructType arm fires.
	names := map[*ast.StructType]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			if st, ok := ts.Type.(*ast.StructType); ok {
				names[st] = ts.Name.Name
			}
			return true
		}
		st, ok := n.(*ast.StructType)
		if !ok {
			return true
		}
		structName, ok := names[st]
		if !ok {
			structName = "(anonymous struct)"
		}
		seen := map[string]bool{} // conforming wire names, for uniqueness
		for _, fld := range st.Fields.List {
			if n := len(fld.Names); n > 1 {
				fields += n
			} else {
				fields++ // single-name or embedded
			}
			v, wireName := fieldViolations(fset, structName, fld)
			violations = append(violations, v...)
			if wireName == "" {
				continue
			}
			if seen[wireName] {
				violations = append(violations, fmt.Sprintf(
					"%s: %s.%s: duplicate wire name %q (encoding/json silently drops EVERY field contending for one wire name)",
					fset.Position(fld.Pos()), structName, fieldLabel(fld), wireName))
			}
			seen[wireName] = true
		}
		return true
	})
	return violations, fields
}

// fieldViolations applies the wire conventions to one struct field. The
// second return is the field's conforming wire name ("" when the field has
// no usable json tag or the name itself is a violation) so the caller can
// enforce per-struct wire-name uniqueness.
func fieldViolations(fset *token.FileSet, structName string, fld *ast.Field) (out []string, wireName string) {
	at := func(format string, args ...any) string {
		return fmt.Sprintf("%s: %s.%s: %s", fset.Position(fld.Pos()), structName, fieldLabel(fld), fmt.Sprintf(format, args...))
	}

	if len(fld.Names) > 1 {
		out = append(out, at("one field per declaration (a shared tag gives every name the same wire name and encoding/json silently drops them all)"))
	}
	for _, n := range fld.Names {
		if !ast.IsExported(n.Name) {
			out = append(out, at("unexported field never reaches the wire (encoding/json ignores it — the json tag is a lie)"))
			break
		}
	}
	if containsAnonStruct(fld.Type) {
		out = append(out, at("anonymous structs forbidden in wire types — declare a named type"))
	}

	if fld.Tag == nil {
		return append(out, at("missing json tag (wire convention: explicit lowerCamelCase json name)")), ""
	}
	raw, err := strconv.Unquote(fld.Tag.Value)
	if err != nil {
		return append(out, at("unparseable struct tag %s: %v", fld.Tag.Value, err)), ""
	}
	jsonTag, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		if strings.Contains(raw, "json") {
			return append(out, at("malformed struct tag %q (json key present but unreadable — expected json:\"name\")", raw)), ""
		}
		return append(out, at("missing json tag (wire convention: explicit lowerCamelCase json name)")), ""
	}

	parts := strings.Split(jsonTag, ",")
	if name := parts[0]; lowerCamel.MatchString(name) {
		wireName = name
	} else {
		out = append(out, at("json name %q is not lowerCamelCase", name))
	}
	hasString := false
	for _, opt := range parts[1:] {
		switch opt {
		case "string":
			hasString = true
		case "omitempty":
			out = append(out, at("omitempty is forbidden (emit-everything convention: absent, \"\" and 0 are distinct wire states)"))
		default:
			out = append(out, at("unexpected json option %q (only `,string` is part of the wire conventions)", opt))
		}
	}
	if is64 := isInt64Kind(fld.Type); is64 && !hasString {
		out = append(out, at("(u)int64 field must carry the `,string` option (protojson: 64-bit integers are JSON strings)"))
	} else if !is64 && hasString {
		out = append(out, at("`,string` is reserved for (u)int64 fields (32-bit integers stay JSON numbers)"))
	}
	if needs64BitDecision(fld.Type) {
		out = append(out, at("64-bit pointer/slice needs an explicit marshaling decision (protojson emits repeated/optional 64-bit as strings; extend isInt64Kind when introducing one)"))
	}
	return out, wireName
}

// violationsOfSrc parses synthetic package-level declarations and returns
// the checker's verdicts plus the field count — the meta-test harness
// proving the lint can actually see each convention break, including the
// per-struct and walker-shape properties a single field cannot express.
func violationsOfSrc(t *testing.T, decls string) ([]string, int) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", "package types\n\n"+decls, parser.SkipObjectResolution)
	require.NoError(t, err)
	return wireViolations(fset, f)
}

// violationsOfField wraps violationsOfSrc for the common one-field case.
func violationsOfField(t *testing.T, field string) []string {
	t.Helper()
	v, fields := violationsOfSrc(t, "type Synthetic struct {\n\t"+field+"\n}\n")
	require.Equal(t, 1, fields, "synthetic struct must register exactly one field")
	return v
}

// The checker must detect each convention break the goldens cannot — and
// stay silent on conforming fields. Each violating case expects exactly one
// violation carrying the named substring.
func TestWireViolations_DetectsEachConventionBreak(t *testing.T) {
	cases := []struct {
		name  string
		field string
		want  string // substring of the single expected violation; "" = clean
	}{
		{"clean string field", "RealName string `json:\"realName\"`", ""},
		{"clean uint64 with string option", "UserId uint64 `json:\"userId,string\"`", ""},
		{"clean int64 with string option", "Offset int64 `json:\"offset,string\"`", ""},
		{"clean uint32 number", "ReplyCount uint32 `json:\"replyCount\"`", ""},
		{"clean named enum type", "Roster RosterType `json:\"roster\"`", ""},
		{"clean uint64-keyed map", "Profiles map[uint64]*Profile `json:\"profiles\"`", ""},
		{"omitempty is forbidden", "RealName string `json:\"realName,omitempty\"`", "omitempty"},
		{"omitempty on a tagged int64", "UserId uint64 `json:\"userId,string,omitempty\"`", "omitempty"},
		{"missing tag entirely", "RealName string", "missing json tag"},
		{"tag without json key", "RealName string `yaml:\"realName\"`", "missing json tag"},
		{"malformed tag with json key", "RealName string `json:realName`", "malformed struct tag"},
		{"UpperCamel json name", "RealName string `json:\"RealName\"`", "lowerCamelCase"},
		{"snake_case json name", "RealName string `json:\"real_name\"`", "lowerCamelCase"},
		{"empty json name", "UserId uint64 `json:\",string\"`", "lowerCamelCase"},
		{"hidden field via dash", "RealName string `json:\"-\"`", "lowerCamelCase"},
		{"unexported field with json tag", "hidden string `json:\"hidden\"`", "unexported"},
		{"uint64 without string option", "UserId uint64 `json:\"userId\"`", ",string"},
		{"int64 without string option", "Offset int64 `json:\"offset\"`", ",string"},
		{"pointer to int64", "Ptr *int64 `json:\"ptr\"`", "explicit marshaling decision"},
		{"pointer to uint64", "Ptr *uint64 `json:\"ptr\"`", "explicit marshaling decision"},
		{"slice of int64", "Ids []int64 `json:\"ids\"`", "explicit marshaling decision"},
		{"slice of uint64", "Ids []uint64 `json:\"ids\"`", "explicit marshaling decision"},
		{"string option on uint32", "ReplyCount uint32 `json:\"replyCount,string\"`", ",string"},
		{"string option on a string", "RealName string `json:\"realName,string\"`", ",string"},
		{"unknown json option", "RealName string `json:\"realName,omitEmpty\"`", "unexpected json option"},
		{"embedded field", "User", "missing json tag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := violationsOfField(t, tc.field)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Contains(t, got[0], tc.want)
			assert.Contains(t, got[0], "Synthetic", "violation must name the struct")
		})
	}
}

// The walker must see structs the TypeSpec-only shape would miss — anonymous
// struct field types and var-decl structs — and must flag the per-struct
// properties (multi-name declarations, duplicate wire names) that the
// single-field harness above cannot express. wantFields pins the per-name
// field accounting that keeps the floor guard in the package lint honest.
func TestWireViolations_WalkerSeesEveryStruct(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		wantFields int
		want       []string // one substring per expected violation, in order
	}{
		{
			name: "anonymous struct as field type",
			// The inner field hides its own convention break: a walker that
			// only fires on TypeSpec structs would report this src clean.
			src:        "type Outer struct {\n\tInner struct {\n\t\tBad string\n\t} `json:\"inner\"`\n}\n",
			wantFields: 2,
			want:       []string{"anonymous structs forbidden", "missing json tag"},
		},
		{
			name:       "var-decl struct",
			src:        "var hidden struct {\n\tBad string\n}\n",
			wantFields: 1,
			want:       []string{"missing json tag"},
		},
		{
			name:       "multi-name declaration",
			src:        "type T struct {\n\tA, B string `json:\"x\"`\n}\n",
			wantFields: 2,
			want:       []string{"one field per declaration"},
		},
		{
			name:       "duplicate wire name",
			src:        "type T struct {\n\tA string `json:\"x\"`\n\tC string `json:\"x\"`\n}\n",
			wantFields: 2,
			want:       []string{"duplicate wire name"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fields := violationsOfSrc(t, tc.src)
			assert.Equal(t, tc.wantFields, fields, "field accounting")
			require.Len(t, got, len(tc.want))
			for i, want := range tc.want {
				assert.Contains(t, got[i], want)
			}
		})
	}
}

// One field breaking three conventions at once must yield all three
// violations — pins the accumulation path, which the exactly-one cases
// above leave unexercised.
func TestWireViolations_AccumulatesCompoundBreaks(t *testing.T) {
	got := violationsOfField(t, "userId uint64 `json:\"userId,omitempty\"`")
	require.Len(t, got, 3)
	assert.Contains(t, got[0], "unexported")
	assert.Contains(t, got[1], "omitempty")
	assert.Contains(t, got[2], ",string")
}

// TestWireConventions_PackageTagLint is the enforcement itself: every struct
// field in every non-test file of this package must conform. Add a wire type
// and this test covers it with zero registration — that is the point.
func TestWireConventions_PackageTagLint(t *testing.T) {
	// Anchor on this file's own location rather than the working directory,
	// so the lint finds the package source regardless of how it is invoked.
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed — cannot locate the package directory")
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var all []string
	var fields int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		v, n := wireViolations(fset, f)
		all = append(all, v...)
		fields += n
	}

	// Vacuous-pass guard with a floor: NotZero would pass if the walker
	// regressed to covering one field of ~149. Bump the floor when removing
	// types; raise it when the package grows.
	require.GreaterOrEqual(t, fields, 140, "field count regressed — walker or file filter broke (bump floor when removing types)")
	for _, v := range all {
		t.Error(v)
	}
}
