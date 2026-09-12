package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newExportCmd(f *flags) *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "export",
		Short: `Print resolved KEY=VALUE pairs, e.g. for eval "$(inject export)"`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExport(cmd, f, format)
		},
	}
	cmd.Flags().StringVar(&format, "format", "shell", "output format: shell|dotenv|json")
	return cmd
}

func runExport(cmd *cobra.Command, f *flags, format string) error {
	result, err := resolveAll(cmd.Context(), f)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	switch format {
	case "dotenv":
		for _, e := range result.OK() {
			fmt.Fprintf(out, "%s=%s\n", e.Key, e.Value)
		}
	case "json":
		obj := make(map[string]string, len(result.OK()))
		for _, e := range result.OK() {
			obj[e.Key] = e.Value
		}
		if err := json.NewEncoder(out).Encode(obj); err != nil {
			return err
		}
	case "shell", "":
		for _, e := range result.OK() {
			fmt.Fprintf(out, "export %s=%s\n", e.Key, shellQuote(e.Value))
		}
	default:
		return fmt.Errorf("unknown --format %q (want shell, dotenv, or json)", format)
	}

	for _, e := range result.Failed() {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %s\n", e.Key, e.Err)
	}

	if result.HasErrors() {
		return withExitCode(1, fmt.Errorf("%d key(s) failed to resolve", len(result.Failed())))
	}
	return nil
}

// shellQuote POSIX-single-quotes s so it's safe inside eval, even if it
// contains spaces, double quotes, '$', backticks, or single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
