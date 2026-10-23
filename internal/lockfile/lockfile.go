// Package lockfile manages syringe.lock, a project-local record of exactly
// which provider version and binary digest `inject init` installed — the
// same role a package-lock.json or .terraform.lock.hcl plays: reproducibility
// and drift detection across a team. Unlike .syringe/, it's meant to be
// committed to version control, and it never contains a secret value.
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
)

// Name is syringe.lock's conventional name, created in the current
// working directory (next to .env).
const Name = "syringe.lock"

const currentVersion = 1

// Platform records one platform's installed binary digest for a provider.
type Platform struct {
	Digest string `json:"digest"`
}

// Provider records what's installed for one provider identity.
type Provider struct {
	Version   string              `json:"version"`
	Platforms map[string]Platform `json:"platforms"`
}

// File is the parsed shape of syringe.lock.
type File struct {
	// Version is syringe.lock's own format version, not a provider's.
	Version   int                 `json:"lockfile_version"`
	Providers map[string]Provider `json:"providers"`
}

// Load reads path, returning an empty (but valid) File if it doesn't
// exist yet — the normal case for a project's first `inject init`.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return File{Version: currentVersion, Providers: map[string]Provider{}}, nil
	}
	if err != nil {
		return File{}, err
	}

	var lf File
	if err := json.Unmarshal(data, &lf); err != nil {
		return File{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if lf.Providers == nil {
		lf.Providers = map[string]Provider{}
	}
	return lf, nil
}

// SetPlatform records/updates one provider's version and one platform's
// digest, preserving any other providers' and platforms' existing entries.
func (lf *File) SetPlatform(provider, version, platform, digest string) {
	if lf.Providers == nil {
		lf.Providers = map[string]Provider{}
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
func (lf *File) Save(path string) error {
	if lf.Version == 0 {
		lf.Version = currentVersion
	}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
