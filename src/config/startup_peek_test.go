package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPeekStartupSettingsUsesOrderedDropInsAndSkipsInvalidFiles(t *testing.T) {
	directory := t.TempDir()
	dropInDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropInDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(directory, "server.yml"), "server:\n  port: '8111'\n  address: 127.0.0.1\n  daemonize: true\n")
	write(filepath.Join(dropInDirectory, "10-first.yml"), "server:\n  port: '8222'\n  daemonize: false\n")
	write(filepath.Join(dropInDirectory, "20-invalid.yml"), "server: [invalid\n")
	write(filepath.Join(dropInDirectory, "30-last.yml"), "server:\n  address: '[::1]'\n")
	write(filepath.Join(directory, "secrets.yml"), "server:\n  port: '9999'\n")

	settings := PeekStartupSettings(filepath.Join(directory, "server.yml"))
	if settings.Port != "8222" {
		t.Errorf("Port = %q, want last valid override 8222", settings.Port)
	}
	if settings.Address != "[::1]" {
		t.Errorf("Address = %q, want last valid override [::1]", settings.Address)
	}
	if settings.Daemonize {
		t.Error("Daemonize = true, want later false override")
	}
}

func TestPeekStartupSettingsPIDFileDefaultsAndOverrides(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.yml")
	if got := PeekStartupSettings(path); !got.PIDFile {
		t.Fatal("missing server.pidfile should use the enabled default")
	}
	if err := os.WriteFile(path, []byte("server:\n  pidfile: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := PeekStartupSettings(path); got.PIDFile {
		t.Fatal("server.pidfile: false should disable PID file handling")
	}
}

func TestPeekStartupSettingsReadsLegacyYAMLFile(t *testing.T) {
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "server.yaml")
	if err := os.WriteFile(legacyPath, []byte("server:\n  port: '8124'\n  address: 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := PeekStartupSettings(filepath.Join(directory, "server.yml"))
	if settings.Port != "8124" || settings.Address != "127.0.0.1" {
		t.Fatalf("legacy startup settings = %+v, want port 8124 and address 127.0.0.1", settings)
	}
}

func TestPeekStartupSettingsDoesNotCreateOrChangeFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "missing")
	configPath := filepath.Join(directory, "server.yml")
	if got := PeekStartupSettings(configPath); !got.PIDFile {
		t.Fatal("missing server.pidfile should use the enabled default")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("PeekStartupSettings created the config directory: stat error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(directory, "yaml.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("server:\n  port: '8123'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modeBefore := fileMode(t, configPath)
	if got := PeekStartupSettings(configPath); got.Port != "8123" {
		t.Fatalf("Port = %q, want 8123", got.Port)
	}
	if modeAfter := fileMode(t, configPath); modeAfter != modeBefore {
		t.Errorf("PeekStartupSettings changed server.yml mode from %04o to %04o", modeBefore, modeAfter)
	}
}

func TestPrepareStartupConfigDoesNotChangeSymlinkTargetMode(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "external.yml")
	configPath := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(target, []byte("server:\n  port: '8123'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	if err := PrepareStartupConfig(configPath); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, target); got != 0o644 {
		t.Fatalf("PrepareStartupConfig changed symlink target mode to %04o, want 0644", got)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
