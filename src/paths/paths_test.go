package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testTempDir(t *testing.T) string {
	t.Helper()
	directory, err := CreateTempDir("countries")
	if err != nil {
		t.Fatalf("CreateTempDir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Fatalf("remove temporary directory: %v", err)
		}
	})
	return directory
}

func TestIsContainerTreatsSystemManagersAsHost(t *testing.T) {
	t.Setenv("RC_SVCNAME", "countries")
	if IsContainer() {
		t.Fatal("OpenRC service process was detected as an application container")
	}
	t.Setenv("RC_SVCNAME", "")
	t.Setenv("INVOCATION_ID", "test-unit")
	if IsContainer() {
		t.Fatal("systemd service process was detected as an application container")
	}
}

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
	t.Setenv("LOG_DIR", "")
	t.Setenv("LOGS_DIR", "/custom/logs")
	if got := GetLogsDir("app"); got != "/custom/logs" {
		t.Errorf("GetLogsDir() = %q, want %q", got, "/custom/logs")
	}
}

func TestGetLogsDirPrefersCanonicalEnvOverride(t *testing.T) {
	t.Setenv("LOGS_DIR", "/legacy/logs")
	t.Setenv("LOG_DIR", "/custom/logs")
	if got := GetLogsDir("app"); got != "/custom/logs" {
		t.Errorf("GetLogsDir() = %q, want canonical LOG_DIR", got)
	}
}

func TestGetCacheDirEnvOverride(t *testing.T) {
	t.Setenv("CACHE_DIR", "/custom/cache")
	if got := GetCacheDir("app"); got != "/custom/cache" {
		t.Errorf("GetCacheDir() = %q, want /custom/cache", got)
	}
}

func TestGetDatabaseDirEnvOverride(t *testing.T) {
	t.Setenv("DATABASE_DIR", "/custom/database")
	if got := GetDatabaseDir("/data"); got != "/custom/database" {
		t.Errorf("GetDatabaseDir() = %q, want /custom/database", got)
	}
}

func TestGetPIDFilePathRespectsStartupMode(t *testing.T) {
	t.Setenv("INVOCATION_ID", "test")
	t.Setenv("RC_SVCNAME", "")
	t.Setenv("container", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if runtime.GOOS == "windows" {
		if got := GetPIDFilePath("countries", "/data/countries", false); got != "" {
			t.Fatalf("user-mode PID path = %q, want empty on Windows", got)
		}
		return
	}
	if got, want := GetPIDFilePath("countries", "/data/countries", false), filepath.Join("/data/countries", "countries.pid"); got != want {
		t.Errorf("user-mode PID path = %q, want %q", got, want)
	}
	if got, want := GetPIDFilePath("countries", "/data/countries", true), filepath.Join("/var/run", "apimgr", "countries", "countries.pid"); got != want {
		t.Errorf("system-mode PID path = %q, want %q", got, want)
	}
}

func TestGetBackupDirEnvironmentOverride(t *testing.T) {
	t.Setenv("BACKUP_DIR", "/custom/backups")
	if got := GetBackupDir("countries"); got != "/custom/backups" {
		t.Fatalf("GetBackupDir() = %q, want /custom/backups", got)
	}
}

func TestGetServiceAccountMarkerPath(t *testing.T) {
	got := GetServiceAccountMarkerPath("countries")
	if runtime.GOOS == "windows" {
		if got != "" {
			t.Fatalf("Windows marker path = %q, want empty", got)
		}
		return
	}
	if !filepath.IsAbs(got) || !strings.Contains(got, ".countries-service-account-created") {
		t.Fatalf("service account marker path = %q, want absolute marker path", got)
	}
}

func TestWritableOrCreatable(t *testing.T) {
	base := testTempDir(t)
	backupPath := filepath.Join(base, "nested", "backups", "countries")
	if !writableOrCreatable(backupPath) {
		t.Fatal("writableOrCreatable() rejected a path with a writable parent")
	}
	entries, err := os.ReadDir(filepath.Dir(backupPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("writableOrCreatable() left probe artifacts: %v", entries)
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

// Container and native defaults are scoped to the application name.
func TestGetConfigDir_ContainsAppName(t *testing.T) {
	t.Setenv("CONFIG_DIR", "")
	got := GetConfigDir("myapp")
	if isContainer() {
		if got != "/config/myapp" {
			t.Errorf("GetConfigDir() in container = %q, want /config/myapp", got)
		}
		return
	}
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetConfigDir() = %q, want it to contain appName", got)
	}
}

// Container and native defaults are scoped to the application name.
func TestGetDataDir_ContainsAppName(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	got := GetDataDir("myapp")
	if isContainer() {
		if got != "/data/myapp" {
			t.Errorf("GetDataDir() in container = %q, want /data/myapp", got)
		}
		return
	}
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetDataDir() = %q, want it to contain appName", got)
	}
}

// Container and native defaults are scoped to the application name.
func TestGetLogsDir_ContainsAppName(t *testing.T) {
	t.Setenv("LOG_DIR", "")
	t.Setenv("LOGS_DIR", "")
	got := GetLogsDir("myapp")
	if isContainer() {
		if got != "/data/log/myapp" {
			t.Errorf("GetLogsDir() in container = %q, want /data/log/myapp", got)
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
	t.Setenv("LOG_DIR", "")
	t.Setenv("LOGS_DIR", "/logs")

	cfg, data, logs := GetDefaultDirs("app")
	if cfg != "/cfg" || data != "/data" || logs != "/logs" {
		t.Errorf("GetDefaultDirs() = (%q, %q, %q), want (/cfg, /data, /logs)", cfg, data, logs)
	}
}

// EnsureDir must create nested directories that do not yet exist.
func TestEnsureDir(t *testing.T) {
	base := testTempDir(t)
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
	base := testTempDir(t)

	if err := EnsureDir(base); err != nil {
		t.Fatalf("first EnsureDir() error = %v", err)
	}
	if err := EnsureDir(base); err != nil {
		t.Fatalf("second EnsureDir() error = %v", err)
	}
}

func TestEnsureDirs(t *testing.T) {
	base := testTempDir(t)
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

func TestEnsurePrivateTreeSecuresNestedDirectoriesAndFiles(t *testing.T) {
	base := testTempDir(t)
	root := filepath.Join(base, "private")
	nested := filepath.Join(root, "tor", "site")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "server.yml")
	if err := os.WriteFile(config, []byte("server: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(nested, "private.key")
	if err := os.WriteFile(key, []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateTree(root); err != nil {
		t.Fatalf("EnsurePrivateTree() error = %v", err)
	}
	for _, path := range []string{root, filepath.Join(root, "tor"), nested} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat private directory %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0700 {
			t.Errorf("directory %s permissions = %04o, want 0700", path, got)
		}
	}
	for _, path := range []string{config, key} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat private file %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("file %s permissions = %04o, want 0600", path, got)
		}
	}
}

func TestEnsurePrivateTreeRejectsSymlinks(t *testing.T) {
	base := testTempDir(t)
	root := filepath.Join(base, "private")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateTree(root); err == nil {
		t.Fatal("EnsurePrivateTree() accepted a symlink")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("symlink target changed: %q, err=%v", data, err)
	}
}

func TestGetBackupDir_ContainsAppName(t *testing.T) {
	t.Setenv("BACKUP_DIR", "")
	got := GetBackupDir("myapp")
	if !strings.Contains(got, "myapp") {
		t.Errorf("GetBackupDir() = %q, want it to contain appName", got)
	}
}

func TestCreateTempDirUsesProjectNamespaceAndPrivatePermissions(t *testing.T) {
	tempRoot := filepath.Join(os.TempDir(), orgName)
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatalf("create temp namespace: %v", err)
	}
	tempBase, err := os.MkdirTemp(tempRoot, "countries-test-")
	if err != nil {
		t.Fatalf("create test temp base: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempBase) })
	t.Setenv("TMPDIR", tempBase)
	dir, err := CreateTempDir("countries")
	if err != nil {
		t.Fatalf("CreateTempDir() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if filepath.Dir(filepath.Dir(dir)) != os.TempDir() {
		t.Errorf("CreateTempDir() = %q, want a directory nested below the OS temp root and project namespace", dir)
	}
	if filepath.Base(filepath.Dir(dir)) != orgName {
		t.Errorf("CreateTempDir() parent = %q, want %q", filepath.Base(filepath.Dir(dir)), orgName)
	}
	if !strings.HasPrefix(filepath.Base(dir), "countries-") {
		t.Errorf("CreateTempDir() basename = %q, want countries- prefix", filepath.Base(dir))
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat temp directory: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("temp directory permissions = %04o, want 0700", info.Mode().Perm())
	}
}

func TestCreateTempDirRejectsPathNames(t *testing.T) {
	for _, name := range []string{"", "..", "../other", "/tmp/other"} {
		if _, err := CreateTempDir(name); err == nil {
			t.Errorf("CreateTempDir(%q) succeeded, want invalid-name error", name)
		}
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
