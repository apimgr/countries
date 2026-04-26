package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

const orgName = "apimgr"

func GetConfigDir(appName string) string {
	// Check for explicit override
	if dir := os.Getenv("CONFIG_DIR"); dir != "" {
		return dir
	}

	// Check if running in container
	if isContainer() {
		return "/config"
	}

	// System-wide config for root user
	if os.Getuid() == 0 {
		return filepath.Join("/etc", orgName, appName)
	}

	// User config directory
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", orgName, appName)
	case "windows":
		return filepath.Join(os.Getenv("APPDATA"), orgName, appName)
	default:
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
		return "/data"
	}

	// System-wide data for root user
	if os.Getuid() == 0 {
		return filepath.Join("/var/lib", orgName, appName)
	}

	// User data directory
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", orgName, appName, "data")
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), orgName, appName, "data")
	default:
		// Linux/Unix - use XDG
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, orgName, appName)
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", orgName, appName)
	}
}

func GetLogsDir(appName string) string {
	// Check for explicit override
	if dir := os.Getenv("LOGS_DIR"); dir != "" {
		return dir
	}

	// Check if running in container
	if isContainer() {
		return "/logs"
	}

	// System-wide logs for root user
	if os.Getuid() == 0 {
		return filepath.Join("/var/log", orgName, appName)
	}

	// User logs directory
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Logs", orgName, appName)
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), orgName, appName, "logs")
	default:
		// Linux/Unix - use XDG
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			return filepath.Join(xdg, orgName, appName, "logs")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "state", orgName, appName, "logs")
	}
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

// GetBackupDir returns the backup directory for an application
func GetBackupDir(appName string) string {
	if os.Getuid() == 0 {
		return filepath.Join("/var/backups", orgName, appName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", orgName, appName, "backups")
}

func isContainer() bool {
	// Check for .dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check cgroup for container indicators
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if len(content) > 0 && (contains(content, "docker") || contains(content, "kubepods") || contains(content, "containerd")) {
			return true
		}
	}

	return false
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
