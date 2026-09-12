package provider

import (
	"errors"
	"fmt"
)

// Sentinel error kinds. Providers classify SDK-specific errors into one of
// these via NewError; callers compare with errors.Is.
var (
	ErrNotFound     = errors.New("secret not found")
	ErrAccessDenied = errors.New("access denied")
	ErrTransient    = errors.New("transient error")
	ErrInvalidRef   = errors.New("invalid reference syntax")
	ErrUnknownTag   = errors.New("unknown provider tag")
)

// Error is the structured error type returned by Provider implementations.
// It carries enough context for CLI output and validate reporting without
// ever embedding a resolved secret value.
type Error struct {
	Kind error  // one of the sentinels above
	Tag  string // provider tag, e.g. "aws-sm"
	Path string // the path that failed (never the resolved value)
	Err  error  // underlying wrapped error (SDK error), may be nil
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: resolving %s:%s: %v", e.Kind, e.Tag, e.Path, e.Err)
	}
	return fmt.Sprintf("%s: resolving %s:%s", e.Kind, e.Tag, e.Path)
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool { return errors.Is(e.Kind, target) }

// NewError constructs a classified provider error.
func NewError(kind error, tag, path string, cause error) *Error {
	return &Error{Kind: kind, Tag: tag, Path: path, Err: cause}
}
