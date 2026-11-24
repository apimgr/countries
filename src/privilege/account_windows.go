//go:build windows

package privilege

import "golang.org/x/sys/windows"

// IsElevated reports whether the current Windows token is elevated by UAC.
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// EnsureSystemAccount is unsupported because Windows manages service accounts.
func EnsureSystemAccount(name, homeDir string) (Identity, error) {
	return Identity{}, ErrUnsupported
}

// RemoveSystemAccount is unsupported because Windows manages service accounts.
func RemoveSystemAccount(identity Identity) error { return ErrUnsupported }

// DropPrivileges is unsupported because Windows services run under a VSA.
func DropPrivileges(identity Identity) error {
	return ErrUnsupported
}
