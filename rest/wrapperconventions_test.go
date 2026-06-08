package rest_test

// Mechanical convention guards over the rest package's source (#174) — the
// same declaration-time-enforcement family as the types tag-lint (#148/#162)
// and the mux.Handle source scan (#173). The flush-chain composition test
// (flushchain_internal_test.go) proves today's chain flushes; these guards
// make the conventions that keep it flushing self-announcing for the NEXT
// wrapper, before it has any tests at all:
//
//   - FlushError convention: every ResponseWriter wrapper implements
//     `FlushError() error` or skips flush interception and declares
//     `Unwrap() http.ResponseWriter` so the controller tunnels — never bare
//     `Flush()`, whose errors http.ResponseController's Flusher branch
//     silently swallows (the wrapper reports success on a flush that died),
//     and never NEITHER: a wrapper with no FlushError, no Flush and no
//     Unwrap dead-ends the controller's method walk, turning every handler
//     flush through it into http.ErrNotSupported in production (#174
//     review — the opaque-wrapper mutant survived the whole suite).
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

// httpImportName resolves the file-local name bound to the "net/http" import
// — "http" by default, the alias when one is declared, "" when the file does
// not import net/http at all (then no selector can reference it). Dot- and
// blank-imports also yield "" — neither produces the selector shape the
// scans match, and neither appears in this package.
func httpImportName(f *ast.File) string {
	for _, imp := range f.Imports {
		if imp.Path.Value != `"net/http"` {
			continue
		}
		if imp.Name == nil {
			return "http"
		}
		if name := imp.Name.Name; name != "." && name != "_" {
			return name
		}
	}
	return ""
}

// wrapperTypes returns every struct type in files that carries a
// http.ResponseWriter delegate — keyed by type name, with the TypeSpec
// position for violation messages — plus the embedded-type edges of EVERY
// struct, for the caller's method-promotion reasoning. Wrapper evidence is
// transitive and field-shape-blind (#174 review closed three detection
// holes):
//
//   - a field of type http.ResponseWriter counts whether EMBEDDED or NAMED:
//     a named delegate plus hand-written Header/Write/WriteHeader methods
//     satisfies the interface exactly as well, so the field shape is no
//     boundary (the four production wrappers all embed; the _test.go
//     delegate fakes are the named shape, and the production scan skips
//     test files);
//   - embedding another wrapper, by value or pointer, makes a wrapper —
//     computed to fixpoint, so a wrapper-of-a-wrapper inherits every guard
//     (its OWN method set is what http.ResponseController's walk sees);
//   - the net/http import's LOCAL name is resolved per file
//     (httpImportName), so an aliased import cannot smuggle a wrapper past
//     the scan.
func wrapperTypes(files []*ast.File) (wrappers map[string]token.Pos, embeds map[string][]string) {
	wrappers = map[string]token.Pos{}
	embeds = map[string][]string{}
	positions := map[string]token.Pos{}
	for _, f := range files {
		httpName := httpImportName(f)
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			positions[ts.Name.Name] = ts.Pos()
			for _, fld := range st.Fields.List {
				if sel, ok := fld.Type.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && httpName != "" && x.Name == httpName && sel.Sel.Name == "ResponseWriter" {
						wrappers[ts.Name.Name] = ts.Pos()
					}
					continue
				}
				if len(fld.Names) != 0 {
					continue // named fields of package-local types carry no promotion
				}
				switch ft := fld.Type.(type) {
				case *ast.Ident:
					embeds[ts.Name.Name] = append(embeds[ts.Name.Name], ft.Name)
				case *ast.StarExpr:
					if id, ok := ft.X.(*ast.Ident); ok {
						embeds[ts.Name.Name] = append(embeds[ts.Name.Name], id.Name)
					}
				}
			}
			return true
		})
	}
	// Fixpoint: embedding a wrapper promotes its delegate, so the embedder
	// is a wrapper too and must satisfy every guard itself.
	for changed := true; changed; {
		changed = false
		for name, embedded := range embeds {
			if _, done := wrappers[name]; done {
				continue
			}
			for _, e := range embedded {
				if _, isWrapper := wrappers[e]; isWrapper {
					wrappers[name] = positions[name]
					changed = true
					break
				}
			}
		}
	}
	return wrappers, embeds
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
	res, ok := soleResult(fd)
	if !ok {
		return false
	}
	id, ok := res.(*ast.Ident)
	return ok && id.Name == "error"
}

// isUnwrapSignature reports whether the method is exactly
// `Unwrap() http.ResponseWriter` — the only shape
// http.ResponseController's method walk descends through. Anything else
// (parameters, a different result type) is invisible to the controller.
// httpName is the declaring file's local name for the net/http import.
func isUnwrapSignature(fd *ast.FuncDecl, httpName string) bool {
	res, ok := soleResult(fd)
	if !ok {
		return false
	}
	sel, ok := res.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && httpName != "" && x.Name == httpName && sel.Sel.Name == "ResponseWriter"
}

// soleResult returns the method's single unnamed result type, reporting
// false for any other parameter/result arity.
func soleResult(fd *ast.FuncDecl) (ast.Expr, bool) {
	if fd.Type.Params != nil && len(fd.Type.Params.List) != 0 {
		return nil, false
	}
	if fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
		return nil, false
	}
	res := fd.Type.Results.List[0]
	if len(res.Names) != 0 {
		return nil, false
	}
	return res.Type, true
}

// flushConventionViolations applies the FlushError convention to every
// ResponseWriter wrapper in files: never bare Flush() (the controller's
// Flusher branch swallows its errors); a FlushError must carry the exact
// `FlushError() error` shape the controller's method search matches — any
// other signature is dead code the controller skips, falling through to the
// swallow-or-tunnel paths the author thought they had replaced; and every
// wrapper must declare `FlushError() error` OR `Unwrap() http.ResponseWriter`
// — a wrapper with neither (and no Flush) dead-ends the controller's method
// walk, so every handler flush through it returns http.ErrNotSupported in
// production (#174 review: that opaque-wrapper mutant survived the entire
// suite before this rule). Returns the wrapper and FlushError-method counts
// for the caller's vacuous-pass floors.
func flushConventionViolations(fset *token.FileSet, files []*ast.File) (violations []string, wrappers, flushErrors int) {
	wrapperSet, embeds := wrapperTypes(files)
	wrappers = len(wrapperSet)
	// escapes: the type declares Flush or FlushError under ANY signature
	// (shape problems are flagged individually, so the dead-end rule fires
	// only when the controller finds nothing at all to walk) or the exact
	// `Unwrap() http.ResponseWriter`. Recorded for EVERY receiver type, not
	// just wrappers: an embedder inherits the promoted methods below.
	escapes := map[string]bool{}
	for _, f := range files {
		httpName := httpImportName(f)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			recv := receiverTypeName(fd)
			if recv == "" {
				continue
			}
			_, isWrapper := wrapperSet[recv]
			switch fd.Name.Name {
			case "Flush":
				escapes[recv] = true
				if isWrapper {
					violations = append(violations, fmt.Sprintf(
						"%s: %s.Flush: bare Flush() on a ResponseWriter wrapper — http.ResponseController's Flusher branch silently swallows its errors, so the handler hears success on a flush that died. Implement FlushError() error instead (see gzipResponseWriter/commitWriter/cacheControlWriter), or drop flush interception entirely and let Unwrap tunnel it",
						fset.Position(fd.Pos()), recv))
				}
			case "FlushError":
				escapes[recv] = true
				if isWrapper {
					flushErrors++
					if !isFlushErrorSignature(fd) {
						violations = append(violations, fmt.Sprintf(
							"%s: %s.FlushError: signature must be exactly `FlushError() error` — anything else is invisible to http.ResponseController's method search and never runs",
							fset.Position(fd.Pos()), recv))
					}
				}
			case "Unwrap":
				if isUnwrapSignature(fd, httpName) {
					escapes[recv] = true
				}
			}
		}
	}
	// Promotion fixpoint: an embedder with no flush machinery of its own
	// still escapes the dead end when an embedded type provides it — Go
	// promotes the embedded FlushError/Unwrap into the embedder's method
	// set, where the controller's walk finds them.
	for changed := true; changed; {
		changed = false
		for name, embedded := range embeds {
			if escapes[name] {
				continue
			}
			for _, e := range embedded {
				if escapes[e] {
					escapes[name] = true
					changed = true
					break
				}
			}
		}
	}
	for name, pos := range wrapperSet {
		if escapes[name] {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s: %s: ResponseWriter wrapper with neither `FlushError() error` nor `Unwrap() http.ResponseWriter` — http.ResponseController's method walk dead-ends here, so every handler flush (and every other controller verb) through this layer returns http.ErrNotSupported in production. Implement FlushError() error to intercept flush (see gzipResponseWriter/commitWriter/cacheControlWriter), or declare Unwrap and let the controller tunnel",
			fset.Position(pos), name))
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
	wrapperSet, _ := wrapperTypes(files) // the transitive set feeds this guard too (#174 review)
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "WriteHeader" {
				continue
			}
			if _, isWrapper := wrapperSet[receiverTypeName(fd)]; !isWrapper {
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
// two conforming shapes (FlushError() error, or Unwrap tunnelling with no
// flush interception).
func TestFlushConventionViolations_DetectsEachBreak(t *testing.T) {
	cases := []struct {
		name     string
		decls    string
		want     string // substring of the single expected violation; "" = clean
		wantType string // wrapper the violation must name; "" = fakeWrap
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
			// The opaque-wrapper mutant from the #174 review: no FlushError,
			// no Flush, no Unwrap — the controller's method walk dead-ends
			// and every handler flush returns http.ErrNotSupported.
			name:  "dead end: neither FlushError nor Unwrap",
			decls: syntheticWrapper,
			want:  "neither `FlushError() error` nor `Unwrap() http.ResponseWriter`",
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
		{
			// Detection hole 1 (#174 review): a wrapper embedding another
			// wrapper is itself a wrapper — its OWN method set is what the
			// controller's walk sees, so it carries the conventions itself.
			name: "wrapper embedding another wrapper inherits the conventions",
			decls: syntheticWrapper +
				"func (w *fakeWrap) FlushError() error { return nil }\n\n" +
				"type outerWrap struct{ *fakeWrap }\n\nfunc (w *outerWrap) Flush() {}\n",
			want:     "bare Flush()",
			wantType: "outerWrap",
		},
		{
			// The promotion escape: a method-less embedder inherits the
			// embedded wrapper's FlushError into its method set, where the
			// controller's walk finds it — no dead end, no violation.
			name: "method-less wrapper embedding a conforming wrapper is clean",
			decls: syntheticWrapper +
				"func (w *fakeWrap) FlushError() error { return nil }\n\n" +
				"type outerWrap struct{ *fakeWrap }\n",
		},
		{
			// Detection hole 2 (#174 review): a NAMED http.ResponseWriter
			// delegate plus hand-written interface methods satisfies
			// http.ResponseWriter exactly as well as an embed — the old
			// embed-only match was blind to this shape.
			name: "named-field delegate is a wrapper",
			decls: "type namedWrap struct {\n\trw http.ResponseWriter\n}\n\n" +
				"func (w *namedWrap) Header() http.Header { return w.rw.Header() }\n\n" +
				"func (w *namedWrap) Write(b []byte) (int, error) { return w.rw.Write(b) }\n\n" +
				"func (w *namedWrap) WriteHeader(code int) { w.rw.WriteHeader(code) }\n\n" +
				"func (w *namedWrap) Flush() {}\n",
			want:     "bare Flush()",
			wantType: "namedWrap",
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
			wantType := tc.wantType
			if wantType == "" {
				wantType = "fakeWrap"
			}
			require.Len(t, got, 1)
			assert.Contains(t, got[0], tc.want)
			assert.Contains(t, got[0], wantType, "violation must name the wrapper type")
		})
	}
}

// Detection hole 3 (#174 review): an aliased net/http import must not slip
// the wrapper scan — the local import name is resolved per file
// (httpImportName), never assumed to be "http". Its own test because the
// shared synthetic harness pins the standard import clause.
func TestFlushConventionViolations_SeesAliasedNetHTTPImport(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic_alias.go",
		"package rest\n\nimport nethttp \"net/http\"\n\ntype aliasWrap struct {\n\tnethttp.ResponseWriter\n}\n\nfunc (w *aliasWrap) Flush() {}\n",
		parser.SkipObjectResolution)
	require.NoError(t, err)

	got, wrappers, _ := flushConventionViolations(fset, []*ast.File{f})
	assert.Equal(t, 1, wrappers, "the aliased embed must count as a wrapper")
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "bare Flush()")
	assert.Contains(t, got[0], "aliasWrap", "violation must name the wrapper type")
}

// The informational-predicate checker must flag a WriteHeader-stateful
// wrapper that skips the predicate — and only on wrapper types.
func TestInformationalPredicateViolations_DetectsTheSkip(t *testing.T) {
	cases := []struct {
		name     string
		decls    string
		want     string // substring of the single expected violation; "" = clean
		wantType string // wrapper the violation must name; "" = fakeWrap
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
		{
			// The transitive wrapper set feeds this guard too (#174 review):
			// a wrapper embedding another wrapper carries the #165
			// obligation itself — its own WriteHeader is what runs.
			name: "WriteHeader on a wrapper-embedding wrapper without the predicate",
			decls: syntheticWrapper +
				"type outerWrap struct{ *fakeWrap }\n\nfunc (w *outerWrap) WriteHeader(code int) {\n\tw.fakeWrap.WriteHeader(code)\n}\n",
			want:     "must consult the informational() predicate",
			wantType: "outerWrap",
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
			wantType := tc.wantType
			if wantType == "" {
				wantType = "fakeWrap"
			}
			require.Len(t, got, 1)
			assert.Contains(t, got[0], tc.want)
			assert.Contains(t, got[0], wantType, "violation must name the wrapper type")
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

	// Violations first: a floor abort below must never hide the findings.
	for _, v := range violations {
		t.Error(v)
	}
	require.GreaterOrEqual(t, wrappers, 4,
		"wrapper count fell below the floor — either the scan rotted (field detection, file filter, net/http import resolution) or a writer wrapper was deliberately removed from the chain; if the removal is intentional, bump this floor (and the FlushError floor below) alongside it")
	require.GreaterOrEqual(t, flushErrors, 4,
		"FlushError count fell below the floor — either the method scan rotted or a wrapper deliberately swapped flush interception for Unwrap tunnelling; if intentional, bump this floor alongside the change")
}

// The informational predicate, enforced over the package: every wrapper
// WriteHeader consults informational() (#165). The floor pins the four
// production WriteHeader methods the scan must see.
func TestRestWrappers_WriteHeaderConsultsInformationalPredicate(t *testing.T) {
	fset, files := parseRestPackage(t)
	violations, writeHeaders := informationalPredicateViolations(fset, files)

	// Violations first: a floor abort below must never hide the findings.
	for _, v := range violations {
		t.Error(v)
	}
	require.GreaterOrEqual(t, writeHeaders, 4,
		"WriteHeader count fell below the floor — either the method scan / file filter rotted or a wrapper deliberately stopped intercepting WriteHeader; if intentional, bump this floor alongside it (all four production wrappers intercept it today)")
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
