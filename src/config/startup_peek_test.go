package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPeekStartupSettingsUsesOrderedDropInsAndSkipsInvalidFiles(t *testing.T) {
	directory := t.TempDir()
	dropInDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropInDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
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
	if err := os.WriteFile(path, []byte("server:\n  pidfile: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := PeekStartupSettings(path); got.PIDFile {
		t.Fatal("server.pidfile: false should disable PID file handling")
	}
}
