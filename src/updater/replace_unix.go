//go:build !windows

package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func CanApplyStaged() bool {
	return os.Geteuid() == 0
}

func replaceBinary(currentPath, newBinaryPath string) error {
	info, err := os.Stat(currentPath)
	if err != nil {
		return fmt.Errorf("inspect current binary: %w", err)
	}
	staged, err := os.Open(newBinaryPath)
	if err != nil {
		return fmt.Errorf("open replacement binary: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(currentPath), ".countries-update-*")
	if err != nil {
		_ = staged.Close()
		return fmt.Errorf("create replacement binary: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = staged.Close()
		_ = temporary.Close()
		return fmt.Errorf("set replacement permissions: %w", err)
	}
	if _, err := io.Copy(temporary, staged); err != nil {
		_ = staged.Close()
		_ = temporary.Close()
		return fmt.Errorf("copy replacement binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("close replacement source: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync replacement binary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close replacement binary: %w", err)
	}
	if err := os.Rename(temporaryPath, currentPath); err != nil {
		return fmt.Errorf("replace current binary: %w", err)
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
