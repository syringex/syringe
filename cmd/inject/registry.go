package main

import (
	"fmt"
	"slices"

	"github.com/syringex/syringe/internal/envfile"
	"github.com/syringex/syringe/internal/execprovider"
	"github.com/syringex/syringe/internal/install"
	"github.com/syringex/syringe/internal/provider"
)

// requiredTags returns the distinct, sorted set of tags entries reference.
func requiredTags(entries []envfile.Entry) []string {
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Ref != nil {
			seen[e.Ref.Tag] = true
		}
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	slices.Sort(tags)
	return tags
}

// requiredProviderNames returns the distinct, sorted set of provider
// identities that entries' referenced tags map to (tags with no known
// provider identity are skipped — presumed literals, not typos). Multiple
// tags can share one identity (aws-sm and aws-ssm both need just "aws"),
// so this can return fewer entries than requiredTags.
func requiredProviderNames(entries []envfile.Entry) []string {
	seen := map[string]bool{}
	for _, tag := range requiredTags(entries) {
		if name, ok := manifest.ProviderFor(tag); ok {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// buildRegistry constructs a provider.Registry for entries' references,
// backed by binaries installed under dir. A tag no provider in manifest
// serves is left as a literal (most likely coincidental, e.g.
// "note:see-section-3"). A known tag whose provider identity has no
// installed binary is a clear error pointing at `inject init`, rather than
// a silent, probably-unintended literal fallback.
func buildRegistry(dir install.Dir, entries []envfile.Entry) (*provider.Registry, error) {
	registry := &provider.Registry{}
	var missing []string

	for _, tag := range requiredTags(entries) {
		name, ok := manifest.ProviderFor(tag)
		if !ok {
			continue
		}
		if dir.IsInstalled(name) {
			extraEnv, err := dir.LoadEnv(name)
			if err != nil {
				return nil, fmt.Errorf("loading config for %q: %w", name, err)
			}
			binPath := dir.BinaryPath(name)
			if err := registry.Register(&execprovider.Provider{TagName: tag, Binary: binPath, Env: extraEnv}); err != nil {
				return nil, err
			}
			continue
		}
		missing = append(missing, tag)
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("provider(s) not installed for tag(s) %v — run `inject init`", missing)
	}
	return registry, nil
}
