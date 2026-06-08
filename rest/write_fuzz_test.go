package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// FuzzWriteError pins the error choke point's wire invariant against arbitrary
// message content. Error messages on this API embed raw user input verbatim —
// the frozen "parsing field ...: ... parsing \"<raw>\"" and "type mismatch ...
// error: <raw>" strings carry whatever bytes the caller sent, including
// control characters and invalid UTF-8. Whatever goes in, the response must
// stay a valid gRPC-status JSON body: parseable, code echoed, details an
// always-present empty array (emit-everything), Content-Type application/json,
// and the HTTP status exactly the code→status table's answer.
//
// json.Marshal sanitizes invalid UTF-8 to U+FFFD, so the message does NOT
// round-trip byte-for-byte — the invariant is "always valid contract JSON",
// not "message survives unchanged". Run:
// go test ./rest -run x -fuzz FuzzWriteError
func FuzzWriteError(f *testing.F) {
	for _, seed := range []struct {
		c   int
		msg string
	}{
		{int(codeInvalidArgument), "type mismatch, parameter: roster, error: combat is not valid"},
		{int(codeNotFound), "no profile found for username: john/doe"},
		{int(codeInvalidArgument), "parsing field \"per_page\": strconv.ParseUint: parsing \"\xff\xfe\": invalid syntax"},
		{int(codeUnknown), ""},
		{999, "code outside the frozen table"},
		{int(codeInvalidArgument), "quotes \" and newlines \n and tabs \t"},
	} {
		f.Add(seed.c, seed.msg)
	}

	f.Fuzz(func(t *testing.T, codeInt int, msg string) {
		rr := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/fuzz", nil)

		// "%s" with the message as the sole arg mirrors how callers embed raw
		// user input; the format string itself is always a constant literal.
		writeError(rr, r, code(codeInt), "%s", msg)

		res := rr.Result()
		if ct := res.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		if want := code(codeInt).httpStatus(); rr.Code != want {
			t.Fatalf("status = %d, want %d for code %d", rr.Code, want, codeInt)
		}

		body := rr.Body.Bytes()
		if !json.Valid(body) {
			t.Fatalf("response body is not valid JSON: %q", body)
		}
		var got statusBody
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("body did not decode into statusBody: %v (%q)", err, body)
		}
		if got.Code != code(codeInt) {
			t.Fatalf("decoded code = %d, want %d", got.Code, codeInt)
		}
		if got.Details == nil || len(got.Details) != 0 {
			t.Fatalf("details = %v, want always-present empty array", got.Details)
		}
	})
}
