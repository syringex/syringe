package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/syringex/syringe/internal/lockfile"
	"github.com/syringex/syringe/internal/providerdownload"
)

var (
	injectBinPath       string
	fakeProviderBinPath string
	testModuleDir       string
)

// TestMain builds the real inject binary and a generic stand-in provider
// binary once, before any test chdir's the process into a scratch
// directory, so the whole CLI (including root's real syscall.Exec path) is
// exercised end to end without any real cloud dependency. It also records
// the repo root (two levels up from this package) while the working
// directory is still trustworthy, so init tests can point the moduleDir
// build-time var at real provider source.
func TestMain(m *testing.M) {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	testModuleDir, err = filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		panic(err)
	}

	dir, err := os.MkdirTemp("", "inject-cmd-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	injectBinPath = filepath.Join(dir, "inject")
	if err := goBuild(injectBinPath, "github.com/syringex/syringe/cmd/inject"); err != nil {
		panic("building inject: " + err.Error())
	}

	fakeProviderBinPath = filepath.Join(dir, "fakeprovider")
	if err := goBuild(fakeProviderBinPath, "github.com/syringex/syringe/internal/execprovider/testdata/fakeprovider"); err != nil {
		panic("building fakeprovider: " + err.Error())
	}

	os.Exit(m.Run())
}

func goBuild(out, pkg string) error {
	build := exec.Command("go", "build", "-o", out, pkg)
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	return build.Run()
}

// installFakeProvider makes the prebuilt fake provider binary available at
// .syringe/providers/<name>/inject-provider-<name> relative to the test's
// current working directory. name is a provider identity (e.g. "aws"), not
// necessarily a reference tag — see providerNames.
func installFakeProvider(t *testing.T, name string) {
	t.Helper()
	destDir := filepath.Join(".syringe", "providers", name)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(destDir, "inject-provider-"+name)

	src, err := os.Open(fakeProviderBinPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	if _, err := io.Copy(out, src); err != nil {
		t.Fatal(err)
	}
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

func writeEnvFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runCLI runs the in-process command tree (fast, no subprocess) — suitable
// for every command except root's `--` exec, which really execs a process
// and so needs the real built binary (see runInjectBinary).
func runCLI(args ...string) (stdout, stderr string, err error) {
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestGetResolvesInstalledProvider(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\nPLAIN=hello\n")
	installFakeProvider(t, "aws")

	stdout, _, err := runCLI("get", "DB_PASSWORD")
	if err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), "resolved-prod/db/password"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestAwsSmAndAwsSsmShareOneInstalledProvider(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\nAPI_KEY=aws-ssm:/myapp/api-key\n")
	// Only ONE provider installed, under the shared "aws" identity — not
	// "aws-sm" and "aws-ssm" separately — yet both tags must resolve.
	installFakeProvider(t, "aws")

	stdout, _, err := runCLI("export", "--format", "dotenv")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if !strings.Contains(stdout, "DB_PASSWORD=resolved-prod/db/password") {
		t.Errorf("stdout = %q, want it to contain the resolved aws-sm value", stdout)
	}
	if !strings.Contains(stdout, "API_KEY=resolved-/myapp/api-key") {
		t.Errorf("stdout = %q, want it to contain the resolved aws-ssm value", stdout)
	}

	// Confirm there's genuinely no separate aws-ssm install.
	if _, err := os.Stat(filepath.Join(dir, ".syringe", "providers", "aws-ssm")); err == nil {
		t.Error("expected no separate .syringe/providers/aws-ssm directory")
	}
}

func TestGetLiteralValue(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "PLAIN=hello-world\n")

	stdout, _, err := runCLI("get", "PLAIN")
	if err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), "hello-world"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestGetUnknownKey(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "PLAIN=hello\n")

	if _, _, err := runCLI("get", "MISSING"); err == nil {
		t.Fatal("expected error for a key not present in the file")
	}
}

func TestKnownTagNotInstalledProducesActionableError(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")
	// deliberately no .syringe/ installed

	_, _, err := runCLI("check")
	if err == nil {
		t.Fatal("expected an error for a known-but-uninstalled provider tag")
	}
	if !strings.Contains(err.Error(), "inject init") {
		t.Errorf("error = %v, want it to mention `inject init`", err)
	}
}

func TestUnknownTagFallsBackToLiteral(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "NOTE=see:section3\n")
	// "see" isn't a known provider tag, so this should resolve as a literal
	// even with nothing installed under .syringe/.

	stdout, _, err := runCLI("get", "NOTE")
	if err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), "see:section3"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestCheckNeverPrintsValues(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")
	installFakeProvider(t, "aws")

	stdout, _, err := runCLI("check")
	if err != nil {
		t.Fatalf("check returned error: %v", err)
	}
	if strings.Contains(stdout, "resolved-") {
		t.Errorf("check output leaked a resolved value: %q", stdout)
	}
	if !strings.Contains(stdout, "DB_PASSWORD: ok") {
		t.Errorf("stdout = %q, want it to report DB_PASSWORD: ok", stdout)
	}
}

func TestVersionCommand(t *testing.T) {
	origVersion, origCommit, origDate := version, commit, date
	version, commit, date = "v0.1.0", "abc1234", "2026-01-01"
	t.Cleanup(func() { version, commit, date = origVersion, origCommit, origDate })

	stdout, _, err := runCLI("version")
	if err != nil {
		t.Fatalf("version returned error: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), "inject v0.1.0 (commit abc1234, built 2026-01-01)"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestExportFormats(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "PLAIN=hello world\n")

	stdout, _, err := runCLI("export")
	if err != nil {
		t.Fatalf("export returned error: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), `export PLAIN='hello world'`; got != want {
		t.Errorf("shell export = %q, want %q", got, want)
	}

	stdout, _, err = runCLI("export", "--format", "dotenv")
	if err != nil {
		t.Fatalf("export --format dotenv returned error: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), "PLAIN=hello world"; got != want {
		t.Errorf("dotenv export = %q, want %q", got, want)
	}
}

func TestRootExecInjectsResolvedEnv(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "PLAIN_GREETING=hello-from-inject\n")

	child := exec.Command(injectBinPath, "--", "sh", "-c", "echo $PLAIN_GREETING")
	child.Dir = dir
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("inject -- sh -c ...: %v\noutput: %s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "hello-from-inject"; got != want {
		t.Errorf("child process saw %q, want %q", got, want)
	}
}

func TestRootExecAbortsOnResolveFailure(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")
	// deliberately no provider installed: exec must never run.

	marker := filepath.Join(dir, "should-not-exist")
	child := exec.Command(injectBinPath, "--", "sh", "-c", "touch "+marker)
	child.Dir = dir
	_ = child.Run()

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("child command ran despite a resolve failure — root should fail closed")
	}
}

// The real aws provider's `init` step always requires an interactive
// terminal now (it deliberately never silently trusts ambient AWS config —
// see providers/aws's own tests for that logic in isolation), and go
// test's stdin isn't one. So here we verify the part that's actually this
// package's responsibility: the binary still gets built and .gitignore
// still gets updated even though configuring fails — a real bug this test
// catches (a configure failure used to short-circuit before either of
// those happened).
func TestInitBuildsProviderEvenWhenConfiguringFailsNonInteractively(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = testModuleDir
	t.Cleanup(func() { moduleDir = origModuleDir })

	stdout, _, err := runCLI("init")
	if err == nil {
		t.Fatal("expected init to report a configure failure (non-interactive stdin)")
	}
	if !strings.Contains(err.Error(), "aws") {
		t.Errorf("error = %v, want it to name the aws provider", err)
	}
	if !strings.Contains(stdout, "configuring") {
		t.Errorf("init stdout = %q, want it to mention configuring aws", stdout)
	}

	binPath := filepath.Join(dir, ".syringe", "providers", "aws", "inject-provider-aws")
	if info, statErr := os.Stat(binPath); statErr != nil || info.IsDir() {
		t.Fatalf("expected the provider binary to be built despite the configure failure, stat error: %v", statErr)
	}

	configPath := filepath.Join(dir, ".syringe", "providers", "aws", "config.toml")
	if _, statErr := os.Stat(configPath); statErr == nil {
		t.Fatalf("expected no config.toml to be written when configuring fails")
	}

	gitignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("expected init to create .gitignore despite the configure failure: %v", err)
	}
	if !strings.Contains(string(gitignore), ".syringe/") {
		t.Errorf(".gitignore = %q, want it to contain .syringe/", gitignore)
	}

	// The lock file must be written even though configuring failed — the
	// binary itself was built successfully and that's what it records.
	lockData, err := os.ReadFile(filepath.Join(dir, lockfile.FileName))
	if err != nil {
		t.Fatalf("expected init to write %s despite the configure failure: %v", lockfile.FileName, err)
	}
	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil {
		t.Fatalf("re-parsing %s: %v", lockfile.FileName, err)
	}
	entry, ok := lf.Providers["aws"]
	if !ok {
		t.Fatalf("%s has no aws entry: %s", lockfile.FileName, lockData)
	}
	if entry.Version == "" {
		t.Error("aws entry has no version")
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	digest, ok := entry.Platforms[platform]
	if !ok {
		t.Fatalf("aws entry has no %q platform: %+v", platform, entry.Platforms)
	}
	wantDigest, err := lockfile.DigestFile(binPath)
	if err != nil {
		t.Fatalf("digesting the installed binary: %v", err)
	}
	if digest.Digest != wantDigest {
		t.Errorf("recorded digest = %q, want %q (the actual installed binary's digest)", digest.Digest, wantDigest)
	}

	// A second run without --force should skip rebuilding (binary already
	// installed) but retry configuring, since config.toml still doesn't exist.
	stdout, _, err = runCLI("init")
	if err == nil {
		t.Fatal("expected the second init to also report a configure failure")
	}
	if !strings.Contains(stdout, "already installed") {
		t.Errorf("second init stdout = %q, want it to report the tag as already installed", stdout)
	}
	if !strings.Contains(stdout, "configuring") {
		t.Errorf("second init stdout = %q, want it to retry configuring since config.toml is still missing", stdout)
	}
}

// inject init must treat aws-sm and aws-ssm as one provider identity to
// build/configure — a real .env referencing both should only trigger one
// build and one configure attempt, not two.
func TestInitBuildsAwsProviderOnceForBothTags(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\nAPI_KEY=aws-ssm:/myapp/api-key\n")

	origModuleDir := moduleDir
	moduleDir = testModuleDir
	t.Cleanup(func() { moduleDir = origModuleDir })

	stdout, _, err := runCLI("init")
	if err == nil {
		t.Fatal("expected a configure failure (non-interactive stdin)")
	}

	if got := strings.Count(stdout, "building from"); got != 1 {
		t.Errorf("init built %d time(s), want exactly 1: %q", got, stdout)
	}
	if got := strings.Count(stdout, "configuring"); got != 1 {
		t.Errorf("init attempted to configure %d time(s), want exactly 1: %q", got, stdout)
	}

	binPath := filepath.Join(dir, ".syringe", "providers", "aws", "inject-provider-aws")
	if _, statErr := os.Stat(binPath); statErr != nil {
		t.Fatalf("expected the shared aws provider binary to be built: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".syringe", "providers", "aws-ssm")); statErr == nil {
		t.Error("expected no separate .syringe/providers/aws-ssm directory")
	}
}

// buildFakeReleaseArchive packages a minimal shell-script stand-in
// provider (speaks just enough of the protocol for `init` to succeed
// unconditionally) into a .tar.gz, alongside a matching checksums.txt for
// the filename a real release asset for (goos, goarch) would use.
func buildFakeReleaseArchive(t *testing.T, providerName, version, goos, goarch string) (archiveFilename string, archiveData []byte, checksumsContent string, scriptContent string) {
	t.Helper()
	script := "#!/bin/sh\necho '{\"env\":{}}'\n"

	var tarBuf bytes.Buffer
	gz := gzip.NewWriter(&tarBuf)
	tw := tar.NewWriter(gz)
	binName := "inject-provider-" + providerName
	if err := tw.WriteHeader(&tar.Header{Name: binName, Size: int64(len(script)), Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archiveData = tarBuf.Bytes()

	sum := sha256.Sum256(archiveData)
	archiveFilename = fmt.Sprintf("inject-provider-%s_v%s_%s_%s.tar.gz", providerName, version, goos, goarch)
	checksumsContent = fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), archiveFilename)
	return archiveFilename, archiveData, checksumsContent, script
}

// fakeReleaseServer serves checksums.txt and one archive at the same
// paths a real GitHub Release would use.
func fakeReleaseServer(tag, archiveFilename string, archiveData []byte, checksums string) *httptest.Server {
	mux := http.NewServeMux()
	base := "/syringex/syringe/releases/download/" + tag + "/"
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, checksums)
	})
	mux.HandleFunc(base+archiveFilename, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archiveData)
	})
	return httptest.NewServer(mux)
}

func TestInitDownloadsProviderWhenNoModuleDir(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = ""
	t.Cleanup(func() { moduleDir = origModuleDir })

	archiveFilename, archiveData, checksums, script := buildFakeReleaseArchive(t, "aws", "0.1.0", runtime.GOOS, runtime.GOARCH)

	srv := fakeReleaseServer("aws/v0.1.0", archiveFilename, archiveData, checksums)
	defer srv.Close()
	restore := providerdownload.SetBaseURLForTesting(srv.URL)
	t.Cleanup(restore)

	stdout, _, err := runCLI("init")
	if err != nil {
		t.Fatalf("init returned error: %v\nstdout: %s", err, stdout)
	}
	if !strings.Contains(stdout, "downloading version 0.1.0") {
		t.Errorf("stdout = %q, want it to mention downloading", stdout)
	}

	binPath := filepath.Join(dir, ".syringe", "providers", "aws", "inject-provider-aws")
	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("expected the downloaded binary to be installed: %v", err)
	}
	if string(got) != script {
		t.Errorf("installed binary content = %q, want %q", got, script)
	}

	lf, err := lockfile.Load(filepath.Join(dir, lockfile.FileName))
	if err != nil {
		t.Fatalf("loading %s: %v", lockfile.FileName, err)
	}
	entry, ok := lf.Providers["aws"]
	if !ok || entry.Version != "0.1.0" {
		t.Errorf("lock entry = %+v, want version 0.1.0", entry)
	}
	wantDigest, err := lockfile.DigestFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH
	if entry.Platforms[platform].Digest != wantDigest {
		t.Errorf("recorded digest = %q, want %q", entry.Platforms[platform].Digest, wantDigest)
	}

	// The fake binary speaks just enough protocol for configure to succeed.
	if _, statErr := os.Stat(filepath.Join(dir, ".syringe", "providers", "aws", "config.toml")); statErr != nil {
		t.Errorf("expected configure to succeed against the fake downloaded binary: %v", statErr)
	}
}

// The actual point of a lock file: a pre-existing syringe.lock pin must
// win over the manifest's current version, so a checked-in lock keeps
// installing the same version a team already agreed on.
func TestInitRespectsExistingLockPinOverManifestVersion(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = ""
	t.Cleanup(func() { moduleDir = origModuleDir })

	archiveFilename, archiveData, checksums, scriptContent := buildFakeReleaseArchive(t, "aws", "0.0.9", runtime.GOOS, runtime.GOARCH)

	// Pin to 0.0.9 — deliberately different from the manifest's current
	// "0.1.0" — before init ever runs, with the digest the download below
	// will actually produce (a real pin, not a placeholder, now that init
	// verifies a downloaded binary's digest against an existing pin for the
	// same version).
	sum := sha256.Sum256([]byte(scriptContent))
	pinnedDigest := "sha256:" + hex.EncodeToString(sum[:])
	var lf lockfile.Lockfile
	lf.SetPlatform("aws", "0.0.9", runtime.GOOS+"_"+runtime.GOARCH, pinnedDigest)
	if err := lf.Save(lockfile.FileName); err != nil {
		t.Fatal(err)
	}

	srv := fakeReleaseServer("aws/v0.0.9", archiveFilename, archiveData, checksums)
	defer srv.Close()
	restore := providerdownload.SetBaseURLForTesting(srv.URL)
	t.Cleanup(restore)

	stdout, _, err := runCLI("init")
	if err != nil {
		t.Fatalf("init returned error: %v\nstdout: %s", err, stdout)
	}
	if !strings.Contains(stdout, "downloading version 0.0.9") {
		t.Errorf("stdout = %q, want it to download the pinned 0.0.9, not the manifest's current 0.1.0", stdout)
	}

	reloaded, err := lockfile.Load(lockfile.FileName)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Providers["aws"].Version != "0.0.9" {
		t.Errorf("lock version after init = %q, want the pin (0.0.9) preserved", reloaded.Providers["aws"].Version)
	}
}

// --force bypasses the pin and re-resolves against the manifest's current
// version — the "upgrade" path.
func TestInitForceIgnoresLockPinAndUsesManifestVersion(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = ""
	t.Cleanup(func() { moduleDir = origModuleDir })

	var lf lockfile.Lockfile
	lf.SetPlatform("aws", "0.0.9", runtime.GOOS+"_"+runtime.GOARCH, "sha256:placeholder")
	if err := lf.Save(lockfile.FileName); err != nil {
		t.Fatal(err)
	}

	// Only the manifest's current version (0.1.0) is actually served —
	// if --force incorrectly still targeted the 0.0.9 pin, this would 404.
	archiveFilename, archiveData, checksums, _ := buildFakeReleaseArchive(t, "aws", "0.1.0", runtime.GOOS, runtime.GOARCH)
	srv := fakeReleaseServer("aws/v0.1.0", archiveFilename, archiveData, checksums)
	defer srv.Close()
	restore := providerdownload.SetBaseURLForTesting(srv.URL)
	t.Cleanup(restore)

	stdout, _, err := runCLI("init", "--force")
	if err != nil {
		t.Fatalf("init --force returned error: %v\nstdout: %s", err, stdout)
	}
	if !strings.Contains(stdout, "downloading version 0.1.0") {
		t.Errorf("stdout = %q, want --force to re-resolve to the manifest's current 0.1.0", stdout)
	}
}

// The actual guarantee a lock file provides: reinstalling the *same*
// pinned version must reproduce the *same* digest, or init refuses to
// proceed — catching a release that was replaced/tampered with after
// syringe.lock was committed, or a locally modified binary.
func TestInitDigestMismatchAgainstLockFailsWithoutForce(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = ""
	t.Cleanup(func() { moduleDir = origModuleDir })

	// Same version as what's about to be "downloaded", but a digest that
	// cannot possibly match the real archive's extracted binary.
	var lf lockfile.Lockfile
	lf.SetPlatform("aws", "0.1.0", runtime.GOOS+"_"+runtime.GOARCH, "sha256:0000000000000000000000000000000000000000000000000000000000000")
	if err := lf.Save(lockfile.FileName); err != nil {
		t.Fatal(err)
	}

	archiveFilename, archiveData, checksums, _ := buildFakeReleaseArchive(t, "aws", "0.1.0", runtime.GOOS, runtime.GOARCH)
	srv := fakeReleaseServer("aws/v0.1.0", archiveFilename, archiveData, checksums)
	defer srv.Close()
	restore := providerdownload.SetBaseURLForTesting(srv.URL)
	t.Cleanup(restore)

	_, _, err := runCLI("init")
	if err == nil {
		t.Fatal("expected an error on digest mismatch against the lock")
	}
	if !strings.Contains(err.Error(), "digest") || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want it to mention the digest mismatch and the --force escape hatch", err)
	}

	reloaded, err := lockfile.Load(lockfile.FileName)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Providers["aws"].Platforms[runtime.GOOS+"_"+runtime.GOARCH].Digest; got != "sha256:0000000000000000000000000000000000000000000000000000000000000" {
		t.Errorf("lock digest after a failed init = %q, want it left untouched", got)
	}
}

func TestInitForceOverridesDigestMismatch(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = ""
	t.Cleanup(func() { moduleDir = origModuleDir })

	var lf lockfile.Lockfile
	lf.SetPlatform("aws", "0.1.0", runtime.GOOS+"_"+runtime.GOARCH, "sha256:0000000000000000000000000000000000000000000000000000000000000")
	if err := lf.Save(lockfile.FileName); err != nil {
		t.Fatal(err)
	}

	archiveFilename, archiveData, checksums, scriptContent := buildFakeReleaseArchive(t, "aws", "0.1.0", runtime.GOOS, runtime.GOARCH)
	srv := fakeReleaseServer("aws/v0.1.0", archiveFilename, archiveData, checksums)
	defer srv.Close()
	restore := providerdownload.SetBaseURLForTesting(srv.URL)
	t.Cleanup(restore)

	if _, _, err := runCLI("init", "--force"); err != nil {
		t.Fatalf("init --force returned error: %v", err)
	}

	sum := sha256.Sum256([]byte(scriptContent))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	reloaded, err := lockfile.Load(lockfile.FileName)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Providers["aws"].Platforms[runtime.GOOS+"_"+runtime.GOARCH].Digest; got != wantDigest {
		t.Errorf("lock digest after --force = %q, want it updated to the newly installed binary's digest %q", got, wantDigest)
	}
}

func TestInitDownloadFailsClearlyWhenNoReleaseExists(t *testing.T) {
	dir := chdirTemp(t)
	writeEnvFile(t, dir, "DB_PASSWORD=aws-sm:prod/db/password\n")

	origModuleDir := moduleDir
	moduleDir = ""
	t.Cleanup(func() { moduleDir = origModuleDir })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	restore := providerdownload.SetBaseURLForTesting(srv.URL)
	t.Cleanup(restore)

	_, _, err := runCLI("init")
	if err == nil {
		t.Fatal("expected an error when no release has been published")
	}
	if !strings.Contains(err.Error(), "downloading provider") {
		t.Errorf("error = %v, want it to mention the download failure", err)
	}
}
