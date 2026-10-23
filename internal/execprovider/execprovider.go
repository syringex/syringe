// Package execprovider implements provider.Provider by shelling out to a
// separately installed provider executable, per the wire protocol defined
// in pkg/providerproto. It is the only provider.Provider implementation
// the core inject binary needs — every cloud-specific SDK call lives in a
// standalone provider binary instead.
package execprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/syringex/syringe/internal/provider"
	"github.com/syringex/syringe/pkg/providerproto"
)

// Provider runs Binary as a child process to resolve/validate secrets
// tagged TagName.
type Provider struct {
	// TagName is the reference tag this Provider serves, passed to Binary on
	// every resolve/validate call. Init only uses it to label its errors.
	TagName string
	// Binary is the path to the provider executable.
	Binary string
	// Env holds ancillary SDK configuration (e.g. a region) gathered by a
	// prior Init call and persisted by the caller — never credentials, which
	// stay on the provider's own default credential chain. It is merged into
	// every subprocess call's environment, and may be nil.
	Env map[string]string
}

func (p *Provider) Tag() string { return p.TagName }

func (p *Provider) Resolve(ctx context.Context, path string) (provider.Secret, error) {
	stdout, err := p.run(ctx, "resolve", path)
	if err != nil {
		return provider.Secret{}, err
	}
	var res providerproto.ResolveResult
	if jsonErr := json.Unmarshal(stdout, &res); jsonErr != nil {
		return provider.Secret{}, &provider.Error{
			Kind: provider.ErrTransient,
			Tag:  p.TagName,
			Path: path,
			Err:  errors.New("malformed response from " + p.Binary + ": " + jsonErr.Error()),
		}
	}
	return provider.Secret{Value: res.Value}, nil
}

func (p *Provider) Validate(ctx context.Context, path string) error {
	_, err := p.run(ctx, "validate", path)
	return err
}

// Init runs the provider's own interactive setup step. Unlike
// Resolve/Validate, stdin and stderr are connected directly to the real
// terminal so the provider can prompt the user itself; only stdout is
// captured, for the final InitResult/ErrorEnvelope line. Init should be
// called with a context that has no short deadline — it may be waiting on
// a human.
func (p *Provider) Init(ctx context.Context) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, p.Binary, "init")
	cmd.Env = p.env()
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if runErr := cmd.Run(); runErr != nil {
		var envelope providerproto.ErrorEnvelope
		if jsonErr := json.Unmarshal(stdout.Bytes(), &envelope); jsonErr == nil && envelope.Error.Message != "" {
			return nil, fmt.Errorf("%s init: %s", p.TagName, envelope.Error.Message)
		}
		return nil, fmt.Errorf("%s init: %w", p.TagName, runErr)
	}

	var res providerproto.InitResult
	if jsonErr := json.Unmarshal(stdout.Bytes(), &res); jsonErr != nil {
		return nil, fmt.Errorf("%s init: malformed response: %w", p.TagName, jsonErr)
	}
	return res.Env, nil
}

// env returns the environment for a subprocess call: nil (inherit
// os.Environ() as-is) when there's no extra config, or os.Environ() with
// p.Env merged over it otherwise.
func (p *Provider) env() []string {
	if len(p.Env) == 0 {
		return nil
	}
	env := os.Environ()
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	return env
}

// run executes the provider binary with subcommand, this Provider's tag,
// and path, returning raw stdout on success or a classified *provider.Error
// on failure. The tag is always passed so one binary can serve more than
// one tag (see pkg/providerproto's package doc); a provider that only ever
// serves one tag is free to ignore it.
func (p *Provider) run(ctx context.Context, subcommand, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.Binary, subcommand, p.TagName, path)
	cmd.Env = p.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		var envelope providerproto.ErrorEnvelope
		if jsonErr := json.Unmarshal(stdout.Bytes(), &envelope); jsonErr == nil && envelope.Error.Kind != "" {
			return nil, &provider.Error{
				Kind: kindToSentinel(envelope.Error.Kind),
				Tag:  p.TagName,
				Path: path,
				Err:  errors.New(envelope.Error.Message),
			}
		}
		msg := stderr.String()
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, &provider.Error{Kind: provider.ErrTransient, Tag: p.TagName, Path: path, Err: errors.New(msg)}
	}

	return stdout.Bytes(), nil
}

func kindToSentinel(k providerproto.Kind) error {
	switch k {
	case providerproto.KindNotFound:
		return provider.ErrNotFound
	case providerproto.KindAccessDenied:
		return provider.ErrAccessDenied
	case providerproto.KindInvalidRef:
		return provider.ErrInvalidRef
	default:
		return provider.ErrTransient
	}
}
