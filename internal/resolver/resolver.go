// Package resolver fans a parsed .env file out to registered providers
// concurrently, aggregating per-key results (including partial failures)
// while preserving the file's original key order.
package resolver

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/syringex/syringe/internal/parser"
	"github.com/syringex/syringe/pkg/provider"
)

const defaultConcurrency = 8

// Options configures a Resolver.
type Options struct {
	// Concurrency bounds how many secrets are resolved at once. <= 0 uses
	// defaultConcurrency.
	Concurrency int
	// PerRefTimeout, if > 0, is applied as a context deadline around each
	// individual Resolve call.
	PerRefTimeout time.Duration
	// FailFast, when true, cancels all in-flight and pending resolutions as
	// soon as one fails, and Resolve returns that error. When false, every
	// entry is attempted independently and failures are reported per-key in
	// Result.Failed() without aborting the batch.
	FailFast bool
}

// Resolver resolves parsed entries against a provider.Registry.
type Resolver struct {
	registry *provider.Registry
	opts     Options
}

// New builds a Resolver backed by registry.
func New(registry *provider.Registry, opts Options) *Resolver {
	return &Resolver{registry: registry, opts: opts}
}

// Resolve resolves every entry, in parallel bounded by opts.Concurrency.
// The returned Result's Entries slice matches entries' order regardless of
// completion order. When opts.FailFast is true, the first error also
// short-circuits remaining work and is returned as the second value;
// otherwise Resolve's error return is always nil and failures are found via
// Result.Failed()/HasErrors().
func (r *Resolver) Resolve(ctx context.Context, entries []parser.Entry) (*Result, error) {
	results := make([]ResolvedEntry, len(entries))

	concurrency := r.opts.Concurrency
	if concurrency <= 0 {
		concurrency = defaultConcurrency
	}

	if r.opts.FailFast {
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(concurrency)
		for i, e := range entries {
			i, e := i, e
			g.Go(func() error {
				re, err := r.resolveOne(gctx, e)
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
		i, e := i, e
		g.Go(func() error {
			re, _ := r.resolveOne(ctx, e)
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
func (r *Resolver) resolveOne(ctx context.Context, e parser.Entry) (ResolvedEntry, error) {
	if e.Ref == nil {
		return ResolvedEntry{Key: e.Key, Value: e.RawValue, Line: e.Line}, nil
	}

	p, ok := r.registry.Get(e.Ref.Tag)
	if !ok {
		return ResolvedEntry{Key: e.Key, Value: e.RawValue, Line: e.Line}, nil
	}

	resolveCtx := ctx
	if r.opts.PerRefTimeout > 0 {
		var cancel context.CancelFunc
		resolveCtx, cancel = context.WithTimeout(ctx, r.opts.PerRefTimeout)
		defer cancel()
	}

	secret, err := p.Resolve(resolveCtx, e.Ref.Path)
	if err != nil {
		return ResolvedEntry{Key: e.Key, Err: err, Line: e.Line}, err
	}
	return ResolvedEntry{Key: e.Key, Value: secret.Reveal(), Line: e.Line}, nil
}
