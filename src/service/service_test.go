package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicePathsAndUnsupportedManager(t *testing.T) {
	if got, want := GetBinaryPath(), "/usr/local/bin/countries"; got != want {
		t.Errorf("GetBinaryPath() = %q, want %q", got, want)
	}
	if got := DetectServiceManager(); got != ServiceUnknown {
		t.Skipf("container exposes service manager %v; unsupported-manager paths do not apply", got)
	}
	for name, operation := range map[string]func() error{
		"install":   Install,
		"uninstall": Uninstall,
		"start":     Start,
		"stop":      Stop,
		"restart":   Restart,
		"reload":    Reload,
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); err == nil {
				t.Errorf("%s() returned nil for an unsupported service manager", name)
			}
		})
	}
}

func TestServiceControlUsesSelectedManager(t *testing.T) {
	binDir := t.TempDir()
	logPath := t.TempDir() + "/systemctl.log"
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_SYSTEMCTL_LOG"
`
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_SYSTEMCTL_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	oldDetect := detectServiceManager
	detectServiceManager = func() ServiceType { return ServiceSystemd }
	t.Cleanup(func() { detectServiceManager = oldDetect })

	for name, operation := range map[string]func() error{
		"start":   Start,
		"stop":    Stop,
		"restart": Restart,
		"reload":  Reload,
		"status":  Status,
		"disable": Disable,
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); err != nil {
				t.Fatalf("%s() error = %v", name, err)
			}
		})
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"start countries", "stop countries", "restart countries", "reload countries", "status countries", "disable countries"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("systemctl log %q missing %q", log, want)
		}
	}
}
