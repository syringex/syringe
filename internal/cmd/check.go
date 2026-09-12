package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCheckCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check that every reference in the file resolves, without printing values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd, f)
		},
	}
}

// runCheck reports per-key pass/fail and never prints a resolved value —
// only "ok" or the (value-free) error message.
//
// TODO(phase 3+): once providers implement Validate, route through it
// instead of Resolve so a secret's value is never even held in memory here.
func runCheck(cmd *cobra.Command, f *flags) error {
	result, err := resolveAll(cmd.Context(), f)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	for _, e := range result.Entries {
		status := "ok"
		if e.Err != nil {
			status = e.Err.Error()
		}
		fmt.Fprintf(out, "%s: %s\n", e.Key, status)
	}

	if result.HasErrors() {
		return withExitCode(1, fmt.Errorf("%d key(s) failed to resolve", len(result.Failed())))
	}
	return nil
}
