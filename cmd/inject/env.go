package main

import (
	"os"

	"github.com/syringex/syringe/internal/resolve"
)

// envFromResult merges result's successfully resolved entries over the
// current process's environment, resolved keys winning on collision.
func envFromResult(result *resolve.Result) []string {
	env := os.Environ()
	for _, e := range result.OK() {
		env = append(env, e.Key+"="+e.Value)
	}
	return env
}
