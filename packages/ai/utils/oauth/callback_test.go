package oauth

import (
	"strings"
	"testing"
)

func TestParseAuthorizationInput(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
	}{
		{"empty", "   ", "", ""},
		{"full url", "https://localhost/callback?code=abc&state=xyz", "abc", "xyz"},
		{"hash separated", "thecode#thestate", "thecode", "thestate"},
		{"query fragment", "code=qcode&state=qstate", "qcode", "qstate"},
		{"bare code", "justacode", "justacode", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, state, err := ParseAuthorizationInput(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if code != tc.wantCode || state != tc.wantState {
				t.Fatalf("ParseAuthorizationInput(%q)=(%q,%q), want (%q,%q)", tc.input, code, state, tc.wantCode, tc.wantState)
			}
		})
	}
}

func TestHTMLEscape(t *testing.T) {
	got := HTMLEscape(`<a href="x">&</a>`)
	want := "&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;"
	if got != want {
		t.Fatalf("HTMLEscape=%q, want %q", got, want)
	}
}

func TestSuccessHTMLEscapesMessage(t *testing.T) {
	html := SuccessHTML("<script>alert(1)</script>")
	if strings.Contains(html, "<script>") {
		t.Fatalf("SuccessHTML did not escape message: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("SuccessHTML missing escaped message: %s", html)
	}
}

func TestErrorHTMLOptionalDetails(t *testing.T) {
	withoutDetails := ErrorHTML("Failed", "")
	if strings.Contains(withoutDetails, "<pre>") {
		t.Fatalf("ErrorHTML should omit <pre> when no details: %s", withoutDetails)
	}
	withDetails := ErrorHTML("Failed", "stack & trace")
	if !strings.Contains(withDetails, "<pre>stack &amp; trace</pre>") {
		t.Fatalf("ErrorHTML missing escaped details: %s", withDetails)
	}
}
