// Command inject resolves secret references in an env file and injects
// them into a command's environment, or exposes them via get/check/export.
package main

import (
	"os"

	"github.com/syringex/syringe/internal/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}
