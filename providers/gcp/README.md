# gcp provider

Covers GCP Secret Manager under one tag:

| Tag | GCP service | Example reference |
|---|---|---|
| `gcp-sm` | Secret Manager | `gcp-sm:prod-db-password` |

Installed as `.syringe/providers/gcp/inject-provider-gcp`, configured via `.syringe/providers/gcp/config.toml`. See the top-level README for how `inject` discovers and invokes providers in general — this file only covers what's specific to GCP.

## Setup

`inject init` builds this provider and then asks one question, once:

- **GCP project** — the project your secrets live in. Shows the project that would currently resolve from Application Default Credentials (e.g. what `gcloud auth application-default login` stored) as a hint, but always asks; it never silently inherits it. Type a project ID, or press enter to accept the shown hint.

Saved to `config.toml`, e.g.:

```toml
[env]
GOOGLE_CLOUD_PROJECT = 'my-project'
```

That file is plain TOML — hand-edit it any time (e.g. to switch projects) without re-running `init`. `init` requires a real interactive terminal; it fails fast rather than hanging if run from a script or CI, since it deliberately doesn't fall back to silently reading ambient env vars for this.

## Credentials

Always resolved via GCP's Application Default Credentials (ADC): `gcloud auth application-default login` for local development, a service account key file via `GOOGLE_APPLICATION_CREDENTIALS`, or the ambient GCE/GKE metadata server / workload identity in a deployed environment. This provider never stores, asks for, or otherwise handles raw credentials itself.

## Required IAM permissions

| Tag | Role or permission |
|---|---|
| `gcp-sm` | `roles/secretmanager.secretAccessor` (for `resolve`), `secretmanager.versions.get` (for `validate`) |

## JSON key selection

A secret is often stored as a single JSON object holding several key-value pairs. Append `#<key>` to select just one field instead of the whole JSON blob:

```
DB_USERNAME=gcp-sm:db-credentials#username
DB_PASSWORD=gcp-sm:db-credentials#password
```

- Omitting `#<key>` resolves the secret's raw value as-is (the whole JSON string, if that's what's stored).
- `#` is never a legal character in a GCP secret ID, so the split is unambiguous.
- A JSON `null` value is treated the same as a missing key (an error), never resolved to the literal string `"null"`.
- Non-string JSON values (numbers, booleans, nested objects/arrays) are re-encoded to their JSON text (e.g. a `port` field of `5432` resolves to `"5432"`).
- Unlike AWS, GCP Secret Manager has no separate string/binary secret type — a secret payload that isn't valid UTF-8 text resolves base64-encoded instead; requesting `#<key>` on one is an `invalid_ref` error, since it isn't JSON.

There's no version-pinning syntax yet — `resolve`/`validate` always use the secret's `latest` version.

## Error classification

GCP API errors are mapped to the core `Kind` taxonomy via the call's gRPC status code:

| gRPC code | `Kind` |
|---|---|
| `NotFound` | `not_found` |
| `PermissionDenied`, `Unauthenticated` | `access_denied` |
| `InvalidArgument`, `FailedPrecondition` | `invalid_ref` |
| anything else (`Unavailable`, `DeadlineExceeded`, `ResourceExhausted`/throttling, a non-gRPC error, ...) | `transient` |

Error messages are formatted as `<code>: <message>` extracted from the gRPC status, not the SDK's full transport wrapper chain.
