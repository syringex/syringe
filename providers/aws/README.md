# aws provider

Covers two AWS secret backends as one provider identity, `aws`, since they share the same profile/region/credentials and there's no reason to install or configure them separately:

| Tag | AWS service | Example reference |
|---|---|---|
| `aws-sm` | Secrets Manager | `aws-sm:prod/db/password` |
| `aws-ssm` | SSM Parameter Store | `aws-ssm:/myapp/api-key` |

Installed as `.syringe/providers/aws/inject-provider-aws`, configured once via `.syringe/providers/aws/config.toml` regardless of which of the two tags your `.env` uses. See the top-level README for how `inject` discovers and invokes providers in general — this file only covers what's specific to AWS.

## Setup

`inject init` builds this provider and then asks two questions, once:

- **AWS profile** — leave blank to use the default credential chain (`AWS_PROFILE` env var, or the `default` profile). Never asks for actual credentials, just which already-configured profile to use.
- **AWS region** — shows the region that would currently resolve (from `~/.aws/config`, `AWS_REGION`, etc.) as a hint, but always asks; it never silently inherits it. Type a region, or press enter to accept the shown hint.

Both are saved to `config.toml`, e.g.:

```toml
[env]
AWS_PROFILE = 'personal'
AWS_REGION = 'us-east-1'
```

That file is plain TOML — hand-edit it any time (e.g. to switch profiles or regions) without re-running `init`. `init` requires a real interactive terminal; it fails fast rather than hanging if run from a script or CI, since it deliberately doesn't fall back to silently reading ambient env vars for this.

## Credentials

Always resolved via the standard AWS SDK default credential chain (environment variables, `~/.aws/credentials`, IAM roles, SSO, ...) for whichever profile is configured above. This provider never stores, asks for, or otherwise handles raw credentials itself.

## Required IAM permissions

| Tag | Action |
|---|---|
| `aws-sm` | `secretsmanager:GetSecretValue` |
| `aws-ssm` | `ssm:GetParameter` |

(`secretsmanager:DescribeSecret` and `ssm:DescribeParameters` are implemented for a future value-free `check`/`validate` path — see the top-level README's open items — but aren't exercised by the CLI yet.)

## JSON key selection

AWS Secrets Manager (and SSM Parameter Store) secrets are often stored as a single JSON object holding several key-value pairs — e.g. AWS's own RDS-managed secrets: `{"username":"...","password":"...","host":"..."}`. Append `#<key>` to select just one field instead of the whole JSON blob:

```
DB_USERNAME=aws-sm:prod/db/credentials#username
DB_PASSWORD=aws-sm:prod/db/credentials#password
```

- Omitting `#<key>` resolves the secret's raw value as-is (the whole JSON string, if that's what's stored).
- `#` is never a legal character in an AWS secret or parameter name, so the split is unambiguous.
- A JSON `null` value is treated the same as a missing key (an error), never resolved to the literal string `"null"`.
- Non-string JSON values (numbers, booleans, nested objects/arrays) are re-encoded to their JSON text (e.g. a `port` field of `5432` resolves to `"5432"`).
- A binary secret (`SecretBinary`) resolves base64-encoded; requesting `#<key>` on one is an `invalid_ref` error, since it isn't JSON.

For SSM Parameter Store, AWS's own `name:version` / `name:label` selector syntax already works with no extra code — the name is passed straight through to AWS, which parses it natively — and composes with `#<key>`:

```
DB_PASSWORD=aws-ssm:/myapp/db-creds:3#password
```

## Error classification

AWS SDK errors are mapped to the core `Kind` taxonomy via the service's modeled error code:

| AWS error code | `Kind` |
|---|---|
| `ResourceNotFoundException`, `ParameterNotFound` | `not_found` |
| `AccessDeniedException`, `UnauthorizedException`, `AuthFailure` | `access_denied` |
| `InvalidRequestException`, `InvalidParameterException`, `InvalidKeyId` | `invalid_ref` |
| anything else (throttling, 5xx, network, missing credentials, ...) | `transient` |

Error messages are formatted as `<code>: <message>` extracted from the AWS SDK's modeled error, not its full operation/transport wrapper chain.
