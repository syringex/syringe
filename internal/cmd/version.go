package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version, commit, and date are set at build time via -ldflags -X (see the
// Makefile for local builds, .github/workflows/release-inject.yml for
// tagged releases, and pr.yml/merge.yml for PR/merge builds). Left at
// their zero-value defaults, they identify an ad hoc local build.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the inject version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "inject %s (commit %s, built %s)\n", version, commit, date)
			return nil
		},
	}
}
