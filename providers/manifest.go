// Package providers embeds providers/manifest.json, the single source of
// truth for which providers exist, which reference tags each serves, its
// current version, and the Go package that builds it. Both inject itself
// (via this package) and CI (via `jq` directly on the JSON file, no Go
// build needed) read from this one file.
package providers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
)

//go:embed manifest.json
var manifestJSON []byte

// Provider describes one provider identity as declared in manifest.json.
type Provider struct {
	// Version is this provider's own version, independent of inject core's
	// version — bumped whenever the provider's implementation changes
	// meaningfully. Embedded into its binary at build time and recorded in
	// syringe.lock.
	Version string `json:"version"`
	// Tags are the reference tags (as written in an .env file) this
	// provider identity serves. Multiple tags can share one identity when
	// they share configuration/credentials (aws-sm and aws-ssm both need
	// just an AWS profile/region) — see cmd/inject/registry.go.
	Tags []string `json:"tags"`
	// Package is the Go package (within this monorepo) that builds this
	// provider's binary.
	Package string `json:"package"`
}

// Manifest is the parsed shape of manifest.json.
type Manifest struct {
	Providers map[string]Provider `json:"providers"`
}

// Load parses the embedded manifest.
func Load() (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return Manifest{}, fmt.Errorf("providers: parsing embedded manifest.json: %w", err)
	}
	return m, nil
}

// MustLoad is like Load but panics if the embedded manifest fails to parse.
// The manifest is fixed at build time (and pinned by this package's tests),
// so a failure means the binary was built from a broken manifest.json, not
// something a caller can recover from at runtime.
func MustLoad() Manifest {
	m, err := Load()
	if err != nil {
		panic(err)
	}
	return m
}

// ProviderFor returns the identity of the provider that serves tag (as
// written in an .env file), e.g. "aws" for both "aws-sm" and "aws-ssm".
// ok is false for a tag no provider serves.
func (m Manifest) ProviderFor(tag string) (name string, ok bool) {
	for name, p := range m.Providers {
		if slices.Contains(p.Tags, tag) {
			return name, true
		}
	}
	return "", false
}
