package cmd

import "github.com/syringex/syringe/providers"

// manifest is providers/manifest.json, the single source of truth for which
// provider identity serves each reference tag, each provider's version
// (embedded into its binary at build time and recorded in syringe.lock,
// independent of inject core's own version), and the Go package within this
// monorepo that builds it. A tag no provider serves is presumed a literal
// value, not a typo (see registry.go).
var manifest = providers.MustLoad()
