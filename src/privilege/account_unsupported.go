//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !windows

package privilege

// EnsureSystemAccount reports unsupported account-management platforms.
func EnsureSystemAccount(name, homeDir string) (Identity, error) {
	return Identity{}, ErrUnsupported
}

// RemoveSystemAccount is unsupported on this platform.
func RemoveSystemAccount(identity Identity) error { return ErrUnsupported }

// DropPrivileges reports unsupported privilege-drop platforms.
func DropPrivileges(identity Identity) error {
	return ErrUnsupported
}
