package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigWatcherAppliesDropInAndReportsRestartSettings(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "server.yml")
	initial := DefaultConfig()
	initial.Server.Port = "64001"
	if err := Save(configPath, initial); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	dropInDir := filepath.Join(configDir, "yaml.d")
	if err := os.MkdirAll(dropInDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	var applied *Config
	var restartSettings []string
	watcher := NewConfigWatcher(configPath, initial, func(_, next *Config, restart []string) {
		applied = next
		restartSettings = restart
	})
	dropInPath := filepath.Join(dropInDir, "branding.yml")
	if err := os.WriteFile(dropInPath, []byte("server:\n  branding:\n    title: Reloaded title\n  port: \"64002\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	watcher.Check()
	if applied == nil {
		t.Fatal("watcher did not apply changed configuration")
	}
	if got, want := applied.Server.Branding.Title, "Reloaded title"; got != want {
		t.Errorf("live branding title = %q, want %q", got, want)
	}
	if got, want := applied.Server.Port, initial.Server.Port; got != want {
		t.Errorf("live port = %q, want running port %q", got, want)
	}
	if want := []string{"server.port"}; !reflect.DeepEqual(restartSettings, want) {
		t.Errorf("restart settings = %v, want %v", restartSettings, want)
	}
}

func TestConfigWatcherReloadNowUsesSameReloadPath(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "server.yml")
	initial := DefaultConfig()
	if err := Save(configPath, initial); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var calls int
	watcher := NewConfigWatcher(configPath, initial, func(_, _ *Config, _ []string) { calls++ })
	if err := watcher.ReloadNow(); err != nil {
		t.Fatalf("ReloadNow() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("reload callback calls = %d, want 1", calls)
	}
}
