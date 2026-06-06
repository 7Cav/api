package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
)

// Auth identifies which credential a battery case presents. The concrete
// header values live in authHeader; goldens record the tier, not the token.
type Auth string

const (
	// AuthNone sends no Authorization header.
	AuthNone Auth = "none"
	// AuthRawKey sends a bare key without the "Bearer " scheme.
	AuthRawKey Auth = "raw-key"
	// AuthInvalidKey sends a well-formed Bearer token that no key matches.
	AuthInvalidKey Auth = "invalid-key"
	// AuthRead is a valid key holding the "read" scope (milpacs surface).
	AuthRead Auth = "read"
	// AuthReadTickets is a valid key holding the "read:tickets" scope.
	AuthReadTickets Auth = "read:tickets"
	// AuthNoScopes is a valid key holding no scopes at all.
	AuthNoScopes Auth = "no-scopes"
)

// Valid reports whether a is one of the defined credential tiers.
func (a Auth) Valid() bool {
	switch a {
	case AuthNone, AuthRawKey, AuthInvalidKey, AuthRead, AuthReadTickets, AuthNoScopes:
		return true
	}
	return false
}

// authHeader maps an Auth tier to the Authorization header the harness sends.
// The token strings are battery fixtures: the recording fake datastore (and
// any future stack's test seed) must accept exactly these.
func authHeader(a Auth) (value string, present bool) {
	switch a {
	case AuthNone:
		return "", false
	case AuthRawKey:
		return "cav7_rawkeywithoutscheme", true
	case AuthInvalidKey:
		return "Bearer cav7_unknownkey", true
	case AuthRead:
		return "Bearer cav7_readkey", true
	case AuthReadTickets:
		return "Bearer cav7_ticketskey", true
	case AuthNoScopes:
		return "Bearer cav7_noscopekey", true
	default:
		panic(fmt.Sprintf("contract: unknown auth tier %q", a))
	}
}

// Case is one recorded request in the battery. Name doubles as the golden
// file path relative to the goldens dir (slashes create directories).
type Case struct {
	Name string
	// Method is always GET today; kept explicit so the corpus stays honest
	// if a non-GET route ever appears.
	Method string
	// Path is the request target including any query string, exactly as a
	// client would send it (URL-encoded).
	Path string
	Auth Auth
	// Notes documents why the case exists (shows up in the golden file).
	Notes string
}

// contractHeaders is the allowlist of headers recorded in goldens. Everything
// else (Date, Content-Length, Grpc-Metadata-*, transfer encodings — and
// historically X-Cache, gone at #123) is infrastructure, not contract:
//
//   - Content-Type distinguishes the JSON surface from the plain-text 401
//     tier pinned by #106.
//   - X-Content-Type-Options rides along on the plain-text error responses
//     (http.Error sets it) and clients may rely on it.
//   - WWW-Authenticate pins an ABSENCE: #106 froze the 401 tiers as having
//     no challenge header. RunCase skips empty headers, so goldens carry no
//     WWW-Authenticate key; a rewrite whose auth middleware adds one shows
//     up in CompareGolden as ""-vs-set — a red diff.
var contractHeaders = []string{"Content-Type", "X-Content-Type-Options", "WWW-Authenticate"}

// Golden is the recorded contract for one case: status, allowlisted headers,
// and the response body. JSON bodies are stored canonicalized (sorted keys,
// stable whitespace, keycloakId stripped); non-JSON bodies (the plain-text
// 401 tier) are stored verbatim in BodyText.
type Golden struct {
	Case   string            `json:"case"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Auth   Auth              `json:"auth"`
	Notes  string            `json:"notes,omitempty"`
	Status int               `json:"status"`
	Header map[string]string `json:"header"`
	// Body holds the canonical JSON body; null when the body is not JSON.
	Body json.RawMessage `json:"body,omitempty"`
	// BodyText holds the verbatim body when it is not JSON.
	BodyText *string `json:"bodyText,omitempty"`
}

// newCaseRequest constructs the HTTP request a battery case sends — the
// single construction shared by RunCase (recording and replay) and the spec
// replay loop, so the two can never drift apart.
func newCaseRequest(c Case) *http.Request {
	req := httptest.NewRequest(c.Method, c.Path, nil)
	if v, ok := authHeader(c.Auth); ok {
		req.Header.Set("Authorization", v)
	}
	return req
}

// RunCase drives one battery case through the mounted stack and returns the
// observed Golden (canonicalized, with the single keycloakId transform
// applied) plus the raw response bytes for informational byte-level output.
func RunCase(h http.Handler, c Case) (*Golden, []byte, error) {
	req := newCaseRequest(c)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	g := &Golden{
		Case:   c.Name,
		Method: c.Method,
		Path:   c.Path,
		Auth:   c.Auth,
		Notes:  c.Notes,
		Status: rr.Code,
		Header: map[string]string{},
	}
	for _, name := range contractHeaders {
		if v := rr.Header().Get(name); v != "" {
			g.Header[name] = v
		}
	}

	raw := rr.Body.Bytes()
	if isJSONContentType(rr.Header().Get("Content-Type")) {
		v, err := canonicalize(raw)
		if err != nil {
			return nil, raw, fmt.Errorf("case %s: response declared JSON but does not parse: %w", c.Name, err)
		}
		g.Body = marshalCanonical(stripKeycloakID(v))
	} else {
		s := string(raw)
		g.BodyText = &s
	}
	return g, raw, nil
}

func isJSONContentType(ct string) bool {
	return strings.HasPrefix(ct, "application/json")
}

// isContractHeader reports whether name is in the contractHeaders allowlist.
func isContractHeader(name string) bool {
	for _, h := range contractHeaders {
		if h == name {
			return true
		}
	}
	return false
}

// CompareGolden semantically compares a committed golden against an observed
// one. Returned strings are human-readable mismatches; empty means equal.
//
// Besides status/headers/body it self-checks the recorded request metadata
// (case, method, path, auth) so a golden replayed under the wrong request is
// a diff rather than silently write-only, flags recorded header keys outside
// the contractHeaders allowlist, and rejects malformed goldens that carry
// both or neither of body/bodyText.
func CompareGolden(want, got *Golden) []string {
	var diffs []string
	if want.Case != got.Case {
		diffs = append(diffs, fmt.Sprintf("case: want %q, got %q", want.Case, got.Case))
	}
	if want.Method != got.Method {
		diffs = append(diffs, fmt.Sprintf("method: want %q, got %q", want.Method, got.Method))
	}
	if want.Path != got.Path {
		diffs = append(diffs, fmt.Sprintf("path: want %q, got %q", want.Path, got.Path))
	}
	if want.Auth != got.Auth {
		diffs = append(diffs, fmt.Sprintf("auth: want %q, got %q", want.Auth, got.Auth))
	}
	if want.Status != got.Status {
		diffs = append(diffs, fmt.Sprintf("status: want %d, got %d", want.Status, got.Status))
	}
	for _, name := range contractHeaders {
		w, g := want.Header[name], got.Header[name]
		if w != g {
			diffs = append(diffs, fmt.Sprintf("header %s: want %q, got %q", name, w, g))
		}
	}
	for name := range want.Header {
		if !isContractHeader(name) {
			diffs = append(diffs, fmt.Sprintf("header %s: recorded but not contract-compared (not in allowlist)", name))
		}
	}
	for _, side := range []struct {
		label string
		g     *Golden
	}{{"want", want}, {"got", got}} {
		if (side.g.Body != nil) == (side.g.BodyText != nil) {
			diffs = append(diffs, fmt.Sprintf("%s: malformed golden: exactly one of body/bodyText must be set (json=%t text=%t)",
				side.label, side.g.Body != nil, side.g.BodyText != nil))
		}
	}
	switch {
	case want.Body != nil && got.Body != nil:
		wv, werr := canonicalize(want.Body)
		gv, gerr := canonicalize(got.Body)
		if werr != nil || gerr != nil {
			diffs = append(diffs, fmt.Sprintf("body: unparseable golden (want err %v, got err %v)", werr, gerr))
			break
		}
		diffs = append(diffs, diff(wv, gv)...)
	case want.BodyText != nil && got.BodyText != nil:
		if *want.BodyText != *got.BodyText {
			diffs = append(diffs, fmt.Sprintf("bodyText: want %q, got %q", *want.BodyText, *got.BodyText))
		}
	default:
		diffs = append(diffs, fmt.Sprintf("body kind: want json=%t text=%t, got json=%t text=%t",
			want.Body != nil, want.BodyText != nil, got.Body != nil, got.BodyText != nil))
	}
	return diffs
}

// goldenPath maps a case name to its file under dir. Empty names and names
// that resolve outside dir (path traversal, absolute paths) are rejected.
func goldenPath(dir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("golden name must not be empty")
	}
	rel := filepath.FromSlash(name)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("golden name %q escapes the goldens directory", name)
	}
	return filepath.Join(dir, rel+".golden.json"), nil
}

// SaveGolden writes g to its file under dir, creating directories as needed.
// The file itself is deterministic: fixed field order, canonical body.
func SaveGolden(dir string, g *Golden) error {
	path, err := goldenPath(dir, g.Case)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	// Indent the embedded canonical body consistently: re-render through the
	// canonical encoder (number-literal preserving) so committed goldens are
	// deterministic and pleasant to review.
	pretty, err := canonicalize(out)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(marshalCanonical(pretty), '\n'), 0o644)
}

// LoadGolden reads the committed golden for a case name and validates its
// structural invariants: exactly one of body/bodyText set and a defined auth
// tier — a hand-edited golden violating either is an error, not a silent
// pass.
func LoadGolden(dir, name string) (*Golden, error) {
	path, err := goldenPath(dir, name)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g Golden
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("golden %s: %w", name, err)
	}
	if (g.Body != nil) == (g.BodyText != nil) {
		return nil, fmt.Errorf("golden %s: exactly one of body/bodyText must be set (json=%t text=%t)",
			name, g.Body != nil, g.BodyText != nil)
	}
	if !g.Auth.Valid() {
		return nil, fmt.Errorf("golden %s: invalid auth tier %q", name, g.Auth)
	}
	return &g, nil
}
