# Contributing

## Adding a new provider

A provider is a standalone executable that speaks the wire protocol in
`pkg/providerproto`. Adding one to this repo means:

1. Write the provider binary under `providers/<name>/cmd/inject-provider-<name>/main.go`,
   implementing the `resolve <tag> <path>`, `validate <tag> <path>`, and
   `init` verbs described in `pkg/providerproto`. See `providers/aws` for a
   full example, including how one provider identity can serve more than one
   reference tag (`aws-sm`, `aws-ssm`).
2. Add an entry to `providers/manifest.json`:
   ```json
   "gcp": {
     "version": "0.1.0",
     "tags": ["gcp-sm"],
     "package": "github.com/syringex/syringe/providers/gcp/cmd/inject-provider-gcp"
   }
   ```
   This is the only place `inject init`, the registry, and CI learn about a
   provider — no other code changes are needed to make it buildable and
   installable.
3. That's it for release wiring — `.github/workflows/release-provider.yml`
   reads a provider's package path straight out of `providers/manifest.json`
   generically, so there's no per-provider release config to add or
   maintain.
4. Add a `providers/<name>/README.md` documenting that provider's specific
   setup (what `init` prompts for, required IAM/API permissions, any
   reference-syntax extensions like AWS's `#key` JSON selection).

At this point the provider builds from source via `inject init` in any local
monorepo checkout (dev builds always build from source — see `moduleDir` in
`internal/cmd/init.go`) and is ready to be released (next section) so a real
distributed `inject` binary can download it too.

## Releasing a new version of an existing provider

Providers are versioned and released independently of `inject` core and of
each other — closer to how Terraform providers work than a single combined
release. `inject` can be at `v0.1.2` while `aws` is at `aws/v0.1.1` and a
future `gcp` provider is at `gcp/v0.1.0`; nothing ties these together.

Releasing is entirely automatic once a version bump reaches `main` — there
is no manual tagging step and nothing to install locally:

1. Whenever you change a provider's code, bump that provider's `version`
   field in `providers/manifest.json` in the same PR. This is the release
   trigger, so treat "I changed `providers/<name>/`" and "I bumped
   `providers/<name>`'s version in the manifest" as the same step.
2. Merge the PR to `main` as normal.
3. `.github/workflows/tag-provider-releases.yml` runs on every push to
   `main` that touches `providers/manifest.json`. For each provider, it
   checks whether a `<name>/v<version>` tag already exists for the
   manifest's current version; if not, it creates and pushes that tag.
4. Pushing that tag triggers `.github/workflows/release-provider.yml`, which
   cross-compiles that provider for every supported platform, archives and
   checksums the result, and publishes a GitHub Release named
   `<name>/v<new-version>` with the provider's binaries, archives, and a
   `checksums.txt`.

`inject init` on a real distributed binary resolves this exact tag: it
fetches that release's `checksums.txt`, verifies the downloaded archive's
digest against it, and only then installs the extracted binary — see
`internal/providerdownload`.

A provider version only becomes installable by anyone else once this has
actually run on `main`. Bumping `manifest.json` on a branch that hasn't
merged yet, or building locally, only ever affects your own monorepo dev
build (see below) — nothing is tagged or released until `main` moves.

## `syringe.lock` and local dev builds

`syringe.lock` records the exact version and digest of each provider
`inject init` installed, so a team installs the same thing everywhere — like
`package-lock.json` or `.terraform.lock.hcl`.

If you run `inject init` from a local monorepo checkout (`moduleDir` set),
the binary is built from whatever source is currently on disk — including
uncommitted or unreleased changes — and the lock file's digest reflects
*that* build, not necessarily any real published release. Don't commit a
`syringe.lock` produced this way unless the version it names has actually
been tagged and released per the steps above; otherwise the recorded digest
won't match what anyone else's `inject init` downloads for that same
version.
