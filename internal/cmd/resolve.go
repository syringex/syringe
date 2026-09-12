package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syringex/syringe/internal/parser"
	"github.com/syringex/syringe/internal/providerdir"
	"github.com/syringex/syringe/internal/resolver"
)

// resolveEntries builds a registry for entries and resolves all of them.
// It never fails fast: callers inspect the returned Result for per-key
// success/failure so they can report every failing key, not just the
// first.
func resolveEntries(ctx context.Context, f *flags, entries []parser.Entry) (*resolver.Result, error) {
	dir := providerdir.Dir(".")
	registry, err := buildRegistry(dir, entries)
	if err != nil {
		return nil, err
	}

	r := resolver.New(registry, resolver.Options{
		Concurrency:   f.concurrency,
		PerRefTimeout: f.timeout,
	})
	return r.Resolve(ctx, entries)
}

// resolveAll parses f.file and resolves every entry in it.
func resolveAll(ctx context.Context, f *flags) (*resolver.Result, error) {
	entries, err := parser.ParseFile(f.file)
	if err != nil {
		return nil, err
	}
	return resolveEntries(ctx, f, entries)
}

// formatResolveErrors turns a Result's failures into a single error listing
// every failing key and its (value-free) error message.
func formatResolveErrors(result *resolver.Result) error {
	var b strings.Builder
	for _, e := range result.Failed() {
		fmt.Fprintf(&b, "%s: %s\n", e.Key, e.Err)
	}
	return withExitCode(1, errors.New(strings.TrimRight(b.String(), "\n")))
}
