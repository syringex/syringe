package cmd

import "github.com/syringex/syringe/providers"

// providerNames maps a reference tag (as written in an .env file) to the
// shared provider identity that serves it — derived from
// providers/manifest.json, the single source of truth. A tag with no entry
// here is presumed a literal value, not a typo (see internal/cmd/registry.go).
var providerNames map[string]string

// providerPackages maps a provider identity (not a tag) to the Go package
// (within this monorepo) that builds its binary. `inject init` builds from
// here until Phase 6 moves to downloading published release artifacts.
var providerPackages map[string]string

// providerVersions maps a provider identity to its manifest-declared
// version — embedded into its binary at build time and recorded in
// syringe.lock, independent of inject core's own version.
var providerVersions map[string]string

func init() {
	m, err := providers.Load()
	if err != nil {
		// The manifest is embedded at build time: a parse failure here
		// means this repo's own providers/manifest.json is broken, not
		// something a caller can recover from at runtime.
		panic("cmd: " + err.Error())
	}

	providerNames = make(map[string]string)
	providerPackages = make(map[string]string)
	providerVersions = make(map[string]string)
	for name, p := range m.Providers {
		providerPackages[name] = p.Package
		providerVersions[name] = p.Version
		for _, tag := range p.Tags {
			providerNames[tag] = name
		}
	}
}
