package servers

// Startup-wiring guard for the trusted-proxy set (#190, ADR 0005). The
// resolver behavior is exhaustively table-tested in rest/clientip_test.go, but
// nothing pinned that the composition root actually CALLS
// rest.InitTrustedProxies() — and makes a non-empty-but-malformed
// TRUSTED_PROXIES FATAL — BEFORE any listener opens. That call is load-bearing
// twice over: skip it and every 401 log line silently logs the proxy address
// instead of the real caller (trust-nothing fallback), and a misconfigured
// trust set boots silently instead of failing loud. Because Error.Fatalf calls
// os.Exit, a direct behavioral test of the fatal path would take the test
// process down with it; a source-level convention guard is the established
// idiom in this codebase for exactly this "a refactor could silently drop the
// wiring and every existing test would still pass" hazard (see
// rest/wrapperconventions_test.go and the flush-chain composition guard). It
// also makes the cross-issue #134 documentation requirement — "preserve this
// call when rebuilding the single-listener composition root" — self-announcing
// rather than remembered: dropping or reordering the call turns this red.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// startMethodDecl parses server.go (anchored on this test file's directory so
// the guard works regardless of how the suite is invoked, same anchor as the
// rest tag-lint) and returns the *MicroServer.Start FuncDecl plus the FileSet
// for position reasoning.
func startMethodDecl(t *testing.T) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed — cannot locate the package directory")
	src := filepath.Join(filepath.Dir(thisFile), "server.go")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.SkipObjectResolution)
	require.NoError(t, err, "server.go must parse")

	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Start" || fd.Recv == nil {
			continue
		}
		return fset, fd
	}
	t.Fatal("could not find the Start method in server.go")
	return nil, nil
}

// selectorCall reports the source position of the first call expression whose
// callee is the selector pkg.fn (e.g. rest.InitTrustedProxies), and whether
// one was found, searching the given node.
func selectorCall(node ast.Node, pkg, fn string) (token.Pos, bool) {
	var pos token.Pos
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if ok && x.Name == pkg && sel.Sel.Name == fn {
			pos = call.Pos()
			found = true
			return false
		}
		return true
	})
	return pos, found
}

// firstSelectorCallPos is selectorCall's position-only twin, returning the
// max token.Pos sentinel when the call is absent so an absent serving call
// never appears to precede the init call in the ordering check.
func firstSelectorCallPos(node ast.Node, pkg, fn string) token.Pos {
	if pos, ok := selectorCall(node, pkg, fn); ok {
		return pos
	}
	return token.Pos(int(^uint(0) >> 1)) // max int — "never, so never earlier"
}

// firstIdentCallPos returns the position of the first call to a bare function
// named fn (e.g. servHTTP), or the max sentinel when absent.
func firstIdentCallPos(node ast.Node, fn string) token.Pos {
	pos := token.Pos(int(^uint(0) >> 1))
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == fn {
			pos = call.Pos()
			found = true
			return false
		}
		return true
	})
	return pos
}

// TestStart_WiresInitTrustedProxiesBeforeServing is the wiring guard: the
// composition root must call rest.InitTrustedProxies() before it opens any
// listener or hands off to the serve loops, and that call's error must be made
// fatal via Error.Fatalf — not silently dropped (which would boot a
// misconfigured trust set trusting nothing). Source-level because
// Error.Fatalf's os.Exit makes the fatal path untestable in-process, and
// because the failure this defends against is precisely a refactor deleting or
// reordering the call with every existing behavioral test still green.
func TestStart_WiresInitTrustedProxiesBeforeServing(t *testing.T) {
	_, start := startMethodDecl(t)

	initPos, ok := selectorCall(start, "rest", "InitTrustedProxies")
	require.True(t, ok,
		"Start must call rest.InitTrustedProxies() — without it the 401 log lines silently log the proxy address instead of the real caller (ADR 0005), and a malformed TRUSTED_PROXIES boots trusting nothing. CROSS-ISSUE #134 must preserve this call when rebuilding the single-listener composition root.")

	// The init call must precede every listener-open and serve-loop handoff:
	// the trusted set has to be cached before any request can hit the shared
	// AuthMiddleware that reads it.
	require.Less(t, int(initPos), int(firstSelectorCallPos(start, "net", "Listen")),
		"rest.InitTrustedProxies() must run BEFORE net.Listen opens a socket — the trusted set must be cached before any request can reach the 401 log sites")
	require.Less(t, int(initPos), int(firstIdentCallPos(start, "servHTTP")),
		"rest.InitTrustedProxies() must run BEFORE servHTTP starts serving")
	require.Less(t, int(initPos), int(firstIdentCallPos(start, "servGRPC")),
		"rest.InitTrustedProxies() must run BEFORE servGRPC starts serving")
}

// TestStart_MakesMalformedTrustedProxiesFatal pins the error handling on the
// init call: its error must reach Error.Fatalf (which os.Exits), so a
// non-empty-but-malformed TRUSTED_PROXIES kills the boot instead of being
// swallowed. The fatal call must sit inside the same if-block that tests the
// InitTrustedProxies error, so the guard cannot be satisfied by an unrelated
// Error.Fatalf elsewhere in Start.
func TestStart_MakesMalformedTrustedProxiesFatal(t *testing.T) {
	_, start := startMethodDecl(t)

	// Find the if-statement whose init/cond invokes rest.InitTrustedProxies,
	// then assert that branch's body fatals on the error.
	var guarded bool
	ast.Inspect(start, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		// The init call lives in the if's Init (err := rest.InitTrustedProxies())
		// or, defensively, its Cond.
		callInInit := false
		if ifStmt.Init != nil {
			_, callInInit = selectorCall(ifStmt.Init, "rest", "InitTrustedProxies")
		}
		callInCond := false
		if ifStmt.Cond != nil {
			_, callInCond = selectorCall(ifStmt.Cond, "rest", "InitTrustedProxies")
		}
		if !callInInit && !callInCond {
			return true
		}
		// This is the trusted-proxy init guard — its body must fatal.
		if _, fatal := selectorCall(ifStmt.Body, "Error", "Fatalf"); fatal {
			guarded = true
		}
		return false
	})

	require.True(t, guarded,
		"the rest.InitTrustedProxies() error must be made fatal via Error.Fatalf in the same if-block — a swallowed error boots a misconfigured trust set silently trusting nothing (ADR 0005)")
}
