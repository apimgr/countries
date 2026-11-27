package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
)

func TestOpenWritesConfiguredKindsAndSanitizesEvents(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig().Server.Logging
	cfg.Level = "debug"
	cfg.Server.Format = "json"
	cfg.Security.Format = "cef"
	cfg.Debug.Enabled = true
	manager, err := Open(dir, cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = manager.Close() }()
	if err := manager.Log(Event{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Level: "error", Kind: "security", Message: "bad\n\x1b[31mcredential 😀", Fields: map[string]any{"request_id": "abc", "api_token": "never-write"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"server.log", "security.log", "error.log"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "never-write") || strings.ContainsAny(string(data), "\x1b\r") || strings.Contains(string(data), "😀") {
			t.Errorf("%s contains unsafe data: %q", name, data)
		}
		if !strings.HasSuffix(string(data), "\n") {
			t.Errorf("%s does not end with a line separator", name)
		}
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		} else if info.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions = %o, want 600", name, info.Mode().Perm())
		}
	}
}

func TestDebugLevelAndFileAreOptIn(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig().Server.Logging
	manager, err := Open(dir, cfg, "production")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Log(Event{Level: "debug", Kind: "server", Message: "hidden"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "debug.log")); !os.IsNotExist(err) {
		t.Fatalf("debug.log exists when disabled: %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "hidden") {
		t.Fatalf("below-threshold event was written: %s", contents)
	}
}

func TestRotationPolicies(t *testing.T) {
	for _, input := range []string{"never", "daily", "weekly,50MB", "1GB"} {
		if _, err := parseRotation(input); err != nil {
			t.Errorf("parseRotation(%q): %v", input, err)
		}
	}
	for _, input := range []string{"", "bogus", "0MB"} {
		if _, err := parseRotation(input); err == nil {
			t.Errorf("parseRotation(%q) succeeded", input)
		}
	}
	for _, input := range []string{"none", "forever", "3", "7d", "2w", "1m", "weekly:4"} {
		if err := validRetention(input); err != nil {
			t.Errorf("validRetention(%q): %v", input, err)
		}
	}
}
