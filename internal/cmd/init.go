package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/syringex/syringe/internal/execprovider"
	"github.com/syringex/syringe/internal/lockfile"
	"github.com/syringex/syringe/internal/parser"
	"github.com/syringex/syringe/internal/providerdir"
	"github.com/syringex/syringe/internal/providerdownload"
)

// moduleDir is the absolute path to this monorepo's source, baked into the
// inject binary at build time via `-ldflags -X ...moduleDir=$(pwd)` (see
// the Makefile). Its presence is what distinguishes a contributor's local
// dev build from a real, distributed inject binary (built by
// release-inject.yml, with no such path baked in):
//   - moduleDir != "": build providers from this monorepo's source, always
//     at the manifest's current version, ignoring any syringe.lock pin — a
//     contributor iterating on provider code should never be silently
//     served an old downloaded release.
//   - moduleDir == "": download+verify a provider's released binary
//     instead, respecting an existing syringe.lock pin for real
//     reproducibility (see runInit).
var moduleDir string

func newInitCmd(f *flags) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Install the provider binaries required by the env file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd, f, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rebuild providers even if already installed")
	return cmd
}

// runInit inspects f.file for the tags it references, maps them to the
// distinct provider identities that serve them (e.g. aws-sm and aws-ssm
// both collapse to "aws" — see providerNames), and for each identity:
// installs it (building from source in a local dev build, or downloading
// and verifying its released binary otherwise — see moduleDir), records
// its version and binary digest in syringe.lock, then runs its own `init`
// step if it has no persisted config yet (or --force). A tag-shaped value
// with no known provider identity is left alone entirely — it's presumed a
// literal, not a typo.
func runInit(cmd *cobra.Command, f *flags, force bool) error {
	entries, err := parser.ParseFile(f.file)
	if err != nil {
		return err
	}

	// Resolved to an absolute path up front: the build below runs with its
	// working directory set to moduleDir, so a relative -o path here would
	// otherwise land inside the syringe repo instead of the caller's project.
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := providerdir.Dir(cwd)
	out := cmd.OutOrStdout()

	lf, err := lockfile.Load(lockfile.FileName)
	if err != nil {
		return err
	}
	platform := runtime.GOOS + "_" + runtime.GOARCH

	var unbuildable []string
	var configFailures []string
	installedAny := false
	lockUpdated := false

	for _, name := range requiredProviderNames(entries) {
		binaryInstalled := providerdir.Exists(dir, name)

		// Respect an existing lock pin by default — the whole point of the
		// lock file is that a checked-in syringe.lock keeps installing the
		// same version even after providers/manifest.json moves on.
		// --force re-resolves against the manifest's current version
		// instead (the "upgrade" path).
		version := providerVersions[name]
		if existing, ok := lf.Providers[name]; ok && existing.Version != "" && !force {
			version = existing.Version
		}

		var digest string
		switch {
		case binaryInstalled && !force:
			fmt.Fprintf(out, "%s: already installed\n", name)

		case moduleDir != "":
			// Local dev build: always build from source at the manifest's
			// current version, ignoring any lock pin — see moduleDir's doc.
			version = providerVersions[name]
			pkg, ok := providerPackages[name]
			if !ok {
				unbuildable = append(unbuildable, name)
				continue
			}
			if err := providerdir.EnsureDir(dir); err != nil {
				return err
			}
			if err := providerdir.EnsureProviderDir(dir, name); err != nil {
				return err
			}

			binPath := providerdir.BinaryPath(dir, name)
			fmt.Fprintf(out, "%s: building from %s (version %s)\n", name, pkg, version)
			build := exec.Command("go", "build", "-ldflags", "-X "+pkg+".version="+version, "-o", binPath, pkg)
			build.Dir = moduleDir
			build.Stdout = out
			build.Stderr = cmd.ErrOrStderr()
			if err := build.Run(); err != nil {
				return fmt.Errorf("building provider %q: %w", name, err)
			}
			installedAny = true

		default:
			// A real, distributed inject binary: download and verify the
			// provider's released binary against its published checksums.
			if err := providerdir.EnsureDir(dir); err != nil {
				return err
			}
			if err := providerdir.EnsureProviderDir(dir, name); err != nil {
				return err
			}

			binPath := providerdir.BinaryPath(dir, name)
			fmt.Fprintf(out, "%s: downloading version %s (%s)...\n", name, version, providerdownload.ReleaseTag(name, version))
			d, err := providerdownload.Install(cmd.Context(), name, version, runtime.GOOS, runtime.GOARCH, binPath)
			if err != nil {
				return fmt.Errorf("downloading provider %q: %w", name, err)
			}
			digest = d
			installedAny = true
		}

		if digest == "" {
			// Recorded for every required provider with a binary present
			// (freshly built, already installed, or downloaded above), so
			// syringe.lock stays an accurate reflection of what's actually
			// on disk even if it was missing, stale, or hand-edited. The
			// download path above already has its digest from Install.
			d, err := lockfile.DigestFile(providerdir.BinaryPath(dir, name))
			if err != nil {
				return fmt.Errorf("digesting provider %q: %w", name, err)
			}
			digest = d
		}

		// A real, distributed binary is meant to reproduce exactly what
		// syringe.lock recorded for this version+platform before — that's
		// the actual guarantee a lock file provides (same as
		// .terraform.lock.hcl / package-lock.json). A dev build
		// (moduleDir != "") is exempt: it always tracks local source at the
		// manifest's current version regardless of any lock pin (see
		// moduleDir's doc), so comparing it against an old lock digest would
		// just make ordinary local iteration fail every time source changes
		// without a version bump.
		if moduleDir == "" && !force {
			if existing, ok := lf.Providers[name]; ok && existing.Version == version {
				if p, ok := existing.Platforms[platform]; ok && p.Digest != "" && p.Digest != digest {
					return fmt.Errorf("provider %q version %s: installed binary digest %s does not match %s's recorded digest %s for %s — refusing to continue (this could mean the release was replaced/tampered with after %s was committed, or the local binary was modified out of band); re-run with --force if this is expected", name, version, digest, lockfile.FileName, p.Digest, platform, lockfile.FileName)
				}
			}
		}

		lf.SetPlatform(name, version, platform, digest)
		lockUpdated = true

		if force || !providerdir.HasConfig(dir, name) {
			fmt.Fprintf(out, "%s: configuring...\n", name)
			// The tag passed to New() here is never used: this call is
			// only ever "init", which configures the shared provider
			// identity, not a specific tag.
			p := execprovider.New(name, providerdir.BinaryPath(dir, name), nil)
			env, err := p.Init(cmd.Context())
			if err != nil {
				// A configure failure for one provider (e.g. non-interactive
				// stdin) shouldn't stop other providers from being built/
				// configured, or skip .gitignore/lock housekeeping below for
				// what did succeed.
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: configuring failed: %v\n", name, err)
				configFailures = append(configFailures, name)
				continue
			}
			if err := providerdir.SaveEnv(dir, name, env); err != nil {
				return err
			}
		}
	}

	if lockUpdated {
		if err := lf.Save(lockfile.FileName); err != nil {
			return fmt.Errorf("writing %s: %w", lockfile.FileName, err)
		}
	}

	if installedAny {
		if err := ensureGitignored(providerdir.Dir(".")); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not update .gitignore: %v\n", err)
		}
	}

	if len(unbuildable) > 0 {
		return fmt.Errorf("no provider implementation available yet for %v", unbuildable)
	}
	if len(configFailures) > 0 {
		return fmt.Errorf("configuring provider(s) failed for %v", configFailures)
	}
	return nil
}

// ensureGitignored appends dir's base name (".syringe/") to ./.gitignore,
// creating the file if needed, unless an entry for it is already present.
func ensureGitignored(dir string) error {
	entry := filepath.Base(dir)
	const gitignorePath = ".gitignore"

	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		trimmed := strings.TrimSuffix(strings.TrimSpace(line), "/")
		if trimmed == entry {
			return nil
		}
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(entry + "/\n")
	return err
}
