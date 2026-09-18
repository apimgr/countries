package service

import (
	"runtime"
	"strings"
	"testing"
)

// DetectServiceManager is a pure runtime.GOOS switch plus read-only stat
// checks; only the current OS's branch is deterministically verifiable in
// CI, so assert the contract that matters on Linux (never panics, returns
// a defined ServiceType) and the fixed mappings for the other platforms
// via the exported constants.
func TestDetectServiceManager_ReturnsKnownType(t *testing.T) {
	got := DetectServiceManager()

	switch runtime.GOOS {
	case "linux":
		if got != ServiceSystemd && got != ServiceRunit && got != ServiceUnknown {
			t.Errorf("DetectServiceManager() on linux = %v, want systemd/runit/unknown", got)
		}
	case "darwin":
		if got != ServiceLaunchd {
			t.Errorf("DetectServiceManager() on darwin = %v, want ServiceLaunchd", got)
		}
	case "windows":
		if got != ServiceWindows {
			t.Errorf("DetectServiceManager() on windows = %v, want ServiceWindows", got)
		}
	default:
		// freebsd/openbsd/netbsd/other: just must not panic.
		_ = got
	}
}

// GetBinaryPath must return a platform-appropriate absolute install path
// containing the app name.
func TestGetBinaryPath(t *testing.T) {
	got := GetBinaryPath()
	if got == "" {
		t.Fatal("GetBinaryPath() returned empty string")
	}
	if !strings.Contains(got, "countries") {
		t.Errorf("GetBinaryPath() = %q, want it to contain the app name", got)
	}

	if runtime.GOOS == "windows" {
		if !strings.Contains(got, `Program Files`) {
			t.Errorf("GetBinaryPath() on windows = %q, want a Program Files path", got)
		}
	} else {
		if !strings.HasPrefix(got, "/usr/local/bin/") {
			t.Errorf("GetBinaryPath() on %s = %q, want /usr/local/bin/ prefix", runtime.GOOS, got)
		}
	}
}

// The ServiceType constants must remain distinct — a collision here would
// silently break DetectServiceManager()'s callers' switch statements.
func TestServiceTypeConstantsAreDistinct(t *testing.T) {
	types := []ServiceType{ServiceUnknown, ServiceSystemd, ServiceRunit, ServiceLaunchd, ServiceWindows, ServiceBSDRC}
	seen := make(map[ServiceType]bool)
	for _, ty := range types {
		if seen[ty] {
			t.Errorf("duplicate ServiceType value: %v", ty)
		}
		seen[ty] = true
	}
}

// Install and Uninstall are thin dispatchers over DetectServiceManager's
// result. In the containerized test environment there is no
// /run/systemd/system, /run/runit, or /etc/systemd, so on Linux
// DetectServiceManager reliably returns ServiceUnknown and both functions
// take their "unsupported service manager" error branch without touching
// the filesystem or shelling out — the install*/uninstall* branches for a
// real service manager are out of scope (they write real unit files and
// invoke systemctl/etc.).
func TestInstall_UnsupportedServiceManager(t *testing.T) {
	if runtime.GOOS != "linux" || DetectServiceManager() != ServiceUnknown {
		t.Skip("only safe to exercise the unsupported-manager branch on a plain Linux container")
	}

	if err := Install(); err == nil {
		t.Error("Install() with no detected service manager: error = nil, want non-nil")
	} else if !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Install() error = %v, want 'unsupported service manager'", err)
	}
}

// See TestInstall_UnsupportedServiceManager for why this branch is safe to
// exercise here.
func TestUninstall_UnsupportedServiceManager(t *testing.T) {
	if runtime.GOOS != "linux" || DetectServiceManager() != ServiceUnknown {
		t.Skip("only safe to exercise the unsupported-manager branch on a plain Linux container")
	}

	if err := Uninstall(); err == nil {
		t.Error("Uninstall() with no detected service manager: error = nil, want non-nil")
	} else if !strings.Contains(err.Error(), "unsupported service manager") {
		t.Errorf("Uninstall() error = %v, want 'unsupported service manager'", err)
	}
}
