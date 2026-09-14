// Package lockfile manages syringe.lock, a project-local record of exactly
// which provider version and binary digest `inject init` installed — the
// same role a package-lock.json or .terraform.lock.hcl plays: reproducibility
// and drift detection across a team. Unlike .syringe/, it's meant to be
// committed to version control, and it never contains a secret value.
package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// FileName is syringe.lock's conventional name, created in the current
// working directory (next to .env).
const FileName = "syringe.lock"

const currentVersion = 1

// Platform records one platform's installed binary digest for a provider.
type Platform struct {
	Digest string `json:"digest"`
}

// ProviderEntry records what's installed for one provider identity.
type ProviderEntry struct {
	Version   string              `json:"version"`
	Platforms map[string]Platform `json:"platforms"`
}

// Lockfile is the parsed shape of syringe.lock.
type Lockfile struct {
	LockfileVersion int                      `json:"lockfile_version"`
	Providers       map[string]ProviderEntry `json:"providers"`
}

// Load reads path, returning an empty (but valid) Lockfile if it doesn't
// exist yet — the normal case for a project's first `inject init`.
func Load(path string) (Lockfile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Lockfile{LockfileVersion: currentVersion, Providers: map[string]ProviderEntry{}}, nil
	}
	if err != nil {
		return Lockfile{}, err
	}

	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return Lockfile{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if lf.Providers == nil {
		lf.Providers = map[string]ProviderEntry{}
	}
	return lf, nil
}

// SetPlatform records/updates one provider's version and one platform's
// digest, preserving any other providers' and platforms' existing entries.
func (lf *Lockfile) SetPlatform(provider, version, platform, digest string) {
	if lf.Providers == nil {
		lf.Providers = map[string]ProviderEntry{}
	}
	entry := lf.Providers[provider]
	if entry.Platforms == nil {
		entry.Platforms = map[string]Platform{}
	}
	entry.Version = version
	entry.Platforms[platform] = Platform{Digest: digest}
	lf.Providers[provider] = entry
}

// Save writes lf to path as indented JSON, creating or overwriting it.
func (lf *Lockfile) Save(path string) error {
	if lf.LockfileVersion == 0 {
		lf.LockfileVersion = currentVersion
	}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// DigestFile returns the sha256 digest of the file at path, formatted as
// "sha256:<hex>" (the same convention container image digests use).
func DigestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
