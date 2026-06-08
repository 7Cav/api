package rest_test

// Mechanical convention guards over the rest package's source (#174) — the
// same declaration-time-enforcement family as the types tag-lint (#148/#162)
// and the mux.Handle source scan (#173). The flush-chain composition test
// (flushchain_internal_test.go) proves today's chain flushes; these guards
// make the conventions that keep it flushing self-announcing for the NEXT
// wrapper, before it has any tests at all:
//
//   - FlushError convention: every ResponseWriter wrapper implements
//     `FlushError() error` or doesn't intercept flush at all — never bare
//     `Flush()`, whose errors http.ResponseController's Flusher branch
//     silently swallows (the wrapper reports success on a flush that died).
//   - Informational predicate (#165 review): every WriteHeader method on a
//     wrapper must consult informational() — a WriteHeader-stateful wrapper
//     added without the non-latching-1xx guard (and without volunteering
//     into the shared table in informational_internal_test.go) reintroduces
//     the exact #165 bug class invisibly.
//   - Hijack tripwire (triage ruling 2026-06-07, from #181's close-out): no
//     production code in this package hijacks. The gzip close-log's coarse
//     ErrHijacked carve-out (#175) is only safe while no handler hijacks
//     through the chain — this makes that precondition self-announcing
//     instead of remembered.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hijackRemedy is the ruled failure message for the hijack tripwire: the two
// sanctioned ways forward when a handler legitimately needs the connection.
const hijackRemedy = "no production code in package rest may hijack — the gzip close-log's coarse ErrHijacked carve-out (#175) is only safe while nothing hijacks through the chain. Mount upgrade endpoints OUTSIDE GzipMiddleware, or implement the faithfulness spec preserved in .out-of-scope/gzip-hijack-faithfulness.md (PR #185, from #181)"

// wrapperTypes returns the names of every struct type in files that embeds
// http.ResponseWriter — the package's writer-wrapper convention (all four
// production wrappers embed it; a wrapper holding the delegate in a named
// field would not satisfy http.ResponseWriter and could not enter the chain).
func wrapperTypes(files []*ast.File) map[string]bool {
	wrappers := map[string]bool{}
	for _, f := range files {
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
				if len(fld.Names) != 0 {
					continue // named field, not an embed
				}
				if sel, ok := fld.Type.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "http" && sel.Sel.Name == "ResponseWriter" {
						wrappers[ts.Name.Name] = true
					}
				}
			}
			return true
		})
	}
	return wrappers
}

// receiverTypeName resolves a method's receiver to its bare type name
// ("" when the declaration has no receiver or an unsupported shape).
func receiverTypeName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) != 1 {
		return ""
	}
	expr := fd.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// isFlushErrorSignature reports whether the method is exactly
// `FlushError() error` — no parameters, one unnamed error result.
func isFlushErrorSignature(fd *ast.FuncDecl) bool {
	if fd.Type.Params != nil && len(fd.Type.Params.List) != 0 {
		return false
	}
	if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
		return false
	}
	res := fd.Type.Results.List[0]
	if len(res.Names) != 0 {
		return false
	}
	id, ok := res.Type.(*ast.Ident)
	return ok && id.Name == "error"
}

// flushConventionViolations applies the FlushError convention to every
// ResponseWriter wrapper in files: never bare Flush() (the controller's
// Flusher branch swallows its errors), and a FlushError must carry the exact
// `FlushError() error` shape the controller's method search matches — any
// other signature is dead code the controller skips, falling through to the
// swallow-or-tunnel paths the author thought they had replaced. Returns the
// wrapper and FlushError-method counts for the caller's vacuous-pass floors.
func flushConventionViolations(fset *token.FileSet, files []*ast.File) (violations []string, wrappers, flushErrors int) {
	wrapperSet := wrapperTypes(files)
	wrappers = len(wrapperSet)
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || !wrapperSet[receiverTypeName(fd)] {
				continue
			}
			switch fd.Name.Name {
			case "Flush":
				violations = append(violations, fmt.Sprintf(
					"%s: %s.Flush: bare Flush() on a ResponseWriter wrapper — http.ResponseController's Flusher branch silently swallows its errors, so the handler hears success on a flush that died. Implement FlushError() error instead (see gzipResponseWriter/commitWriter/cacheControlWriter), or drop flush interception entirely and let Unwrap tunnel it",
					fset.Position(fd.Pos()), receiverTypeName(fd)))
			case "FlushError":
				flushErrors++
				if !isFlushErrorSignature(fd) {
					violations = append(violations, fmt.Sprintf(
						"%s: %s.FlushError: signature must be exactly `FlushError() error` — anything else is invisible to http.ResponseController's method search and never runs",
						fset.Position(fd.Pos()), receiverTypeName(fd)))
				}
			}
		}
	}
	return violations, wrappers, flushErrors
}

// informationalPredicateViolations enforces the #165 guard mechanically:
// every WriteHeader method on a ResponseWriter wrapper must reference the
// informational() predicate — the shared definition of "forwards without
// committing" (1xx minus 101). A WriteHeader-stateful wrapper that skips it
// latches on a forwarded 103 and diverges from the wire exactly as #165's
// wrappers did. Returns the WriteHeader-method count for the caller's
// vacuous-pass floor.
func informationalPredicateViolations(fset *token.FileSet, files []*ast.File) (violations []string, writeHeaders int) {
	wrapperSet := wrapperTypes(files)
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "WriteHeader" || !wrapperSet[receiverTypeName(fd)] {
				continue
			}
			writeHeaders++
			found := false
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == "informational" {
					found = true
				}
				return !found
			})
			if !found {
				violations = append(violations, fmt.Sprintf(
					"%s: %s.WriteHeader: must consult the informational() predicate (#165) — a wrapper that latches state on a forwarded non-latching 1xx diverges from net/http's commit semantics. Guard the latch with informational(code) and join the shared table in informational_internal_test.go",
					fset.Position(fd.Pos()), receiverTypeName(fd)))
			}
		}
	}
	return violations, writeHeaders
}

// hijackViolations is the tripwire: any identifier named Hijack or Hijacker
// anywhere in production source — a http.Hijacker assertion, a
// ResponseController.Hijack call, a local helper named after either —
// violates. Identifier-level deliberately: the precondition is "nothing
// hijacks", not "nothing hijacks in the shapes we thought of". Comments
// never trip it (they are not identifiers); http.ErrHijacked — the carve-out
// gzip.go legitimately matches on — is a different identifier and stays
// clean.
func hijackViolations(fset *token.FileSet, files []*ast.File) []string {
	var violations []string
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && (id.Name == "Hijack" || id.Name == "Hijacker") {
				violations = append(violations, fmt.Sprintf("%s: identifier %q: %s",
					fset.Position(id.Pos()), id.Name, hijackRemedy))
			}
			return true
		})
	}
	return violations
}

// parseSyntheticRest parses synthetic package-rest declarations for the
// meta-tests — the harness proving each checker can actually see the break
// it exists for (same discipline as the types tag-lint's violationsOfSrc).
func parseSyntheticRest(t *testing.T, decls string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go",
		"package rest\n\nimport \"net/http\"\n\nvar _ = http.StatusOK\n\n"+decls,
		parser.SkipObjectResolution)
	require.NoError(t, err)
	return fset, []*ast.File{f}
}

// syntheticWrapper is a minimal ResponseWriter wrapper the meta-cases extend.
const syntheticWrapper = "type fakeWrap struct {\n\thttp.ResponseWriter\n}\n\n"

// The flush-convention checker must see each break — and stay silent on the
// two conforming shapes (FlushError() error, or no flush interception).
func TestFlushConventionViolations_DetectsEachBreak(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		want  string // substring of the single expected violation; "" = clean
	}{
		{
			name:  "conforming FlushError",
			decls: syntheticWrapper + "func (w *fakeWrap) FlushError() error { return nil }\n",
		},
		{
			name:  "no flush interception at all",
			decls: syntheticWrapper + "func (w *fakeWrap) Unwrap() http.ResponseWriter { return w.ResponseWriter }\n",
		},
		{
			name:  "bare Flush",
			decls: syntheticWrapper + "func (w *fakeWrap) Flush() {}\n",
			want:  "bare Flush()",
		},
		{
			name:  "bare Flush on a value receiver",
			decls: syntheticWrapper + "func (w fakeWrap) Flush() {}\n",
			want:  "bare Flush()",
		},
		{
			name:  "FlushError without the error result",
			decls: syntheticWrapper + "func (w *fakeWrap) FlushError() {}\n",
			want:  "exactly `FlushError() error`",
		},
		{
			name:  "FlushError with a parameter",
			decls: syntheticWrapper + "func (w *fakeWrap) FlushError(force bool) error { return nil }\n",
			want:  "exactly `FlushError() error`",
		},
		{
			name:  "Flush on a non-wrapper stays out of scope",
			decls: "type plainBuffer struct{ n int }\n\nfunc (b *plainBuffer) Flush() {}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset, files := parseSyntheticRest(t, tc.decls)
			got, _, _ := flushConventionViolations(fset, files)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Contains(t, got[0], tc.want)
			assert.Contains(t, got[0], "fakeWrap", "violation must name the wrapper type")
		})
	}
}

// The informational-predicate checker must flag a WriteHeader-stateful
// wrapper that skips the predicate — and only on wrapper types.
func TestInformationalPredicateViolations_DetectsTheSkip(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		want  string // substring of the single expected violation; "" = clean
	}{
		{
			name:  "WriteHeader consulting the predicate",
			decls: syntheticWrapper + "func (w *fakeWrap) WriteHeader(code int) {\n\tif informational(code) {\n\t\tw.ResponseWriter.WriteHeader(code)\n\t\treturn\n\t}\n\tw.ResponseWriter.WriteHeader(code)\n}\n",
		},
		{
			name:  "WriteHeader without the predicate",
			decls: syntheticWrapper + "func (w *fakeWrap) WriteHeader(code int) {\n\tw.ResponseWriter.WriteHeader(code)\n}\n",
			want:  "must consult the informational() predicate",
		},
		{
			name:  "WriteHeader on a non-wrapper stays out of scope",
			decls: "type plainSink struct{ n int }\n\nfunc (s *plainSink) WriteHeader(code int) { s.n = code }\n",
		},
		{
			name:  "wrapper without WriteHeader is clean",
			decls: syntheticWrapper,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset, files := parseSyntheticRest(t, tc.decls)
			got, _ := informationalPredicateViolations(fset, files)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			require.Len(t, got, 1)
			assert.Contains(t, got[0], tc.want)
			assert.Contains(t, got[0], "fakeWrap", "violation must name the wrapper type")
		})
	}
}

// The hijack tripwire must fire on both hijack shapes, carry the ruled
// remedy, and stay silent on the legitimate neighbours (comments, the
// ErrHijacked carve-out gzip.go matches on).
func TestHijackViolations_TripsOnEachShapeOnly(t *testing.T) {
	cases := []struct {
		name  string
		decls string
		trips bool
	}{
		{
			name:  "Hijacker assertion",
			decls: "func grab(w http.ResponseWriter) {\n\t_, _ = w.(http.Hijacker)\n}\n",
			trips: true,
		},
		{
			name:  "ResponseController Hijack call",
			decls: "func grab(w http.ResponseWriter) {\n\t_, _, _ = http.NewResponseController(w).Hijack()\n}\n",
			trips: true,
		},
		{
			name:  "comment-only mention stays clean",
			decls: "// Hijack and Hijacker discussed here at length — comments are not code.\nfunc clean() {}\n",
			trips: false,
		},
		{
			name:  "ErrHijacked carve-out stays clean",
			decls: "func carve(err error) bool {\n\treturn err == http.ErrHijacked\n}\n",
			trips: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset, files := parseSyntheticRest(t, tc.decls)
			got := hijackViolations(fset, files)
			if !tc.trips {
				assert.Empty(t, got)
				return
			}
			require.NotEmpty(t, got)
			assert.Contains(t, got[0], "OUTSIDE GzipMiddleware",
				"the tripwire must name the sanctioned mounting remedy")
			assert.Contains(t, got[0], ".out-of-scope/gzip-hijack-faithfulness.md",
				"the tripwire must point at the preserved faithfulness spec")
		})
	}
}

// parseRestPackage parses every non-test source file of the rest package,
// anchored on this file's own location so the enforcement tests work
// regardless of how they are invoked (same anchor as the types tag-lint).
func parseRestPackage(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed — cannot locate the package directory")
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		files = append(files, f)
	}
	require.GreaterOrEqual(t, len(files), 10,
		"file filter regressed — the rest package has many more production sources than this")
	return fset, files
}

// The FlushError convention, enforced over the package: zero violations, and
// the floors prove the walker still sees the real wrapper population (four
// wrappers, all four carrying FlushError since #174 gave statusWriter the
// #164/#167 treatment — bump the floors when wrappers come or go).
func TestRestWrappers_FlushErrorConvention(t *testing.T) {
	fset, files := parseRestPackage(t)
	violations, wrappers, flushErrors := flushConventionViolations(fset, files)

	require.GreaterOrEqual(t, wrappers, 4,
		"wrapper count regressed — the embed detection or file filter broke (the chain mounts four writer wrappers)")
	require.GreaterOrEqual(t, flushErrors, 4,
		"FlushError count regressed — the method scan broke")
	for _, v := range violations {
		t.Error(v)
	}
}

// The informational predicate, enforced over the package: every wrapper
// WriteHeader consults informational() (#165). The floor pins the four
// production WriteHeader methods the scan must see.
func TestRestWrappers_WriteHeaderConsultsInformationalPredicate(t *testing.T) {
	fset, files := parseRestPackage(t)
	violations, writeHeaders := informationalPredicateViolations(fset, files)

	require.GreaterOrEqual(t, writeHeaders, 4,
		"WriteHeader count regressed — the method scan or file filter broke (all four wrappers intercept WriteHeader)")
	for _, v := range violations {
		t.Error(v)
	}
}

// The hijack tripwire, enforced over the package: today trivially true (only
// comments mention hijacking), and that is the point — the moment production
// code asserts Hijacker or calls Hijack, this fails with the ruled remedy
// instead of letting the #175 carve-out's precondition rot silently.
func TestRest_NoProductionCodeHijacks(t *testing.T) {
	fset, files := parseRestPackage(t)
	for _, v := range hijackViolations(fset, files) {
		t.Error(v)
	}
}
