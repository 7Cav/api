package types_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is the mechanical tag-lint for the wire conventions in the
// package doc (types.go), issue #148: the goldens and the spec replay loop
// prove names, kinds, and coverage, but `omitempty` on a populated-in-goldens
// field and a missing `,string` slip through both. The lint walks every
// struct declared in the package source and converts the prose into
// enforcement:
//
//   - every field carries an explicit json tag with a lowerCamelCase name
//   - no `omitempty` anywhere (emit-everything convention)
//   - `,string` present iff the Go type is int64/uint64 (protojson convention)

// lowerCamel is the wire-name shape: leading lowercase letter, then letters
// and digits only. It also rejects the empty name and the `json:"-"` opt-out
// (wire structs have no hidden fields).
var lowerCamel = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// isInt64Kind reports whether the declared field type is the builtin
// int64/uint64 — the two kinds protojson serializes as JSON strings. Named
// types, pointers, slices, and maps are not Idents named (u)int64, so they
// fall out naturally (enums carry their own marshalers; uint64 MAP KEYS
// stringify without a tag).
func isInt64Kind(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && (id.Name == "int64" || id.Name == "uint64")
}

// wireViolations walks every struct type declared in f and returns one
// human-readable violation per non-conforming field, plus the number of
// fields seen so callers can guard against a vacuous pass.
func wireViolations(fset *token.FileSet, f *ast.File) (violations []string, fields int) {
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fld := range st.Fields.List {
			fields++
			violations = append(violations, fieldViolations(fset, ts.Name.Name, fld)...)
		}
		return true
	})
	return violations, fields
}

// fieldViolations applies the wire conventions to one struct field.
func fieldViolations(fset *token.FileSet, structName string, fld *ast.Field) []string {
	fieldName := "(embedded)"
	if len(fld.Names) > 0 {
		names := make([]string, len(fld.Names))
		for i, n := range fld.Names {
			names[i] = n.Name
		}
		fieldName = strings.Join(names, ", ")
	}
	at := func(format string, args ...any) string {
		return fmt.Sprintf("%s: %s.%s: %s", fset.Position(fld.Pos()), structName, fieldName, fmt.Sprintf(format, args...))
	}

	if fld.Tag == nil {
		return []string{at("missing json tag (wire convention: explicit lowerCamelCase json name)")}
	}
	raw, err := strconv.Unquote(fld.Tag.Value)
	if err != nil {
		return []string{at("unparseable struct tag %s: %v", fld.Tag.Value, err)}
	}
	jsonTag, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return []string{at("missing json tag (wire convention: explicit lowerCamelCase json name)")}
	}

	var out []string
	parts := strings.Split(jsonTag, ",")
	if name := parts[0]; !lowerCamel.MatchString(name) {
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
	return out
}

// violationsOfField parses a one-field synthetic struct and returns the
// checker's verdicts for it — the meta-test harness proving the lint can
// actually see each convention break.
func violationsOfField(t *testing.T, field string) []string {
	t.Helper()
	src := "package types\n\ntype Synthetic struct {\n\t" + field + "\n}\n"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	v, fields := wireViolations(fset, f)
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
		{"UpperCamel json name", "RealName string `json:\"RealName\"`", "lowerCamelCase"},
		{"snake_case json name", "RealName string `json:\"real_name\"`", "lowerCamelCase"},
		{"empty json name", "UserId uint64 `json:\",string\"`", "lowerCamelCase"},
		{"hidden field via dash", "RealName string `json:\"-\"`", "lowerCamelCase"},
		{"uint64 without string option", "UserId uint64 `json:\"userId\"`", ",string"},
		{"int64 without string option", "Offset int64 `json:\"offset\"`", ",string"},
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

// TestWireConventions_PackageTagLint is the enforcement itself: every struct
// field in every non-test file of this package must conform. Add a wire type
// and this test covers it with zero registration — that is the point.
func TestWireConventions_PackageTagLint(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	var all []string
	var fields int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		v, n := wireViolations(fset, f)
		all = append(all, v...)
		fields += n
	}

	// Vacuous-pass guard: zero fields means the walker broke, not that the
	// package went clean.
	require.NotZero(t, fields, "no struct fields found in package source — walker broken")
	for _, v := range all {
		t.Error(v)
	}
}
