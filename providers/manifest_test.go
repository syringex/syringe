package providers

import "testing"

// This pins the actual embedded manifest.json's shape — a regression guard
// against the real file, not just the parsing logic.
func TestLoadEmbeddedManifest(t *testing.T) {
	m, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	aws, ok := m.Providers["aws"]
	if !ok {
		t.Fatal(`expected an "aws" provider entry`)
	}
	if aws.Version == "" {
		t.Error("aws.Version is empty")
	}
	if aws.Package != "github.com/syringex/syringe/providers/aws/cmd/inject-provider-aws" {
		t.Errorf("aws.Package = %q", aws.Package)
	}

	wantTags := map[string]bool{"aws-sm": false, "aws-ssm": false}
	for _, tag := range aws.Tags {
		if _, ok := wantTags[tag]; !ok {
			t.Errorf("unexpected tag %q", tag)
			continue
		}
		wantTags[tag] = true
	}
	for tag, seen := range wantTags {
		if !seen {
			t.Errorf("expected tag %q in aws.Tags", tag)
		}
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	orig := manifestJSON
	manifestJSON = []byte("{not valid json")
	defer func() { manifestJSON = orig }()

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}
