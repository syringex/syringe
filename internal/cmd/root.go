// Package cmd wires up inject's cobra command tree.
package cmd

import (
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

// flags holds settings shared across subcommands, populated by root's
// persistent flags. Threaded explicitly (not a package-level global) so
// each newRootCmd() call, including in tests, gets an independent set.
type flags struct {
	file        string
	concurrency int
	timeout     time.Duration
	verbose     bool
	jsonOutput  bool
	noColor     bool
}

// exitCodeErr lets a subcommand request a specific process exit code
// without cobra's default error handling getting in the way of the
// message it prints.
type exitCodeErr struct {
	code int
	err  error
}

func (e *exitCodeErr) Error() string { return e.err.Error() }
func (e *exitCodeErr) Unwrap() error { return e.err }

// withExitCode wraps err so Execute reports it via os.Exit(code). A nil err
// stays nil.
func withExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &exitCodeErr{code: code, err: err}
}

// Execute runs the inject CLI and returns the process exit code.
func Execute() int {
	root := newRootCmd()
	err := root.Execute()
	if err == nil {
		return 0
	}
	if ec, ok := errors.AsType[*exitCodeErr](err); ok {
		return ec.code
	}
	return 1
}

func newRootCmd() *cobra.Command {
	f := &flags{}

	root := &cobra.Command{
		Use:   "inject [-- <command> [args...]]",
		Short: "Resolve secret references in an env file and inject them into a command's environment",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRoot(cmd, args, f)
		},
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(&f.file, "file", "f", ".env", "path to the env-with-refs file")
	root.PersistentFlags().IntVar(&f.concurrency, "concurrency", 8, "maximum concurrent secret resolutions")
	root.PersistentFlags().DurationVar(&f.timeout, "timeout", 10*time.Second, "per-secret resolve timeout")
	root.PersistentFlags().BoolVarP(&f.verbose, "verbose", "v", false, "enable verbose diagnostic output")
	root.PersistentFlags().BoolVar(&f.jsonOutput, "json", false, "machine-readable output where supported")
	root.PersistentFlags().BoolVar(&f.noColor, "no-color", false, "disable colored output")

	root.AddCommand(newGetCmd(f))
	root.AddCommand(newCheckCmd(f))
	root.AddCommand(newExportCmd(f))
	root.AddCommand(newInitCmd(f))
	root.AddCommand(newVersionCmd())

	return root
}

// runRoot implements the default action: `inject -- <command> [args...]`
// resolves every entry and execs command with the resolved values merged
// into its environment. Any resolution failure aborts before exec — an
// external process should never see a partially-populated environment.
func runRoot(cmd *cobra.Command, args []string, f *flags) error {
	dashAt := cmd.ArgsLenAtDash()
	if dashAt < 0 {
		return cmd.Help()
	}
	command := args[dashAt:]
	if len(command) == 0 {
		return fmt.Errorf("no command given after `--`")
	}

	result, err := resolveAll(cmd.Context(), f)
	if err != nil {
		return err
	}
	if result.HasErrors() {
		return formatResolveErrors(result)
	}

	binPath, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("command not found: %s", command[0])
	}

	env := envFromResult(result)
	return execReplace(binPath, command, env)
}
