// Package paths resolves platform-specific application directories.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/apimgr/countries/src/privilege"
)

const orgName = "apimgr"

var startedElevated = privilege.IsElevated()

func GetConfigDir(appName string) string {
	// Check for explicit override
	if dir := os.Getenv("CONFIG_DIR"); dir != "" {
		return dir
	}

	// Check if running in container
	if isContainer() {
		return filepath.Join("/config", appName)
	}

	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		if privilege.IsElevated() {
			return filepath.Join("/Library", "Application Support", orgName, appName)
		}
		return filepath.Join(home, "Library", "Application Support", orgName, appName)
	case "windows":
		base := os.Getenv("ProgramData")
		if !privilege.IsElevated() {
			base = os.Getenv("APPDATA")
		}
		return filepath.Join(base, orgName, appName)
	case "freebsd", "openbsd", "netbsd":
		if privilege.IsElevated() {
			return filepath.Join("/usr/local/etc", orgName, appName)
		}
		return filepath.Join(os.Getenv("HOME"), ".config", orgName, appName)
	default:
		if privilege.IsElevated() {
			return filepath.Join("/etc", orgName, appName)
		}
		// Linux/Unix - use XDG
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, orgName, appName)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", orgName, appName)
	}
}

func GetDataDir(appName string) string {
	// Check for explicit override
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return dir
	}

	// Check if running in container
	if isContainer() {
		return filepath.Join("/data", appName)
	}

	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		if privilege.IsElevated() {
			return filepath.Join("/Library", "Application Support", orgName, appName, "data")
		}
		return filepath.Join(home, "Library", "Application Support", orgName, appName, "data")
	case "windows":
		base := os.Getenv("ProgramData")
		if !privilege.IsElevated() {
			base = os.Getenv("LOCALAPPDATA")
		}
		return filepath.Join(base, orgName, appName, "data")
	case "freebsd", "openbsd", "netbsd":
		if privilege.IsElevated() {
			return filepath.Join("/var/db", orgName, appName)
		}
		return filepath.Join(os.Getenv("HOME"), ".local", "share", orgName, appName)
	default:
		if privilege.IsElevated() {
			return filepath.Join("/var/lib", orgName, appName)
		}
		// Linux/Unix - use XDG
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, orgName, appName)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", orgName, appName)
	}
}

// GetPIDFilePath returns the default server PID file path for the startup mode.
func GetPIDFilePath(appName, dataDir string, systemMode bool) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if isContainer() {
		return ""
	}
	if systemMode {
		return filepath.Join("/var/run", orgName, appName, appName+".pid")
	}
	return filepath.Join(dataDir, appName+".pid")
}

// IsContainer reports whether the process is running in a container.
func IsContainer() bool { return isContainer() }

func GetLogsDir(appName string) string {
	// Check for explicit override
	if dir := os.Getenv("LOG_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("LOGS_DIR"); dir != "" {
		return dir
	}

	// Check if running in container
	if isContainer() {
		return filepath.Join("/data", "log", appName)
	}

	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		if privilege.IsElevated() {
			return filepath.Join("/Library", "Logs", orgName, appName)
		}
		return filepath.Join(home, "Library", "Logs", orgName, appName)
	case "windows":
		base := os.Getenv("ProgramData")
		if !privilege.IsElevated() {
			base = os.Getenv("LOCALAPPDATA")
		}
		return filepath.Join(base, orgName, appName, "logs")
	case "freebsd", "openbsd", "netbsd":
		if privilege.IsElevated() {
			return filepath.Join("/var/log", orgName, appName)
		}
		return filepath.Join(os.Getenv("HOME"), ".local", "log", orgName, appName)
	default:
		if privilege.IsElevated() {
			return filepath.Join("/var/log", orgName, appName)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "log", orgName, appName)
	}
}

// GetCacheDir resolves the platform-specific cache directory.
func GetCacheDir(appName string) string {
	if dir := os.Getenv("CACHE_DIR"); dir != "" {
		return dir
	}
	if isContainer() {
		return filepath.Join("/data", appName, "cache")
	}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		if privilege.IsElevated() {
			return filepath.Join("/Library", "Caches", orgName, appName)
		}
		return filepath.Join(home, "Library", "Caches", orgName, appName)
	case "windows":
		base := os.Getenv("ProgramData")
		if !privilege.IsElevated() {
			base = os.Getenv("LOCALAPPDATA")
		}
		return filepath.Join(base, orgName, appName, "cache")
	case "freebsd", "openbsd", "netbsd":
		if privilege.IsElevated() {
			return filepath.Join("/var/cache", orgName, appName)
		}
		return filepath.Join(os.Getenv("HOME"), ".cache", orgName, appName)
	default:
		if privilege.IsElevated() {
			return filepath.Join("/var/cache", orgName, appName)
		}
		if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
			return filepath.Join(xdg, orgName, appName)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".cache", orgName, appName)
	}
}

// GetDatabaseDir resolves the SQLite directory from the environment or platform defaults.
func GetDatabaseDir(dataDir string) string {
	if dir := os.Getenv("DATABASE_DIR"); dir != "" {
		return dir
	}
	if isContainer() {
		return "/data/db/sqlite"
	}
	return filepath.Join(dataDir, "db")
}

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// GetDefaultDirs returns the default config, data, and logs directories
func GetDefaultDirs(appName string) (configDir, dataDir, logsDir string) {
	return GetConfigDir(appName), GetDataDir(appName), GetLogsDir(appName)
}

// EnsureDirs creates all specified directories
func EnsureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

// EnsurePrivateTree creates a private directory tree and enforces owner-only
// permissions on every directory and regular file below it.
func EnsurePrivateTree(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create private directory %s: %w", path, err)
	}
	return filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect private path %s: %w", current, walkErr)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect private path %s: %w", current, err)
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsDir() && !mode.IsRegular() {
			return fmt.Errorf("private path %s has unsupported mode %04o", current, mode.Perm())
		}
		permissions := os.FileMode(0600)
		if mode.IsDir() {
			permissions = 0700
		}
		if mode.Perm() != permissions {
			if err := os.Chmod(current, permissions); err != nil {
				return fmt.Errorf("cannot secure private path %s with mode %04o (current %04o): %w", current, permissions, mode.Perm(), err)
			}
		}
		return nil
	})
}

// GetBackupDir returns the backup directory for an application
func GetBackupDir(appName string) string {
	if dir := os.Getenv("BACKUP_DIR"); dir != "" {
		return dir
	}
	if isContainer() {
		return filepath.Join("/data", "backups", appName)
	}
	system := systemBackupDir(appName)
	if writableOrCreatable(system) {
		return system
	}
	if startedElevated {
		return filepath.Join(GetDataDir(appName), "backup")
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Backups", orgName, appName)
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Backups", orgName, appName)
	default:
		return filepath.Join(home, ".local", "share", "Backups", orgName, appName)
	}
}

// GetServiceAccountMarkerPath returns a root-owned marker path outside the
// service-writable runtime directories.
func GetServiceAccountMarkerPath(appName string) string {
	base := "/etc"
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join("/Library", "Application Support")
	case "freebsd", "openbsd", "netbsd":
		base = "/usr/local/etc"
	case "windows":
		return ""
	}
	return filepath.Join(base, orgName, "."+appName+"-service-account-created")
}

func systemBackupDir(appName string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join("/Library", "Backups", orgName, appName)
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "Backups", orgName, appName)
	case "freebsd", "openbsd", "netbsd":
		return filepath.Join("/var", "backups", orgName, appName)
	default:
		return filepath.Join("/mnt", "Backups", orgName, appName)
	}
}

func writableOrCreatable(path string) bool {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return false
	}
	test, err := os.CreateTemp(parent, ".countries-backup-write-test-")
	if err != nil {
		return false
	}
	name := test.Name()
	if err := test.Close(); err != nil {
		_ = os.Remove(name)
		return false
	}
	return os.Remove(name) == nil
}

// CreateTempDir creates a private, project-scoped directory in the OS temp root.
func CreateTempDir(appName string) (string, error) {
	if appName == "" || appName == "." || appName == ".." || filepath.Base(appName) != appName || strings.ContainsAny(appName, "/\\\x00") {
		return "", fmt.Errorf("invalid application name %q", appName)
	}
	orgDir := filepath.Join(os.TempDir(), orgName)
	if err := os.MkdirAll(orgDir, 0755); err != nil {
		return "", fmt.Errorf("create project temp namespace: %w", err)
	}
	dir, err := os.MkdirTemp(orgDir, appName+"-")
	if err != nil {
		return "", fmt.Errorf("create project temp directory: %w", err)
	}
	return dir, nil
}

func isContainer() bool {
	// An init system running inside an Incus/LXC system container is a service
	// host; this check must precede generic container indicators.
	if os.Getenv("INVOCATION_ID") != "" || os.Getenv("RC_SVCNAME") != "" {
		return false
	}
	if _, err := os.Stat("/run/systemd/system"); err == nil {
		return false
	}
	if _, err := os.Stat("/run/openrc"); err == nil {
		return false
	}
	// Check for .dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/dev/lxc"); err == nil {
		return true
	}
	if os.Getenv("container") != "" || os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	parentName := getParentProcessName()
	switch parentName {
	case "tini", "dumb-init", "s6-svscan", "runsv", "runsvdir", "catatonit", filepath.Base(os.Args[0]):
		return true
	}

	// Check cgroup for container indicators
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if len(content) > 0 && (contains(content, "docker") || contains(content, "kubepods") || contains(content, "lxc")) {
			return true
		}
	}

	return false
}

func getParentProcessName() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join("/proc", fmt.Sprint(os.Getppid()), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
