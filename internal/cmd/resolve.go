package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syringex/syringe/internal/envfile"
	"github.com/syringex/syringe/internal/install"
	"github.com/syringex/syringe/internal/resolve"
)

// resolveEntries builds a registry for entries and resolves all of them.
// It never fails fast: callers inspect the returned Result for per-key
// success/failure so they can report every failing key, not just the
// first.
func resolveEntries(ctx context.Context, f *flags, entries []envfile.Entry) (*resolve.Result, error) {
	registry, err := buildRegistry(install.DirIn("."), entries)
	if err != nil {
		return nil, err
	}

	return resolve.Entries(ctx, registry, entries, resolve.Options{
		Concurrency:   f.concurrency,
		PerRefTimeout: f.timeout,
	})
}

// resolveAll parses f.file and resolves every entry in it.
func resolveAll(ctx context.Context, f *flags) (*resolve.Result, error) {
	entries, err := envfile.ParseFile(f.file)
	if err != nil {
		return nil, err
	}
	return resolveEntries(ctx, f, entries)
}

// formatResolveErrors turns a Result's failures into a single error listing
// every failing key and its (value-free) error message.
func formatResolveErrors(result *resolve.Result) error {
	var b strings.Builder
	for _, e := range result.Failed() {
		fmt.Fprintf(&b, "%s: %s\n", e.Key, e.Err)
	}
	return withExitCode(1, errors.New(strings.TrimRight(b.String(), "\n")))
}
