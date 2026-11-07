//go:build !windows

package updater

import (
	"fmt"
	"os"
	"syscall"
)

func replaceBinary(currentPath, newBinaryPath string) error {
	info, err := os.Stat(currentPath)
	if err != nil {
		return fmt.Errorf("inspect current binary: %w", err)
	}
	if err := os.Rename(newBinaryPath, currentPath); err != nil {
		return fmt.Errorf("replace current binary: %w", err)
	}
	if err := os.Chmod(currentPath, info.Mode()); err != nil {
		return fmt.Errorf("restore binary permissions: %w", err)
	}
	return nil
}

// RestartSelf replaces the running process with the updated executable.
func RestartSelf() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	return syscall.Exec(executable, os.Args, os.Environ())
}
