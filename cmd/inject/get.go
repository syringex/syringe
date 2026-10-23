package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/syringex/syringe/internal/envfile"
)

func newGetCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "get <KEY>",
		Short: "Resolve and print a single key's value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGet(cmd, f, args[0])
		},
	}
}

func runGet(cmd *cobra.Command, f *flags, key string) error {
	entries, err := envfile.ParseFile(f.file)
	if err != nil {
		return err
	}

	var match *envfile.Entry
	for i := range entries {
		if entries[i].Key == key {
			match = &entries[i] // last occurrence wins, matching envfile.Parse's documented semantics
		}
	}
	if match == nil {
		return fmt.Errorf("key %q not found in %s", key, f.file)
	}

	result, err := resolveEntries(cmd.Context(), f, []envfile.Entry{*match})
	if err != nil {
		return err
	}
	if result.HasErrors() {
		return formatResolveErrors(result)
	}

	fmt.Fprintln(cmd.OutOrStdout(), result.Entries[0].Value)
	return nil
}
