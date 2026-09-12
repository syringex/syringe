package execprovider_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/syringex/syringe/internal/execprovider"
	"github.com/syringex/syringe/pkg/provider"
)

var fakeBinaryPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "execprovider-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	fakeBinaryPath = filepath.Join(dir, "fakeprovider")
	build := exec.Command("go", "build", "-o", fakeBinaryPath, "./testdata/fakeprovider")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("building fakeprovider: " + err.Error())
	}

	os.Exit(m.Run())
}

func TestResolveSuccess(t *testing.T) {
	p := execprovider.New("fake", fakeBinaryPath, nil)
	secret, err := p.Resolve(context.Background(), "my/path")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got, want := secret.Reveal(), "resolved-my/path"; got != want {
		t.Errorf("Reveal() = %q, want %q", got, want)
	}
}

func TestResolveErrorKinds(t *testing.T) {
	cases := []struct {
		path     string
		wantKind error
	}{
		{"not-found", provider.ErrNotFound},
		{"access-denied", provider.ErrAccessDenied},
		{"transient", provider.ErrTransient},
		{"invalid-ref", provider.ErrInvalidRef},
	}
	p := execprovider.New("fake", fakeBinaryPath, nil)
	for _, c := range cases {
		_, err := p.Resolve(context.Background(), c.path)
		if err == nil {
			t.Errorf("path %q: expected error, got nil", c.path)
			continue
		}
		if !errors.Is(err, c.wantKind) {
			t.Errorf("path %q: error = %v, want kind %v", c.path, err, c.wantKind)
		}
	}
}

func TestResolveMalformedOutputFallsBackToTransient(t *testing.T) {
	p := execprovider.New("fake", fakeBinaryPath, nil)

	// "malformed" exits 0 but prints non-JSON: parsing the success envelope fails.
	if _, err := p.Resolve(context.Background(), "malformed"); err == nil {
		t.Error("expected error for non-JSON success output, got nil")
	} else if !errors.Is(err, provider.ErrTransient) {
		t.Errorf("error = %v, want ErrTransient", err)
	}

	// "malformed-error" exits non-zero with non-JSON stdout: falls back to
	// a transient error built from stderr/exit status rather than crashing.
	if _, err := p.Resolve(context.Background(), "malformed-error"); err == nil {
		t.Error("expected error for non-JSON failure output, got nil")
	} else if !errors.Is(err, provider.ErrTransient) {
		t.Errorf("error = %v, want ErrTransient", err)
	}
}

func TestValidateSuccess(t *testing.T) {
	p := execprovider.New("fake", fakeBinaryPath, nil)
	if err := p.Validate(context.Background(), "my/path"); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestResolveContextTimeoutKillsSubprocess(t *testing.T) {
	p := execprovider.New("fake", fakeBinaryPath, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Resolve(ctx, "slow")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error when context times out before the subprocess finishes")
	}
	if elapsed > time.Second {
		t.Errorf("Resolve took %v, expected it to return promptly after the context timeout", elapsed)
	}
}

func TestInitSkipsPromptWhenAlreadySatisfied(t *testing.T) {
	t.Setenv("FAKE_INIT_MODE", "") // default branch in fakeprovider: no prompt at all

	p := execprovider.New("fake", fakeBinaryPath, nil)
	env, err := p.Init(context.Background())
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("env = %v, want empty (nothing to persist when already satisfied)", env)
	}
}

func TestInitPromptsAndReturnsEnv(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	go func() {
		defer w.Close()
		fmt.Fprintln(w, "typed-value")
	}()

	t.Setenv("FAKE_INIT_MODE", "prompt")
	p := execprovider.New("fake", fakeBinaryPath, nil)
	env, err := p.Init(context.Background())
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if got, want := env["FAKE_VALUE"], "typed-value"; got != want {
		t.Errorf("env[FAKE_VALUE] = %q, want %q", got, want)
	}
}

func TestInitFailure(t *testing.T) {
	t.Setenv("FAKE_INIT_MODE", "fail")

	p := execprovider.New("fake", fakeBinaryPath, nil)
	if _, err := p.Init(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}
