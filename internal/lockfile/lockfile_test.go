package lockfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/syringex/syringe/internal/lockfile"
)

func TestLoadMissingFileReturnsEmptyValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockfile.FileName)
	lf, err := lockfile.Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if lf.LockfileVersion != 1 {
		t.Errorf("LockfileVersion = %d, want 1", lf.LockfileVersion)
	}
	if len(lf.Providers) != 0 {
		t.Errorf("Providers = %v, want empty", lf.Providers)
	}
}

func TestSetPlatformThenSaveThenLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockfile.FileName)

	lf, err := lockfile.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lf.SetPlatform("aws", "0.1.0", "darwin_arm64", "sha256:abc123")
	if err := lf.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := lockfile.Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	entry, ok := reloaded.Providers["aws"]
	if !ok {
		t.Fatal(`expected an "aws" entry after reload`)
	}
	if entry.Version != "0.1.0" {
		t.Errorf("Version = %q, want %q", entry.Version, "0.1.0")
	}
	platform, ok := entry.Platforms["darwin_arm64"]
	if !ok || platform.Digest != "sha256:abc123" {
		t.Errorf("Platforms[darwin_arm64] = %+v, ok=%v", platform, ok)
	}
}

func TestSetPlatformPreservesOtherProvidersAndPlatforms(t *testing.T) {
	var lf lockfile.Lockfile
	lf.SetPlatform("aws", "0.1.0", "darwin_arm64", "sha256:aws-mac")
	lf.SetPlatform("gcp", "0.2.0", "linux_amd64", "sha256:gcp-linux")
	// Update aws on a different platform: must not clobber aws's existing
	// darwin_arm64 entry, nor the unrelated gcp provider.
	lf.SetPlatform("aws", "0.1.0", "linux_amd64", "sha256:aws-linux")

	if lf.Providers["aws"].Platforms["darwin_arm64"].Digest != "sha256:aws-mac" {
		t.Error("aws's darwin_arm64 entry was clobbered")
	}
	if lf.Providers["aws"].Platforms["linux_amd64"].Digest != "sha256:aws-linux" {
		t.Error("aws's linux_amd64 entry wasn't set")
	}
	if lf.Providers["gcp"].Platforms["linux_amd64"].Digest != "sha256:gcp-linux" {
		t.Error("gcp's entry was clobbered by an unrelated provider's update")
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), lockfile.FileName)
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lockfile.Load(path); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestDigestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := lockfile.DigestFile(path)
	if err != nil {
		t.Fatalf("DigestFile: %v", err)
	}
	const wantDigest = "sha256:b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9" // sha256("hello world")
	if got != wantDigest {
		t.Errorf("DigestFile = %q, want %q", got, wantDigest)
	}
}
