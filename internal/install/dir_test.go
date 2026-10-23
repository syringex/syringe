package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syringex/syringe/internal/install"
)

func TestDirInAndBinaryPath(t *testing.T) {
	dir := install.DirIn("/tmp/project")
	if want := filepath.Join("/tmp/project", ".syringe"); string(dir) != want {
		t.Errorf("DirIn = %q, want %q", dir, want)
	}

	bp := dir.BinaryPath("aws-sm")
	want := filepath.Join(string(dir), "providers", "aws-sm", "inject-provider-aws-sm")
	if bp != want {
		t.Errorf("BinaryPath = %q, want %q", bp, want)
	}
}

func TestPrepareAndIsInstalled(t *testing.T) {
	base := t.TempDir()
	dir := install.DirIn(base)

	if dir.IsInstalled("aws-sm") {
		t.Fatal("expected aws-sm not to exist before install")
	}

	if err := dir.Prepare("aws-sm"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	binPath := dir.BinaryPath("aws-sm")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if !dir.IsInstalled("aws-sm") {
		t.Fatal("expected aws-sm to exist after install")
	}
}

func TestPrepareRejectsNonDirectory(t *testing.T) {
	base := t.TempDir()
	dir := install.DirIn(base)
	if err := os.WriteFile(string(dir), []byte("oops"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := dir.Prepare("aws-sm"); err == nil {
		t.Fatal("expected Prepare to reject a non-directory occupying dir's path")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	base := t.TempDir()
	dir := install.DirIn(base)

	if dir.HasConfig("aws-sm") {
		t.Fatal("expected no config before SaveEnv")
	}
	env, err := dir.LoadEnv("aws-sm")
	if err != nil {
		t.Fatalf("LoadEnv on missing config: %v", err)
	}
	if env != nil {
		t.Errorf("LoadEnv on missing config = %v, want nil", env)
	}

	want := map[string]string{"AWS_REGION": "us-east-1"}
	if err := dir.SaveEnv("aws-sm", want); err != nil {
		t.Fatalf("SaveEnv: %v", err)
	}
	if !dir.HasConfig("aws-sm") {
		t.Fatal("expected HasConfig to be true after SaveEnv")
	}

	got, err := dir.LoadEnv("aws-sm")
	if err != nil {
		t.Fatalf("LoadEnv: %v", err)
	}
	if len(got) != len(want) || got["AWS_REGION"] != want["AWS_REGION"] {
		t.Errorf("LoadEnv = %v, want %v", got, want)
	}

	data, err := os.ReadFile(dir.ConfigPath("aws-sm"))
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	if !strings.Contains(string(data), "[env]") || !strings.Contains(string(data), "us-east-1") {
		t.Errorf("config.toml = %q, want it to contain an [env] table with the region", data)
	}
}
