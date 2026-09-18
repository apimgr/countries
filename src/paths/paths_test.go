package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the environment-variable override paths, since those are
// deterministic across any OS/container/user without needing root.
func TestGetConfigDir_EnvOverride(t *testing.T) {
	t.Setenv("CONFIG_DIR", "/custom/config")
	if got := GetConfigDir("app"); got != "/custom/config" {
		t.Errorf("GetConfigDir() = %q, want %q", got, "/custom/config")
	}
}

func TestGetDataDir_EnvOverride(t *testing.T) {
	t.Setenv("DATA_DIR", "/custom/data")
	if got := GetDataDir("app"); got != "/custom/data" {
		t.Errorf("GetDataDir() = %q, want %q", got, "/custom/data")
	}
}

func TestGetLogsDir_EnvOverride(t *testing.T) {
	t.Setenv("LOGS_DIR", "/custom/logs")
	if got := GetLogsDir("app"); got != "/custom/logs" {
		t.Errorf("GetLogsDir() = %q, want %q", got, "/custom/logs")
	}
}

// Empty overrides must fall through to the normal resolution logic
// rather than being treated as a valid explicit path.
func TestGetConfigDir_EmptyEnvFallsThrough(t *testing.T) {
	t.Setenv("CONFIG_DIR", "")
	got := GetConfigDir("app")
	if got == "" {
		t.Error("GetConfigDir() returned empty string when env override is unset")
	}
}

// Inside a container isContainer() short-circuits to a fixed path
// ("/config") that intentionally omits appName; outside a container the
// path is derived per-OS and must embed appName.
func TestGetConfigDir_ContainsAppName(t *testing.T) {
	t.Setenv("CONFIG_DIR", "")
	got := GetConfigDir("myapp")
	if isContainer() {
		if got != "/config" {
			t.Errorf("GetConfigDir() in container = %q, want /config", got)
		}
		return
	}
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetConfigDir() = %q, want it to contain appName", got)
	}
}

// Inside a container isContainer() short-circuits to a fixed path
// ("/data") that intentionally omits appName; outside a container the
// path is derived per-OS and must embed appName.
func TestGetDataDir_ContainsAppName(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	got := GetDataDir("myapp")
	if isContainer() {
		if got != "/data" {
			t.Errorf("GetDataDir() in container = %q, want /data", got)
		}
		return
	}
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetDataDir() = %q, want it to contain appName", got)
	}
}

// Inside a container isContainer() short-circuits to a fixed path
// ("/logs") that intentionally omits appName; outside a container the
// path is derived per-OS and must embed appName.
func TestGetLogsDir_ContainsAppName(t *testing.T) {
	t.Setenv("LOGS_DIR", "")
	got := GetLogsDir("myapp")
	if isContainer() {
		if got != "/logs" {
			t.Errorf("GetLogsDir() in container = %q, want /logs", got)
		}
		return
	}
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetLogsDir() = %q, want it to contain appName", got)
	}
}

func TestGetDefaultDirs(t *testing.T) {
	t.Setenv("CONFIG_DIR", "/cfg")
	t.Setenv("DATA_DIR", "/data")
	t.Setenv("LOGS_DIR", "/logs")

	cfg, data, logs := GetDefaultDirs("app")
	if cfg != "/cfg" || data != "/data" || logs != "/logs" {
		t.Errorf("GetDefaultDirs() = (%q, %q, %q), want (/cfg, /data, /logs)", cfg, data, logs)
	}
}

// EnsureDir must create nested directories that do not yet exist.
func TestEnsureDir(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "a", "b", "c")

	if err := EnsureDir(nested); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected path to be a directory")
	}
}

// EnsureDir on an already-existing directory must be idempotent.
func TestEnsureDir_Idempotent(t *testing.T) {
	base := t.TempDir()

	if err := EnsureDir(base); err != nil {
		t.Fatalf("first EnsureDir() error = %v", err)
	}
	if err := EnsureDir(base); err != nil {
		t.Fatalf("second EnsureDir() error = %v", err)
	}
}

func TestEnsureDirs(t *testing.T) {
	base := t.TempDir()
	dir1 := filepath.Join(base, "one")
	dir2 := filepath.Join(base, "two")

	if err := EnsureDirs(dir1, dir2); err != nil {
		t.Fatalf("EnsureDirs() error = %v", err)
	}

	for _, d := range []string{dir1, dir2} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("expected %q to exist: %v", d, err)
		}
	}
}

// EnsureDirs with zero arguments should be a safe no-op.
func TestEnsureDirs_Empty(t *testing.T) {
	if err := EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs() with no args error = %v", err)
	}
}

func TestGetBackupDir_ContainsAppName(t *testing.T) {
	got := GetBackupDir("myapp")
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetBackupDir() = %q, want it to contain appName", got)
	}
}

// contains/containsHelper are the package's own substring implementation;
// exercise boundary conditions directly since isContainer() depends on them.
func TestContains(t *testing.T) {
	cases := []struct {
		s, substr string
		want      bool
	}{
		{"docker daemon", "docker", true},
		{"kubepods-burstable", "kubepods", true},
		{"nothing here", "docker", false},
		{"", "", true},
		{"abc", "", true},
		{"", "abc", false},
		{"exact", "exact", true},
	}
	for _, c := range cases {
		if got := contains(c.s, c.substr); got != c.want {
			t.Errorf("contains(%q, %q) = %v, want %v", c.s, c.substr, got, c.want)
		}
	}
}

func TestContainsHelper(t *testing.T) {
	if !containsHelper("hello world", "world") {
		t.Error("containsHelper() should find substring at end")
	}
	if containsHelper("hello world", "xyz") {
		t.Error("containsHelper() should not find absent substring")
	}
	// Guard against index-out-of-range when substr is longer than s.
	if containsHelper("ab", "abc") {
		t.Error("containsHelper() should return false when substr longer than s")
	}
}
