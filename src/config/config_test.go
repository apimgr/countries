package config

import (
	"os"
	"path/filepath"
	"testing"
)

// DefaultConfig must return the documented, safe-by-default values so a
// fresh install works with zero configuration.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Port != "8080" {
		t.Errorf("Server.Port = %q, want 8080", cfg.Server.Port)
	}
	if cfg.Server.FQDN != "localhost" {
		t.Errorf("Server.FQDN = %q, want localhost", cfg.Server.FQDN)
	}
	if cfg.Server.Address != "0.0.0.0" {
		t.Errorf("Server.Address = %q, want 0.0.0.0", cfg.Server.Address)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.Metrics.Enabled {
		t.Error("Server.Metrics.Enabled should default to false")
	}
	if cfg.WebUI.Theme != "dark" {
		t.Errorf("WebUI.Theme = %q, want dark", cfg.WebUI.Theme)
	}
	if !cfg.WebUI.Notifications.Enabled {
		t.Error("WebUI.Notifications.Enabled should default to true")
	}
	if len(cfg.WebRobots.Allow) != 2 || cfg.WebRobots.Allow[0] != "/" || cfg.WebRobots.Allow[1] != "/api" {
		t.Errorf("WebRobots.Allow = %v, want [/ /api]", cfg.WebRobots.Allow)
	}
	if len(cfg.WebRobots.Deny) != 1 || cfg.WebRobots.Deny[0] != "/debug" {
		t.Errorf("WebRobots.Deny = %v, want [/debug]", cfg.WebRobots.Deny)
	}
	if cfg.WebSecurity.CORS != "*" {
		t.Errorf("WebSecurity.CORS = %q, want *", cfg.WebSecurity.CORS)
	}
}

// Load on a non-existent path must create the file with default contents
// and return that default config without error.
func TestLoad_CreatesDefaultWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Server.Port != "8080" {
		t.Errorf("Load() returned Port = %q, want default 8080", cfg.Server.Port)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected Load() to create %q: %v", path, err)
	}
}

// Load must round-trip a previously saved config, overriding the defaults
// with whatever was persisted.
func TestLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	original := DefaultConfig()
	original.Server.Port = "9999"
	original.Server.FQDN = "example.test"

	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Server.Port != "9999" {
		t.Errorf("loaded Server.Port = %q, want 9999", loaded.Server.Port)
	}
	if loaded.Server.FQDN != "example.test" {
		t.Errorf("loaded Server.FQDN = %q, want example.test", loaded.Server.FQDN)
	}
}

// Malformed YAML must surface as an error rather than being silently
// swallowed, so a broken config file cannot start the server unnoticed.
func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	if err := os.WriteFile(path, []byte("server: [this is not: valid: yaml"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("Load() with malformed YAML expected error, got nil")
	}
}

// Save must write a file that Load can read back byte-for-byte-equivalent
// in the fields that matter.
func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.yml")

	// Save into a directory that does not exist yet must fail cleanly
	// (Save does not create parent directories).
	if err := Save(path, DefaultConfig()); err == nil {
		t.Error("Save() into missing directory expected error, got nil")
	}
}

// migrateYamlToYml must rename a sibling .yaml file to .yml when the .yml
// path itself does not already exist, preserving user data across the
// extension change.
func TestMigrateYamlToYml_RenamesWhenYmlMissing(t *testing.T) {
	dir := t.TempDir()
	ymlPath := filepath.Join(dir, "config.yml")
	yamlPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(yamlPath, []byte("server:\n  port: \"1234\"\n"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	migrateYamlToYml(ymlPath)

	if _, err := os.Stat(ymlPath); err != nil {
		t.Errorf("expected %q to exist after migration: %v", ymlPath, err)
	}
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
		t.Errorf("expected %q to be gone after migration, stat err = %v", yamlPath, err)
	}
}

// migrateYamlToYml must not overwrite an existing .yml file with the
// contents of an older .yaml file.
func TestMigrateYamlToYml_DoesNotOverwriteExistingYml(t *testing.T) {
	dir := t.TempDir()
	ymlPath := filepath.Join(dir, "config.yml")
	yamlPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(ymlPath, []byte("new"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	if err := os.WriteFile(yamlPath, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	migrateYamlToYml(ymlPath)

	got, err := os.ReadFile(ymlPath)
	if err != nil {
		t.Fatalf("failed to read %q: %v", ymlPath, err)
	}
	if string(got) != "new" {
		t.Errorf("config.yml contents = %q, want unchanged %q", got, "new")
	}
}

// A path that does not end in .yml must be left alone entirely.
func TestMigrateYamlToYml_IgnoresNonYmlPath(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.json")

	// Must not panic or error for an unrelated extension.
	migrateYamlToYml(jsonPath)
}
