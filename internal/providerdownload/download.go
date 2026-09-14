// Package providerdownload downloads, verifies, and extracts a provider
// binary from its GitHub Release. Used by `inject init` only for a real,
// distributed inject binary — a contributor's local monorepo build (which
// has `moduleDir` baked in) builds providers from source instead; see
// internal/cmd/init.go.
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
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

// githubRepo is this project's own repo — hardcoded rather than derived,
// since it's already effectively hardcoded throughout the codebase (every
// -ldflags -X package path names it).
const githubRepo = "syringex/syringe"

// baseURL is overridable in tests to point at an httptest server instead
// of real GitHub.
var baseURL = "https://github.com"

// SetBaseURLForTesting overrides the GitHub base URL used for every
// download, for the duration of a test (e.g. pointing it at an httptest
// server standing in for GitHub Releases). Returns a function that
// restores the original value — pair with t.Cleanup.
func SetBaseURLForTesting(url string) (restore func()) {
	orig := baseURL
	baseURL = url
	return func() { baseURL = orig }
}

// ReleaseTag returns the git tag a provider's version is published under —
// its own tag namespace, independent of inject core's `v<version>` tags.
func ReleaseTag(providerName, version string) string {
	return providerName + "/v" + version
}

// BinaryName is the executable name inside a provider's release archive.
func BinaryName(providerName, goos string) string {
	name := "inject-provider-" + providerName
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func releaseAssetURL(tag, filename string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", baseURL, githubRepo, tag, filename)
}

// FetchChecksums downloads and parses a release's checksums.txt (the
// standard `sha256sum`-style "<hex>  <filename>" format goreleaser
// produces) into a filename -> lowercase hex digest map.
func FetchChecksums(ctx context.Context, tag string) (map[string]string, error) {
	body, err := get(ctx, releaseAssetURL(tag, "checksums.txt"))
	if err != nil {
		return nil, fmt.Errorf("fetching checksums for %s: %w", tag, err)
	}

	sums := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sums[fields[1]] = strings.ToLower(fields[0])
	}
	return sums, nil
}

// FindAsset picks the checksums.txt entry for providerName on the given
// platform, matching by "_<goos>_<goarch>" appearing in the filename
// rather than predicting goreleaser's exact naming — one source of truth
// (the real, published checksums file) instead of two places that could
// drift out of sync.
func FindAsset(checksums map[string]string, providerName, goos, goarch string) (filename, digest string, err error) {
	prefix := "inject-provider-" + providerName + "_"
	suffix := fmt.Sprintf("_%s_%s", goos, goarch)
	for name, digest := range checksums {
		if strings.HasPrefix(name, prefix) && strings.Contains(name, suffix) {
			return name, digest, nil
		}
	}
	return "", "", fmt.Errorf("no released asset found for provider %q on %s/%s", providerName, goos, goarch)
}

// DownloadAndVerify downloads tag's asset and verifies its sha256 matches
// wantDigest (lowercase hex, no "sha256:" prefix — checksums.txt's own
// format), refusing to return anything on mismatch. This is the actual
// security-relevant integrity check: it catches a corrupted download or a
// tampered/replaced release asset, independent of TLS transport security.
func DownloadAndVerify(ctx context.Context, tag, filename, wantDigest string) ([]byte, error) {
	data, err := get(ctx, releaseAssetURL(tag, filename))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", filename, err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != wantDigest {
		return nil, fmt.Errorf("digest mismatch for %s: got %s, want %s from checksums.txt — refusing to install", filename, got, wantDigest)
	}
	return data, nil
}

// ExtractBinary extracts binaryName out of a .tar.gz/.tgz or .zip archive
// (chosen by archiveFilename's extension).
func ExtractBinary(archiveFilename string, archiveData []byte, binaryName string) ([]byte, error) {
	switch {
	case strings.HasSuffix(archiveFilename, ".tar.gz"), strings.HasSuffix(archiveFilename, ".tgz"):
		return extractFromTarGz(archiveData, binaryName)
	case strings.HasSuffix(archiveFilename, ".zip"):
		return extractFromZip(archiveData, binaryName)
	default:
		return nil, fmt.Errorf("unrecognized archive format: %s", archiveFilename)
	}
}

func extractFromTarGz(data []byte, binaryName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// Archive entry names always use "/", regardless of the host OS
		// that built or is reading the archive — path.Base, not filepath.Base.
		if path.Base(hdr.Name) == binaryName {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binaryName)
}

func extractFromZip(data []byte, binaryName string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range r.File {
		if path.Base(f.Name) != binaryName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("%s not found in archive", binaryName)
}

// Install downloads, verifies, and extracts providerName's version for
// (goos, goarch) into destPath (whose parent directory must already
// exist), returning "sha256:<hex>" of the extracted binary — the same
// format internal/lockfile records for a source-built install.
func Install(ctx context.Context, providerName, version, goos, goarch, destPath string) (digest string, err error) {
	tag := ReleaseTag(providerName, version)

	checksums, err := FetchChecksums(ctx, tag)
	if err != nil {
		return "", err
	}
	filename, wantDigest, err := FindAsset(checksums, providerName, goos, goarch)
	if err != nil {
		return "", err
	}
	archiveData, err := DownloadAndVerify(ctx, tag, filename, wantDigest)
	if err != nil {
		return "", err
	}

	binaryName := BinaryName(providerName, goos)
	binaryData, err := ExtractBinary(filename, archiveData, binaryName)
	if err != nil {
		return "", fmt.Errorf("extracting %s from %s: %w", binaryName, filename, err)
	}

	if err := writeExecutable(destPath, binaryData); err != nil {
		return "", err
	}

	sum := sha256.Sum256(binaryData)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func writeExecutable(path string, data []byte) error {
	return os.WriteFile(path, data, 0o755)
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
