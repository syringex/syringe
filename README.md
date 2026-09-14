# inject

A CLI that resolves secret *references* in an `.env`-style file instead of storing plaintext secrets, and injects the resolved values into a command's environment (or prints them, or just checks they're resolvable).

```
DB_PASSWORD=aws-sm:prod/db/password
API_KEY=aws-ssm:/myapp/api-key
PLAIN_VALUE=this-is-just-a-normal-value
```

A value shaped like `tag:path` is treated as a reference to a secret manager; anything else is a plain literal, so existing `.env` files keep working unchanged.

Each provider has its own README covering what's specific to it (supported tags, setup, required permissions, any extra reference syntax): [`providers/aws`](providers/aws/README.md).

## Status

- **Providers implemented:** one AWS provider, covering both AWS Secrets Manager (`aws-sm`) and AWS SSM Parameter Store (`aws-ssm`) — installed, configured, and built once (see [`providers/aws/README.md`](providers/aws/README.md)). GCP Secret Manager and Azure Key Vault are recognized as known tags but have no provider yet — referencing them will tell you so.
- **Provider installation:** in a local monorepo checkout, `inject init` builds provider binaries from source (`go build`) into a project-local `.syringe/` directory, using [`providers/manifest.json`](providers/manifest.json) to know what to build and at what version. In a real, distributed `inject` binary, `init` instead downloads and checksum-verifies that provider's own released binary — see [CONTRIBUTING.md](CONTRIBUTING.md) for how a provider gets released, and `internal/providerdownload` for how a download is verified.
- **Distribution:** `.goreleaser.yaml` is ready for a real tagged release (not yet wired to CI); `.github/workflows/pr.yml` and `.github/workflows/merge.yml` build a linux/darwin/windows × amd64/arm64 matrix as artifacts on every PR and merge to `main`.

## How it works

`inject` itself never talks to any cloud SDK. Each *provider identity* (e.g. `aws`) is a separate executable, `inject-provider-aws`, invoked as a subprocess. This keeps the core binary small regardless of how many providers exist, and means a provider's SDK dependency never bloats anyone's `inject` binary unless their `.env` actually references it.

A reference tag (what you write in `.env`, e.g. `aws-sm` or `aws-ssm`) and a provider identity (what gets installed/configured) aren't necessarily one-to-one: `aws-sm` and `aws-ssm` both map to one `aws` provider identity, since they share the same AWS profile/region/credentials and there's no reason to install or configure them separately. `inject` always tells the provider which tag a given call is for, so one binary can still act differently per tag.

Providers live in a project-local `.syringe/` directory (next to your `.env`), one subdirectory per *provider identity*:

```
.syringe/providers/
  aws/
    inject-provider-aws   # the executable — handles both aws-sm and aws-ssm
    config.toml           # one shared profile/region config for both tags
```

`inject init` builds/configures this once even if your `.env` references both `aws-sm:` and `aws-ssm:`. This tag→provider grouping is an implementation detail of the AWS provider specifically — a third-party provider author is free to have a 1:1 tag↔binary mapping instead, which is the default assumption unless tags genuinely share configuration like AWS's do.

### The provider manifest and `syringe.lock`

[`providers/manifest.json`](providers/manifest.json) is the single source of truth for which providers exist — their identity, the reference tags they serve, their own version (independent of `inject` core's version), and the Go package that builds them:

```json
{
  "providers": {
    "aws": {
      "version": "0.1.0",
      "tags": ["aws-sm", "aws-ssm"],
      "package": "github.com/syringex/syringe/providers/aws/cmd/inject-provider-aws"
    }
  }
}
```

`inject` embeds this file at build time; CI reads it directly with `jq`. Adding a new provider means adding one entry here — `inject init`, the tag registry, and CI's build matrix all pick it up with no other code changes.

Every time `inject init` (re)builds or confirms a provider, it records the provider's manifest version and a sha256 digest of the exact installed binary in `syringe.lock`, at the project root:

```json
{
  "lockfile_version": 1,
  "providers": {
    "aws": {
      "version": "0.1.0",
      "platforms": {
        "darwin_arm64": { "digest": "sha256:..." }
      }
    }
  }
}
```

Unlike `.syringe/`, **`syringe.lock` is meant to be committed** — like `package-lock.json` or `.terraform.lock.hcl`, it's how a team gets reproducible, verifiable provider installs rather than "whatever `go build` happened to produce on someone's machine." It's written/updated only by `inject init` today; nothing yet re-verifies an installed binary against it before use.

## Building

```
make build      # builds ./inject with this repo's path baked in, needed for `inject init` to work locally
make test       # go test ./...
```

## Usage

Given a `.env` file in the current directory:

```sh
# Install the provider binaries your .env file actually references
inject init

# Run a command with resolved secrets injected into its environment
inject -- node index.js

# Fetch a single resolved value, e.g. for scripting
SECRET=$(inject get DB_PASSWORD)

# Check every reference resolves, without ever printing a value
inject check

# Print every resolved KEY=VALUE (shell/dotenv/json), e.g. for eval "$(inject export)"
inject export
inject export --format dotenv
inject export --format json
```

Common flags (on every subcommand): `--file/-f` (default `.env`), `--concurrency` (default 8), `--timeout` (default 10s), `--verbose`.

A provider that needs configuration beyond credentials (e.g. AWS needs a profile and region) prompts for it interactively the first time `inject init` runs — it never silently inherits whatever your AWS CLI already has configured, even if that would technically work, so your project's choice stays explicit and stops depending on ambient machine state. It never prompts for credentials, which always come from that provider's own default credential chain (AWS SDK default chain, GCP ADC, Azure `DefaultAzureCredential`, ...) — a profile *name* is fine to ask for since it just points at credentials already set up elsewhere, not the credentials themselves. The answers are saved to `.syringe/providers/<provider>/config.toml` and reused automatically; that file is plain TOML and safe to hand-edit (e.g. to point at a different profile or region than what you originally typed).

`inject init` requires a real interactive terminal for this step — running it non-interactively (e.g. from a script or CI) will fail with a clear message rather than hang; hand-write `config.toml` for unattended installs.

## Writing a provider

A provider is any executable named `inject-provider-<name>` (`<name>` is its provider identity, e.g. `aws`, not necessarily a single reference tag) that implements this protocol (see `pkg/providerproto` for the exact Go types — the protocol itself is language-agnostic):

| Invocation | On success (exit 0, one line of JSON on stdout) | On failure (exit non-zero, one line of JSON on stdout) |
|---|---|---|
| `inject-provider-<name> resolve <tag> <path>` | `{"value":"..."}` | `{"error":{"kind":"...","message":"..."}}` |
| `inject-provider-<name> validate <tag> <path>` | `{"ok":true}` | same error shape |
| `inject-provider-<name> init` | `{"env":{"SOME_VAR":"..."}}` (empty map if nothing's needed) | same error shape |

`tag` is the reference tag the call is for (e.g. `aws-sm` or `aws-ssm`) — `inject` always passes it explicitly, since one provider identity can serve more than one tag. If your provider only ever serves one tag, just ignore the argument. `init` has no tag: it configures whatever's shared across every tag that identity serves, once.

`kind` is one of `not_found`, `access_denied`, `transient`, `invalid_ref`.

Rules:
- Never write the secret value anywhere except `resolve`'s `value` field on success — not to logs, not folded into an error message.
- `init` is the one verb where `inject` connects your stdin/stderr to the real terminal, so you can prompt the user directly — but only for *ancillary* configuration (a profile, a region, a project ID, ...). Never prompt for credentials themselves (a profile/account *name* is fine). If stdin isn't an interactive terminal, fail fast with a clear message instead of blocking — don't fall back to reading ambient env vars in that case either.
- Prefer always asking over silently trusting whatever's ambient on the machine (e.g. don't skip a region prompt just because one happens to already be configured) — a project's config should stay explicit and stable regardless of what changes on the host later. Showing the value that *would* currently resolve as a hint the user can accept is good UX; applying it without asking isn't.
- Whatever `init` returns is persisted verbatim by `inject` and merged into your environment on every future `resolve`/`validate`/`init` call for that provider identity, so `resolve`/`validate` usually need no special code to pick it up (e.g. setting `AWS_REGION` is exactly what the AWS SDK's default chain already looks for).
- Only fold multiple tags into one provider identity when they genuinely share configuration/credentials (like `aws-sm`/`aws-ssm` do) — the default assumption for an unrelated provider is one tag, one identity, one binary.
