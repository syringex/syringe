package resolver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syringex/syringe/internal/parser"
	"github.com/syringex/syringe/internal/resolver"
	"github.com/syringex/syringe/pkg/provider"
	"github.com/syringex/syringe/pkg/provider/providertest"
)

func TestResolveOrderAndLiterals(t *testing.T) {
	fake := &providertest.FakeProvider{
		TagName: "aws-sm",
		Responses: map[string]providertest.FakeResponse{
			"prod/db/password": {Value: "hunter2"},
		},
	}
	registry := provider.NewRegistry()
	if err := registry.Register(fake); err != nil {
		t.Fatal(err)
	}

	entries := []parser.Entry{
		{Key: "DB_PASSWORD", RawValue: "aws-sm:prod/db/password", Ref: &provider.Ref{Tag: "aws-sm", Path: "prod/db/password"}, Line: 1},
		{Key: "PLAIN", RawValue: "just-a-value", Line: 2},
		{Key: "TYPO_TAG", RawValue: "aws-ssm:not/registered", Ref: &provider.Ref{Tag: "aws-ssm", Path: "not/registered"}, Line: 3},
	}

	r := resolver.New(registry, resolver.Options{})
	result, err := r.Resolve(context.Background(), entries)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(result.Entries))
	}

	// Order must match input order regardless of concurrency.
	if got, want := result.Entries[0].Key, "DB_PASSWORD"; got != want {
		t.Errorf("Entries[0].Key = %q, want %q", got, want)
	}
	if got, want := result.Entries[0].Value, "hunter2"; got != want {
		t.Errorf("resolved value = %q, want %q", got, want)
	}
	if got, want := result.Entries[1].Value, "just-a-value"; got != want {
		t.Errorf("literal value = %q, want %q", got, want)
	}
	// An unregistered tag falls back to the raw literal value.
	if got, want := result.Entries[2].Value, "aws-ssm:not/registered"; got != want {
		t.Errorf("unregistered-tag fallback = %q, want %q", got, want)
	}
	if result.HasErrors() {
		t.Errorf("unexpected errors: %+v", result.Failed())
	}
}

func TestResolvePartialFailureNonFailFast(t *testing.T) {
	fake := &providertest.FakeProvider{
		TagName: "aws-sm",
		Responses: map[string]providertest.FakeResponse{
			"good":  {Value: "ok"},
			"bad":   {Err: provider.NewError(provider.ErrAccessDenied, "aws-sm", "bad", nil)},
			"good2": {Value: "ok2"},
		},
	}
	registry := provider.NewRegistry()
	_ = registry.Register(fake)

	entries := []parser.Entry{
		{Key: "A", RawValue: "aws-sm:good", Ref: &provider.Ref{Tag: "aws-sm", Path: "good"}, Line: 1},
		{Key: "B", RawValue: "aws-sm:bad", Ref: &provider.Ref{Tag: "aws-sm", Path: "bad"}, Line: 2},
		{Key: "C", RawValue: "aws-sm:good2", Ref: &provider.Ref{Tag: "aws-sm", Path: "good2"}, Line: 3},
	}

	r := resolver.New(registry, resolver.Options{FailFast: false})
	result, err := r.Resolve(context.Background(), entries)
	if err != nil {
		t.Fatalf("non-fail-fast Resolve should never return an error, got: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("expected HasErrors to be true")
	}
	if len(result.OK()) != 2 {
		t.Errorf("got %d ok entries, want 2", len(result.OK()))
	}
	failed := result.Failed()
	if len(failed) != 1 || failed[0].Key != "B" {
		t.Errorf("Failed() = %+v, want just key B", failed)
	}
	if !errors.Is(failed[0].Err, provider.ErrAccessDenied) {
		t.Errorf("failed entry error = %v, want ErrAccessDenied", failed[0].Err)
	}
	// Siblings of the failed entry must still have resolved.
	if got := result.Entries[2].Value; got != "ok2" {
		t.Errorf("entry after failure = %q, want %q (sibling should not be cancelled)", got, "ok2")
	}
}

func TestResolveFailFastCancelsRemaining(t *testing.T) {
	fake := &providertest.FakeProvider{
		TagName: "aws-sm",
		Responses: map[string]providertest.FakeResponse{
			// "bad" fails immediately; "slow1" would take 2s if allowed to
			// run to completion. Distinct per-path delays (rather than one
			// shared FakeProvider.Delay) make which one fails first
			// deterministic instead of a race between two equally-delayed
			// goroutines.
			"slow1": {Value: "should-not-finish", Delay: 2 * time.Second},
			"bad":   {Err: provider.NewError(provider.ErrAccessDenied, "aws-sm", "bad", nil)},
		},
	}
	registry := provider.NewRegistry()
	_ = registry.Register(fake)

	entries := []parser.Entry{
		{Key: "SLOW1", RawValue: "aws-sm:slow1", Ref: &provider.Ref{Tag: "aws-sm", Path: "slow1"}, Line: 1},
		{Key: "BAD", RawValue: "aws-sm:bad", Ref: &provider.Ref{Tag: "aws-sm", Path: "bad"}, Line: 2},
	}

	r := resolver.New(registry, resolver.Options{FailFast: true, Concurrency: 2})
	start := time.Now()
	_, err := r.Resolve(context.Background(), entries)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected fail-fast Resolve to return the first error")
	}
	if !errors.Is(err, provider.ErrAccessDenied) {
		t.Errorf("error = %v, want ErrAccessDenied", err)
	}
	if elapsed > time.Second {
		t.Errorf("Resolve took %v, expected fail-fast to cancel \"slow1\" well before its 2s delay", elapsed)
	}
}

func TestResolvePerRefTimeout(t *testing.T) {
	fake := &providertest.FakeProvider{
		TagName: "aws-sm",
		Responses: map[string]providertest.FakeResponse{
			"slow": {Value: "too-late"},
		},
		Delay: 200 * time.Millisecond,
	}
	registry := provider.NewRegistry()
	_ = registry.Register(fake)

	entries := []parser.Entry{
		{Key: "SLOW", RawValue: "aws-sm:slow", Ref: &provider.Ref{Tag: "aws-sm", Path: "slow"}, Line: 1},
	}

	r := resolver.New(registry, resolver.Options{PerRefTimeout: 20 * time.Millisecond})
	result, err := r.Resolve(context.Background(), entries)
	if err != nil {
		t.Fatalf("non-fail-fast Resolve should never return an error, got: %v", err)
	}
	if !result.HasErrors() {
		t.Fatal("expected the slow entry to time out and be recorded as a failure")
	}
}
