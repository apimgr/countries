// Package pidfile manages the server's own process ID file.
package pidfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Acquire rejects a live instance of this executable and atomically writes the current PID.
func Acquire(path string, mode os.FileMode) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create PID directory: %w", err)
	}
	if err := removeStale(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("PID file %s appeared during startup; another instance may be starting", path)
		}
		return fmt.Errorf("create PID file: %w", err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write PID file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close PID file: %w", err)
	}
	return nil
}

// Remove removes the process PID file, treating an absent file as already cleaned up.
func Remove(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove PID file: %w", err)
	}
	return nil
}

func removeStale(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if parseErr == nil && pid > 0 && processIsThisExecutable(pid) {
		return fmt.Errorf("already running (pid %d)", pid)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale PID file: %w", err)
	}
	return nil
}
