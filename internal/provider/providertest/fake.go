// Package providertest offers test doubles for provider.Provider, so tests
// across inject's packages can exercise resolution without hitting real
// cloud APIs.
package providertest

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/syringex/syringe/internal/provider"
)

// Response is the canned result Fake returns for a given path.
type Response struct {
	Value string
	Err   error
	// Delay, if set, overrides Fake.Delay for this specific path —
	// useful when a test needs two paths to resolve at deterministically
	// different speeds (e.g. one fails fast while another is still
	// in-flight) rather than racing on one shared delay.
	Delay time.Duration
}

// Fake is a configurable provider.Provider stub for unit tests.
type Fake struct {
	TagName   string
	Responses map[string]Response
	// Delay, if set, is slept (or until ctx is done, whichever comes first)
	// before returning, to simulate network latency in concurrency tests.
	Delay time.Duration

	CallCount atomic.Int32
}

func (f *Fake) Tag() string { return f.TagName }

func (f *Fake) Resolve(ctx context.Context, path string) (provider.Secret, error) {
	f.CallCount.Add(1)
	resp, ok := f.Responses[path]

	delay := f.Delay
	if ok && resp.Delay > 0 {
		delay = resp.Delay
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return provider.Secret{}, &provider.Error{Kind: provider.ErrTransient, Tag: f.TagName, Path: path, Err: ctx.Err()}
		}
	}

	if !ok {
		return provider.Secret{}, &provider.Error{Kind: provider.ErrNotFound, Tag: f.TagName, Path: path}
	}
	if resp.Err != nil {
		return provider.Secret{}, resp.Err
	}
	return provider.Secret{Value: resp.Value}, nil
}

func (f *Fake) Validate(ctx context.Context, path string) error {
	_, err := f.Resolve(ctx, path)
	return err
}
