// Package providerdir manages the project-local ".syringe" directory that
// holds installed provider executables, e.g.
// .syringe/providers/aws-sm/inject-provider-aws-sm.
package providerdir

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const dirName = ".syringe"

// Dir returns the project-local provider directory path under base
// (typically the current working directory, where the .env file lives).
func Dir(base string) string {
	return filepath.Join(base, dirName)
}

// BinaryName is the executable name for a given provider tag.
func BinaryName(tag string) string {
	return "inject-provider-" + tag
}

// BinaryPath returns the path tag's executable should live at under dir (as
// returned by Dir).
func BinaryPath(dir, tag string) string {
	return filepath.Join(dir, "providers", tag, BinaryName(tag))
}

// Exists reports whether tag's provider binary is already installed under dir.
func Exists(dir, tag string) bool {
	info, err := os.Stat(BinaryPath(dir, tag))
	return err == nil && !info.IsDir()
}

// EnsureDir creates dir's providers subdirectory if missing. It errors if a
// non-directory already occupies dir itself.
func EnsureDir(dir string) error {
	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(filepath.Join(dir, "providers"), 0o755)
}

// EnsureProviderDir creates the directory tag's binary should live in.
func EnsureProviderDir(dir, tag string) error {
	return os.MkdirAll(filepath.Dir(BinaryPath(dir, tag)), 0o755)
}

// config is the on-disk shape of a provider's config.toml: an [env] table
// of ancillary SDK configuration (e.g. a region) — never credentials.
type config struct {
	Env map[string]string `toml:"env"`
}

// ConfigPath returns the path to tag's config manifest under dir.
func ConfigPath(dir, tag string) string {
	return filepath.Join(dir, "providers", tag, "config.toml")
}

// HasConfig reports whether tag already has a persisted config.toml.
func HasConfig(dir, tag string) bool {
	_, err := os.Stat(ConfigPath(dir, tag))
	return err == nil
}

// LoadEnv reads tag's persisted [env] table, if any. A missing config.toml
// is not an error — it just means the provider has no extra config.
func LoadEnv(dir, tag string) (map[string]string, error) {
	data, err := os.ReadFile(ConfigPath(dir, tag))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ConfigPath(dir, tag), err)
	}
	return cfg.Env, nil
}

// SaveEnv writes env as tag's [env] table, creating the provider's
// directory if needed.
func SaveEnv(dir, tag string, env map[string]string) error {
	if err := EnsureProviderDir(dir, tag); err != nil {
		return err
	}
	data, err := toml.Marshal(config{Env: env})
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(dir, tag), data, 0o600)
}
