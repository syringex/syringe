//go:build !windows

package cmd

import "syscall"

// execReplace replaces the current process image with binPath, argv, envv
// (true exec — on success this never returns).
func execReplace(binPath string, argv, envv []string) error {
	return syscall.Exec(binPath, argv, envv)
}
