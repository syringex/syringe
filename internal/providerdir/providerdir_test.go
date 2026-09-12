package providerdir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syringex/syringe/internal/providerdir"
)

func TestDirAndBinaryPath(t *testing.T) {
	dir := providerdir.Dir("/tmp/project")
	if want := filepath.Join("/tmp/project", ".syringe"); dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}

	bp := providerdir.BinaryPath(dir, "aws-sm")
	want := filepath.Join(dir, "providers", "aws-sm", "inject-provider-aws-sm")
	if bp != want {
		t.Errorf("BinaryPath = %q, want %q", bp, want)
	}
}

func TestEnsureDirAndExists(t *testing.T) {
	base := t.TempDir()
	dir := providerdir.Dir(base)

	if providerdir.Exists(dir, "aws-sm") {
		t.Fatal("expected aws-sm not to exist before install")
	}

	if err := providerdir.EnsureDir(dir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if err := providerdir.EnsureProviderDir(dir, "aws-sm"); err != nil {
		t.Fatalf("EnsureProviderDir: %v", err)
	}

	binPath := providerdir.BinaryPath(dir, "aws-sm")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !providerdir.Exists(dir, "aws-sm") {
		t.Fatal("expected aws-sm to exist after install")
	}
}

func TestEnsureDirRejectsNonDirectory(t *testing.T) {
	base := t.TempDir()
	dir := providerdir.Dir(base)
	if err := os.WriteFile(dir, []byte("oops"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := providerdir.EnsureDir(dir); err == nil {
		t.Fatal("expected EnsureDir to reject a non-directory occupying dir's path")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	base := t.TempDir()
	dir := providerdir.Dir(base)

	if providerdir.HasConfig(dir, "aws-sm") {
		t.Fatal("expected no config before SaveEnv")
	}
	env, err := providerdir.LoadEnv(dir, "aws-sm")
	if err != nil {
		t.Fatalf("LoadEnv on missing config: %v", err)
	}
	if env != nil {
		t.Errorf("LoadEnv on missing config = %v, want nil", env)
	}

	want := map[string]string{"AWS_REGION": "us-east-1"}
	if err := providerdir.SaveEnv(dir, "aws-sm", want); err != nil {
		t.Fatalf("SaveEnv: %v", err)
	}
	if !providerdir.HasConfig(dir, "aws-sm") {
		t.Fatal("expected HasConfig to be true after SaveEnv")
	}

	got, err := providerdir.LoadEnv(dir, "aws-sm")
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if len(got) != len(want) || got["AWS_REGION"] != want["AWS_REGION"] {
		t.Errorf("LoadEnv = %v, want %v", got, want)
	}

	data, err := os.ReadFile(providerdir.ConfigPath(dir, "aws-sm"))
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	if !strings.Contains(string(data), "[env]") || !strings.Contains(string(data), "us-east-1") {
		t.Errorf("config.toml = %q, want it to contain an [env] table with the region", data)
	}
}
