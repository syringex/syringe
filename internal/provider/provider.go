// Package provider defines the contract secret backends implement, plus the
// registry that maps a reference tag (e.g. "aws-sm") to an implementation.
package provider

import "context"

// Ref is a parsed reference, e.g. "aws-sm:prod/db/password" splits into
// Tag="aws-sm", Path="prod/db/password".
type Ref struct {
	Tag  string
	Path string
	Raw  string
}

// Provider is the pluggable contract every secret backend implements.
// Implementations must be safe for concurrent use: the resolver calls
// Resolve/Validate concurrently across many refs.
type Provider interface {
	// Tag returns the reference tag this implementation handles (e.g. "aws-sm").
	Tag() string

	// Resolve fetches the secret value at path. It must respect ctx
	// cancellation/deadline. Errors should be classified via NewError so
	// callers can distinguish not-found/access-denied/transient/invalid.
	Resolve(ctx context.Context, path string) (Secret, error)

	// Validate checks that path exists and is reachable, without requiring
	// the caller to hold onto the resolved value. Implementations without a
	// cheaper existence check may resolve and discard the value.
	Validate(ctx context.Context, path string) error
}

// Secret wraps a resolved value. A dedicated type (rather than a bare
// string) keeps accidental fmt/log exposure from leaking a value: String()
// redacts, and Reveal() is the one explicit, grep-able way to unwrap it.
type Secret struct {
	Value string
}

// Reveal returns the underlying plaintext value.
func (s Secret) Reveal() string { return s.Value }

// String implements fmt.Stringer with redaction so fmt.Println(secret) or a
// "%v" in a log line never leaks the value.
func (s Secret) String() string { return "<redacted>" }
