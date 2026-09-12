package cmd

// providerNames maps a reference tag (as written in an .env file) to the
// shared provider identity that serves it. Multiple tags can point at one
// identity — aws-sm and aws-ssm both map to "aws" because they're really
// one provider (same AWS profile/region/credentials) covering two AWS
// services, not two separate providers. A tag with no entry here is
// presumed a literal value, not a typo (see internal/cmd/registry.go).
var providerNames = map[string]string{
	"aws-sm":  "aws",
	"aws-ssm": "aws",
	"gcp-sm":  "gcp-sm",
	"az-kv":   "az-kv",
}

// providerPackages maps a provider identity (not a tag) to the Go package
// (within this monorepo) that builds its binary. `inject init` builds from
// here until Phase 6 moves to downloading published release artifacts.
var providerPackages = map[string]string{
	"aws": "github.com/syringex/syringe/providers/aws/cmd/inject-provider-aws",
}
