// Package providertest offers test doubles for provider.Provider, exported
// so both syringe's own tests and out-of-tree provider authors can reuse
// them without hitting real cloud APIs.
package providertest

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/syringex/syringe/pkg/provider"
)

// FakeResponse is the canned result FakeProvider returns for a given path.
type FakeResponse struct {
	Value string
	Err   error
	// Delay, if set, overrides FakeProvider.Delay for this specific path —
	// useful when a test needs two paths to resolve at deterministically
	// different speeds (e.g. one fails fast while another is still
	// in-flight) rather than racing on one shared delay.
	Delay time.Duration
}

// FakeProvider is a configurable provider.Provider stub for unit tests.
type FakeProvider struct {
	TagName   string
	Responses map[string]FakeResponse
	// Delay, if set, is slept (or until ctx is done, whichever comes first)
	// before returning, to simulate network latency in concurrency tests.
	Delay time.Duration

	CallCount atomic.Int32
}

func (f *FakeProvider) Tag() string { return f.TagName }

func (f *FakeProvider) Resolve(ctx context.Context, path string) (provider.Secret, error) {
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
			return provider.Secret{}, provider.NewError(provider.ErrTransient, f.TagName, path, ctx.Err())
		}
	}

	if !ok {
		return provider.Secret{}, provider.NewError(provider.ErrNotFound, f.TagName, path, nil)
	}
	if resp.Err != nil {
		return provider.Secret{}, resp.Err
	}
	return provider.Secret{Value: resp.Value}, nil
}

func (f *FakeProvider) Validate(ctx context.Context, path string) error {
	_, err := f.Resolve(ctx, path)
	return err
}
