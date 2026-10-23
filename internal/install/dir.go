// Package install puts provider executables where inject can run them: the
// project-local .syringe directory (binaries plus each provider's persisted
// config), and verified downloads of providers' released binaries into it.
package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const dirName = ".syringe"

// Dir is a project-local provider directory, holding installed provider
// executables, e.g. .syringe/providers/aws/inject-provider-aws.
type Dir string

// DirIn returns the provider directory under base (typically the current
// working directory, where the .env file lives).
func DirIn(base string) Dir {
	return Dir(filepath.Join(base, dirName))
}

// BinaryPath returns the path name's executable lives at under d.
func (d Dir) BinaryPath(name string) string {
	return filepath.Join(d.providerDir(name), "inject-provider-"+name)
}

// IsInstalled reports whether name's provider binary is already installed
// under d.
func (d Dir) IsInstalled(name string) bool {
	info, err := os.Stat(d.BinaryPath(name))
	return err == nil && !info.IsDir()
}

// Prepare creates the directory name's binary should live in. It errors if a
// non-directory already occupies d itself.
func (d Dir) Prepare(name string) error {
	if info, err := os.Stat(string(d)); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", d)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(string(d), "providers"), 0o755); err != nil {
		return err
	}
	return os.MkdirAll(d.providerDir(name), 0o755)
}

func (d Dir) providerDir(name string) string {
	return filepath.Join(string(d), "providers", name)
}

// config is the on-disk shape of a provider's config.toml: an [env] table
// of ancillary SDK configuration (e.g. a region) — never credentials.
type config struct {
	Env map[string]string `toml:"env"`
}

// ConfigPath returns the path to name's config manifest under d.
func (d Dir) ConfigPath(name string) string {
	return filepath.Join(d.providerDir(name), "config.toml")
}

// HasConfig reports whether name already has a persisted config.toml.
func (d Dir) HasConfig(name string) bool {
	_, err := os.Stat(d.ConfigPath(name))
	return err == nil
}

// LoadEnv reads name's persisted [env] table, if any. A missing config.toml
// is not an error — it just means the provider has no extra config.
func (d Dir) LoadEnv(name string) (map[string]string, error) {
	data, err := os.ReadFile(d.ConfigPath(name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", d.ConfigPath(name), err)
	}
	return cfg.Env, nil
}

// SaveEnv writes env as name's [env] table, creating the provider's
// directory if needed.
func (d Dir) SaveEnv(name string, env map[string]string) error {
	if err := os.MkdirAll(d.providerDir(name), 0o755); err != nil {
		return err
	}
	data, err := toml.Marshal(config{Env: env})
	if err != nil {
		return err
	}
	return os.WriteFile(d.ConfigPath(name), data, 0o600)
}
