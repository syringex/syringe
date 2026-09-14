package providerdownload

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeGitHub points baseURL at srv for the duration of the test.
func withFakeGitHub(t *testing.T, srv *httptest.Server) {
	t.Helper()
	orig := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = orig })
}

func TestFetchChecksums(t *testing.T) {
	content := "abc123  inject-provider-aws_v0.1.0_linux_amd64.tar.gz\ndef456  inject-provider-aws_v0.1.0_darwin_arm64.tar.gz\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, content)
	}))
	defer srv.Close()
	withFakeGitHub(t, srv)

	sums, err := FetchChecksums(context.Background(), "aws/v0.1.0")
	if err != nil {
		t.Fatalf("FetchChecksums: %v", err)
	}
	if sums["inject-provider-aws_v0.1.0_linux_amd64.tar.gz"] != "abc123" {
		t.Errorf("got %v", sums)
	}
	if sums["inject-provider-aws_v0.1.0_darwin_arm64.tar.gz"] != "def456" {
		t.Errorf("got %v", sums)
	}
}

func TestFetchChecksumsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	withFakeGitHub(t, srv)

	if _, err := FetchChecksums(context.Background(), "aws/v9.9.9"); err == nil {
		t.Fatal("expected an error when the release doesn't exist (404)")
	}
}

func TestFindAsset(t *testing.T) {
	sums := map[string]string{
		"inject-provider-aws_v0.1.0_linux_amd64.tar.gz":  "abc",
		"inject-provider-aws_v0.1.0_darwin_arm64.tar.gz": "def",
		"inject-provider-aws_v0.1.0_windows_amd64.zip":   "ghi",
	}
	name, digest, err := FindAsset(sums, "aws", "darwin", "arm64")
	if err != nil {
		t.Fatalf("FindAsset: %v", err)
	}
	if name != "inject-provider-aws_v0.1.0_darwin_arm64.tar.gz" || digest != "def" {
		t.Errorf("got (%q, %q)", name, digest)
	}
}

func TestFindAssetNotFound(t *testing.T) {
	sums := map[string]string{"inject-provider-aws_v0.1.0_linux_amd64.tar.gz": "abc"}
	if _, _, err := FindAsset(sums, "aws", "windows", "arm64"); err == nil {
		t.Fatal("expected an error when no matching asset exists for the platform")
	}
}

func TestDownloadAndVerifySuccess(t *testing.T) {
	content := []byte("fake archive bytes")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()
	withFakeGitHub(t, srv)

	got, err := DownloadAndVerify(context.Background(), "aws/v0.1.0", "asset.tar.gz", digest)
	if err != nil {
		t.Fatalf("DownloadAndVerify: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Error("content mismatch")
	}
}

func TestDownloadAndVerifyDigestMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("actual content"))
	}))
	defer srv.Close()
	withFakeGitHub(t, srv)

	wrongDigest := strings.Repeat("0", 64)
	if _, err := DownloadAndVerify(context.Background(), "aws/v0.1.0", "asset.tar.gz", wrongDigest); err == nil {
		t.Fatal("expected a digest mismatch error")
	}
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	archive := makeTarGz(t, map[string]string{
		"inject-provider-aws": "fake binary contents",
		"README.md":           "hi",
	})
	got, err := ExtractBinary("inject-provider-aws_v0.1.0_linux_amd64.tar.gz", archive, "inject-provider-aws")
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if string(got) != "fake binary contents" {
		t.Errorf("got %q", got)
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	archive := makeZip(t, map[string]string{
		"inject-provider-aws.exe": "fake windows binary",
	})
	got, err := ExtractBinary("inject-provider-aws_v0.1.0_windows_amd64.zip", archive, "inject-provider-aws.exe")
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if string(got) != "fake windows binary" {
		t.Errorf("got %q", got)
	}
}

func TestExtractBinaryMissingFromArchive(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"other-file": "x"})
	if _, err := ExtractBinary("x.tar.gz", archive, "inject-provider-aws"); err == nil {
		t.Fatal("expected an error when the binary isn't in the archive")
	}
}

func TestExtractBinaryUnknownFormat(t *testing.T) {
	if _, err := ExtractBinary("x.rar", []byte("data"), "inject-provider-aws"); err == nil {
		t.Fatal("expected an error for an unrecognized archive format")
	}
}

// fakeReleaseServer serves checksums.txt and one archive asset at the
// exact paths a real GitHub Release would use.
func fakeReleaseServer(tag, filename string, archive []byte, checksumsOverride string) *httptest.Server {
	sum := sha256.Sum256(archive)
	checksums := checksumsOverride
	if checksums == "" {
		checksums = fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), filename)
	}

	mux := http.NewServeMux()
	base := "/" + githubRepo + "/releases/download/" + tag + "/"
	mux.HandleFunc(base+"checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, checksums)
	})
	mux.HandleFunc(base+filename, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	return httptest.NewServer(mux)
}

// TestInstallEndToEnd exercises the full flow — fetch checksums, download,
// verify, extract, write to disk — against a fake GitHub Releases server;
// no real network access or a real published release required.
func TestInstallEndToEnd(t *testing.T) {
	binaryContent := []byte("#!/bin/sh\necho fake-aws-provider\n")
	archive := makeTarGz(t, map[string]string{"inject-provider-aws": string(binaryContent)})
	filename := "inject-provider-aws_v0.1.0_linux_amd64.tar.gz"

	srv := fakeReleaseServer("aws/v0.1.0", filename, archive, "")
	defer srv.Close()
	withFakeGitHub(t, srv)

	dest := filepath.Join(t.TempDir(), "inject-provider-aws")
	digest, err := Install(context.Background(), "aws", "0.1.0", "linux", "amd64", dest)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	wantSum := sha256.Sum256(binaryContent)
	wantDigest := "sha256:" + hex.EncodeToString(wantSum[:])
	if digest != wantDigest {
		t.Errorf("digest = %q, want %q", digest, wantDigest)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading installed binary: %v", err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Error("installed binary content mismatch")
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("installed binary should be executable")
	}
}

func TestInstallTamperedArchiveFails(t *testing.T) {
	archive := makeTarGz(t, map[string]string{"inject-provider-aws": "original"})
	filename := "inject-provider-aws_v0.1.0_linux_amd64.tar.gz"
	// checksums.txt records a digest for content different from what's
	// actually served — simulating a corrupted/tampered release asset.
	wrongChecksums := fmt.Sprintf("%s  %s\n", strings.Repeat("0", 64), filename)

	srv := fakeReleaseServer("aws/v0.1.0", filename, archive, wrongChecksums)
	defer srv.Close()
	withFakeGitHub(t, srv)

	dest := filepath.Join(t.TempDir(), "inject-provider-aws")
	if _, err := Install(context.Background(), "aws", "0.1.0", "linux", "amd64", dest); err == nil {
		t.Fatal("expected Install to fail on a digest mismatch")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("expected no file to be written when verification fails")
	}
}

func TestInstallNoReleaseYet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	withFakeGitHub(t, srv)

	dest := filepath.Join(t.TempDir(), "inject-provider-aws")
	if _, err := Install(context.Background(), "aws", "9.9.9", "linux", "amd64", dest); err == nil {
		t.Fatal("expected Install to fail when no release exists yet")
	}
}
