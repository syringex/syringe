// Package parser parses .env-style files whose values may be plain literals
// or "tag:path" references to a secret in a remote provider. It has no
// knowledge of which tags are actually registered — that decision belongs
// to the resolver, so a parsed Ref may turn out to be a typo'd tag that
// falls back to being treated as a literal value.
package parser

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/syringex/syringe/pkg/provider"
)

// Entry is one KEY=VALUE line, in file order.
type Entry struct {
	Key      string
	RawValue string
	// Ref is non-nil when RawValue is shaped like "tag:path". The tag is
	// not yet validated against any registry.
	Ref  *provider.Ref
	Line int
}

// ParseError carries source line context for user-facing diagnostics.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

var (
	keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tagPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

// ParseFile is a convenience wrapper around os.Open + Parse.
func ParseFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads a .env-style file and returns entries in file order. It does
// not resolve anything — pure syntax parsing. Supported grammar:
//
//   - one KEY=VALUE per line, value taken verbatim after the first '='
//   - optional leading "export " before KEY
//   - lines starting with '#' (after trimming leading whitespace) are
//     comments
//   - blank lines are skipped
//   - duplicate keys: last occurrence wins, earlier ones are silently
//     shadowed (callers that care, e.g. `validate`, can detect duplicates
//     themselves from the returned line numbers)
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")
		trimmed = strings.TrimSpace(trimmed)

		eq := strings.Index(trimmed, "=")
		if eq < 0 {
			return nil, &ParseError{Line: lineNo, Msg: fmt.Sprintf("missing '=' in %q", line)}
		}
		key := strings.TrimSpace(trimmed[:eq])
		value := trimmed[eq+1:]

		if !keyPattern.MatchString(key) {
			return nil, &ParseError{Line: lineNo, Msg: fmt.Sprintf("invalid key %q", key)}
		}

		entries = append(entries, Entry{
			Key:      key,
			RawValue: value,
			Ref:      ParseRef(value),
			Line:     lineNo,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// ParseRef inspects a raw value and, if it looks like "tag:path" (tag being
// a lowercase-kebab-case token), returns a *provider.Ref. Otherwise it
// returns nil, meaning the value should be treated as a literal. The tag is
// not checked against any registry here.
func ParseRef(raw string) *provider.Ref {
	colon := strings.Index(raw, ":")
	if colon <= 0 {
		return nil
	}
	tag := raw[:colon]
	if !tagPattern.MatchString(tag) {
		return nil
	}
	return &provider.Ref{
		Tag:  tag,
		Path: raw[colon+1:],
		Raw:  raw,
	}
}
