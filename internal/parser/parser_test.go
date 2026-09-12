package parser

import (
	"strings"
	"testing"

	"github.com/syringex/syringe/pkg/provider"
)

func TestParse(t *testing.T) {
	input := `# a comment
DB_PASSWORD=aws-sm:prod/db/password
API_KEY=aws-ssm:/myapp/api-key

export GCP_SECRET=gcp-sm:my-project/my-secret
AZURE_SECRET=az-kv:my-vault/my-secret
PLAIN_VALUE=just-a-normal-value-with-no-colon-prefix-scheme
NOTE=see:section3
DB_PASSWORD=aws-sm:prod/db/password-v2
`
	entries, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	want := []Entry{
		{Key: "DB_PASSWORD", RawValue: "aws-sm:prod/db/password", Ref: &provider.Ref{Tag: "aws-sm", Path: "prod/db/password", Raw: "aws-sm:prod/db/password"}, Line: 2},
		{Key: "API_KEY", RawValue: "aws-ssm:/myapp/api-key", Ref: &provider.Ref{Tag: "aws-ssm", Path: "/myapp/api-key", Raw: "aws-ssm:/myapp/api-key"}, Line: 3},
		{Key: "GCP_SECRET", RawValue: "gcp-sm:my-project/my-secret", Ref: &provider.Ref{Tag: "gcp-sm", Path: "my-project/my-secret", Raw: "gcp-sm:my-project/my-secret"}, Line: 5},
		{Key: "AZURE_SECRET", RawValue: "az-kv:my-vault/my-secret", Ref: &provider.Ref{Tag: "az-kv", Path: "my-vault/my-secret", Raw: "az-kv:my-vault/my-secret"}, Line: 6},
		{Key: "PLAIN_VALUE", RawValue: "just-a-normal-value-with-no-colon-prefix-scheme", Ref: nil, Line: 7},
		{Key: "NOTE", RawValue: "see:section3", Ref: &provider.Ref{Tag: "see", Path: "section3", Raw: "see:section3"}, Line: 8},
		{Key: "DB_PASSWORD", RawValue: "aws-sm:prod/db/password-v2", Ref: &provider.Ref{Tag: "aws-sm", Path: "prod/db/password-v2", Raw: "aws-sm:prod/db/password-v2"}, Line: 9},
	}

	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		w := want[i]
		if e.Key != w.Key || e.RawValue != w.RawValue || e.Line != w.Line {
			t.Errorf("entry %d = %+v, want %+v", i, e, w)
		}
		if (e.Ref == nil) != (w.Ref == nil) {
			t.Errorf("entry %d Ref presence mismatch: got %+v, want %+v", i, e.Ref, w.Ref)
			continue
		}
		if e.Ref != nil && *e.Ref != *w.Ref {
			t.Errorf("entry %d Ref = %+v, want %+v", i, *e.Ref, *w.Ref)
		}
	}
}

func TestParseMissingEquals(t *testing.T) {
	_, err := Parse(strings.NewReader("NOT_A_VALID_LINE\n"))
	if err == nil {
		t.Fatal("expected error for line without '='")
	}
	perr, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("expected *ParseError, got %T", err)
	}
	if perr.Line != 1 {
		t.Errorf("expected error on line 1, got line %d", perr.Line)
	}
}

func TestParseInvalidKey(t *testing.T) {
	_, err := Parse(strings.NewReader("1BAD=value\n"))
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestParseRef(t *testing.T) {
	cases := []struct {
		raw  string
		want *provider.Ref
	}{
		{"aws-sm:prod/db/password", &provider.Ref{Tag: "aws-sm", Path: "prod/db/password", Raw: "aws-sm:prod/db/password"}},
		{"no-colon-here", nil},
		{":leading-colon", nil},
		{"C:/windows/path", nil}, // "C" fails tagPattern (uppercase)
		{"az-kv:my-vault/my-secret", &provider.Ref{Tag: "az-kv", Path: "my-vault/my-secret", Raw: "az-kv:my-vault/my-secret"}},
	}
	for _, c := range cases {
		got := ParseRef(c.raw)
		if (got == nil) != (c.want == nil) {
			t.Errorf("ParseRef(%q) = %+v, want %+v", c.raw, got, c.want)
			continue
		}
		if got != nil && *got != *c.want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", c.raw, *got, *c.want)
		}
	}
}
