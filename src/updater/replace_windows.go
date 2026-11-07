//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
)

func replaceBinary(currentPath, newBinaryPath string) error {
	backupPath := currentPath + ".old"
	if _, err := os.Stat(backupPath); err == nil {
		return fmt.Errorf("previous update backup already exists: %s", backupPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect previous update backup: %w", err)
	}
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("move running binary aside: %w", err)
	}
	if err := os.Rename(newBinaryPath, currentPath); err != nil {
		_ = os.Rename(backupPath, currentPath)
		return fmt.Errorf("install updated binary: %w", err)
	}
	oldPath, err := windows.UTF16PtrFromString(backupPath)
	if err != nil {
		return fmt.Errorf("encode previous binary path: %w", err)
	}
	if err := windows.MoveFileEx(oldPath, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT); err != nil {
		return fmt.Errorf("schedule previous binary cleanup: %w", err)
	}
	return nil
}

// RestartSelf starts the new executable and exits this process on Windows.
func RestartSelf() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	command := exec.Command(executable, os.Args[1:]...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Start(); err != nil {
		return fmt.Errorf("start updated executable: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	os.Exit(0)
	return nil
}
