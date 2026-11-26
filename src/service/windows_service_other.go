//go:build !windows

package service

import "context"

// IsWindowsService reports false on platforms without Windows services.
func IsWindowsService() bool { return false }

// RunWindowsService invokes the application callback on non-Windows systems.
func RunWindowsService(run func(context.Context) error) error {
	if run == nil {
		return nil
	}
	return run(context.Background())
}
