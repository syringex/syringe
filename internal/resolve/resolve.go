// Package resolve fans a parsed .env file out to registered providers
// concurrently, aggregating per-key results (including partial failures)
// while preserving the file's original key order.
package resolve

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/syringex/syringe/internal/envfile"
	"github.com/syringex/syringe/internal/provider"
)

const defaultConcurrency = 8

// Options configures Entries.
type Options struct {
	// Concurrency bounds how many secrets are resolved at once. <= 0 uses
	// defaultConcurrency.
	Concurrency int
	// PerRefTimeout, if > 0, is applied as a context deadline around each
	// individual Resolve call.
	PerRefTimeout time.Duration
	// FailFast, when true, cancels all in-flight and pending resolutions as
	// soon as one fails, and Entries returns that error. When false, every
	// entry is attempted independently and failures are reported per-key in
	// Result.Failed() without aborting the batch.
	FailFast bool
}

// Entries resolves every entry against registry, in parallel bounded by
// opts.Concurrency.
// The returned Result's Entries slice matches entries' order regardless of
// completion order. When opts.FailFast is true, the first error also
// short-circuits remaining work and is returned as the second value;
// otherwise the error return is always nil and failures are found via
// Result.Failed()/HasErrors().
func Entries(ctx context.Context, registry *provider.Registry, entries []envfile.Entry, opts Options) (*Result, error) {
	results := make([]Entry, len(entries))

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	if opts.FailFast {
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(concurrency)
		for i, e := range entries {
			g.Go(func() error {
				re, err := resolveOne(gctx, registry, e, opts.PerRefTimeout)
				results[i] = re
				return err
			})
		}
		if err := g.Wait(); err != nil {
			return &Result{Entries: results}, err
		}
		return &Result{Entries: results}, nil
	}

	var g errgroup.Group
	g.SetLimit(concurrency)
	for i, e := range entries {
		g.Go(func() error {
			re, _ := resolveOne(ctx, registry, e, opts.PerRefTimeout)
			results[i] = re
			return nil // never fail the group; failures are recorded per-entry
		})
	}
	_ = g.Wait()
	return &Result{Entries: results}, nil
}

// resolveOne resolves a single entry. A nil Ref, or a Ref whose tag isn't
// registered, is treated as a literal value (backward-compatible fallback
// for typo'd or unrecognized "tag:"-shaped values).
func resolveOne(
	ctx context.Context,
	registry *provider.Registry,
	e envfile.Entry,
	timeout time.Duration,
) (Entry, error) {
	if e.Ref == nil {
		return Entry{Key: e.Key, Value: e.RawValue, Line: e.Line}, nil
	}

	p, ok := registry.Get(e.Ref.Tag)
	if !ok {
		return Entry{Key: e.Key, Value: e.RawValue, Line: e.Line}, nil
	}

	resolveCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		resolveCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	secret, err := p.Resolve(resolveCtx, e.Ref.Path)
	if err != nil {
		return Entry{Key: e.Key, Err: err, Line: e.Line}, err
	}
	return Entry{Key: e.Key, Value: secret.Reveal(), Line: e.Line}, nil
}
