//go:build windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
)

// execReplace runs binPath as a child process and exits with its exit
// code — Windows has no true exec() process-replacement primitive.
func execReplace(binPath string, argv, envv []string) error {
	c := exec.Command(binPath, argv[1:]...)
	c.Env = envv
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr

	runErr := c.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		os.Exit(exitErr.ExitCode())
	}
	if runErr != nil {
		return runErr
	}
	os.Exit(0)
	return nil
}
