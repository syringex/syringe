// Package providerproto defines the JSON wire protocol between the inject
// core binary and a provider executable (e.g. inject-provider-aws), invoked
// as `<binary> resolve <tag> <path>`, `<binary> validate <tag> <path>`, or
// `<binary> init` (no tag or path).
//
// tag is the reference tag the call is for (e.g. "aws-sm" or "aws-ssm").
// inject always passes it explicitly because one installed provider binary
// can serve more than one tag — e.g. a single "aws" provider identity
// covering both AWS Secrets Manager and SSM Parameter Store, since they
// share the same profile/region/credentials and shouldn't be installed or
// configured separately. A provider that only ever serves one tag can
// simply ignore the argument. "init" has no tag: it configures whatever is
// shared across every tag that provider identity serves, once.
//
// On success a provider prints exactly one line of JSON to stdout and
// exits 0: ResolveResult for "resolve", ValidateResult for "validate",
// InitResult for "init". On failure it prints exactly one line of JSON
// ErrorEnvelope to stdout and exits non-zero. A provider must never write
// the secret value anywhere but ResolveResult.Value on success — not to
// logs, not folded into an error message.
//
// "init" is the one exception to "just stdout": inject connects its stdin
// and stderr directly to the real terminal so it can prompt the user
// interactively, but its *final* line of stdout must still be a single
// InitResult (or ErrorEnvelope) exactly like the other two verbs.
package providerproto

// Kind classifies why a resolve/validate call failed, mirroring
// pkg/provider's error sentinels as a language-agnostic string so a
// provider written in any language can produce it.
type Kind string

const (
	KindNotFound     Kind = "not_found"
	KindAccessDenied Kind = "access_denied"
	KindTransient    Kind = "transient"
	KindInvalidRef   Kind = "invalid_ref"
)

// ResolveResult is printed on stdout after a successful "resolve <path>".
type ResolveResult struct {
	Value string `json:"value"`
}

// ValidateResult is printed on stdout after a successful "validate <path>".
type ValidateResult struct {
	OK bool `json:"ok"`
}

// InitResult is printed on stdout after a successful "init" call (no path
// argument). Env holds environment variables inject should persist and set
// on every future invocation of this provider — for ancillary SDK
// configuration only (e.g. a region), never credentials. A provider that
// finds it already has everything it needs returns an empty map and must
// not prompt for anything.
type InitResult struct {
	Env map[string]string `json:"env"`
}

// ErrorEnvelope is printed on stdout (alongside a non-zero exit code) when
// a resolve or validate call fails.
type ErrorEnvelope struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload carries a classified error kind and a human-readable
// message. Message must never contain a secret value.
type ErrorPayload struct {
	Kind    Kind   `json:"kind"`
	Message string `json:"message"`
}
