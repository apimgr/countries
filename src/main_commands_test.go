package main

import (
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
)

func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestCheckHealth(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/healthz" {
			t.Errorf("health path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	if err := checkHealth(strconv.Itoa(port)); err != nil {
		t.Fatalf("checkHealth() error = %v", err)
	}
	if err := checkHealth("1"); err == nil {
		t.Fatal("checkHealth() on an unused port returned nil")
	}
	badListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	badPort := badListener.Addr().(*net.TCPAddr).Port
	badServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})}
	go func() { _ = badServer.Serve(badListener) }()
	t.Cleanup(func() { _ = badServer.Close() })
	if err := checkHealth(strconv.Itoa(badPort)); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("checkHealth() unexpected status error = %v", err)
	}
}

func TestMaintenanceAndModeCommands(t *testing.T) {
	releaseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	defer releaseServer.Close()
	previousReleaseURL := updaterReleaseURL
	updaterReleaseURL = releaseServer.URL
	t.Cleanup(func() { updaterReleaseURL = previousReleaseURL })
	configPath := filepath.Join(t.TempDir(), "server.yml")
	cfg := config.DefaultConfig()
	cfg.Server.Port = "54321"
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	if output := captureOutput(t, func() { setApplicationMode("development", configPath) }); !strings.Contains(output, "development") {
		t.Fatalf("mode output = %q", output)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Mode != "development" {
		t.Fatalf("saved mode = %q", loaded.Server.Mode)
	}

	oldFlags := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = oldFlags })
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if output := captureOutput(t, func() {
		handleMaintenanceCommand("mode", "", t.TempDir(), t.TempDir(), configPath)
	}); !strings.Contains(output, "development") {
		t.Fatalf("maintenance mode output = %q", output)
	}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if output := captureOutput(t, func() {
		handleMaintenanceCommand("setup", "", t.TempDir(), t.TempDir(), configPath)
	}); !strings.Contains(output, "Setup complete") {
		t.Fatalf("maintenance setup output = %q", output)
	}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if output := captureOutput(t, func() { _ = handleUpdateCommand("check", cfg) }); !strings.Contains(output, "up to date") {
		t.Fatalf("update check output = %q", output)
	}
	if output := captureOutput(t, func() { _ = handleUpdateCommand("yes", cfg) }); !strings.Contains(output, "up to date") {
		t.Fatalf("update install output = %q", output)
	}
	if output := captureOutput(t, func() { _ = handleUpdateCommand("branch", cfg) }); !strings.Contains(output, "stable") {
		t.Fatalf("update branch output = %q", output)
	}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse([]string{"beta"}); err != nil {
		t.Fatal(err)
	}
	if output := captureOutput(t, func() { _ = handleUpdateCommand("branch", cfg) }); !strings.Contains(output, "beta") {
		t.Fatalf("update branch selection output = %q", output)
	}
	if cfg.Server.Update.Branch != "beta" {
		t.Fatalf("update branch = %q", cfg.Server.Update.Branch)
	}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if output := captureOutput(t, func() { handleMaintenanceCommand("update", "", t.TempDir(), t.TempDir(), configPath) }); !strings.Contains(output, "up to date") {
		t.Fatalf("maintenance update output = %q", output)
	}
}

func TestServiceHelpAndRunCommand(t *testing.T) {
	if output := captureOutput(t, func() { handleServiceCommand("--help", t.TempDir()) }); !strings.Contains(output, "Service commands:") {
		t.Fatalf("service help output = %q", output)
	}
	runCmd("true")
	runCmd("countries-test-command-that-does-not-exist")
}

func TestMaintenanceBackupAndRestoreUseTempPaths(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "config")
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("safe: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(tempDir, "backup.tar.gz")
	oldFlags := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = oldFlags })
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse([]string{archive}); err != nil {
		t.Fatal(err)
	}
	captureOutput(t, func() { handleMaintenanceCommand("backup", configDir, dataDir, t.TempDir(), "") })
	if info, err := os.Stat(archive); err != nil || info.Size() == 0 {
		t.Fatalf("backup archive info=%v err=%v", info, err)
	}

	payload := filepath.Join(tempDir, "restore-payload")
	if err := os.MkdirAll(payload, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(payload, "restored.txt")
	if err := os.WriteFile(marker, []byte("restored"), 0600); err != nil {
		t.Fatal(err)
	}
	restoreArchive := filepath.Join(tempDir, "restore.tar.gz")
	relativePayload, err := filepath.Rel(string(filepath.Separator), payload)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("tar", "-czf", restoreArchive, "-C", string(filepath.Separator), relativePayload).CombinedOutput(); err != nil {
		t.Fatalf("create restore fixture: %v: %s", err, output)
	}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse([]string{restoreArchive}); err != nil {
		t.Fatal(err)
	}
	captureOutput(t, func() { handleMaintenanceCommand("restore", "", "", "", "") })
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "restored" {
		t.Fatalf("restored marker = %q, err=%v", contents, err)
	}
}

func TestResolveServerPortPrecedence(t *testing.T) {
	for _, tc := range []struct {
		flagPort, envPort, configPort, want string
	}{
		{"80", "90", "8080", "80"},
		{"", "90", "8080", "90"},
		{"", "", "8080", "8080"},
		{"", "", "", "8080"},
		{"  ", "  ", "  ", "8080"},
	} {
		if got := resolveServerPort(tc.flagPort, tc.envPort, tc.configPort); got != tc.want {
			t.Errorf("resolveServerPort(%q, %q, %q) = %q, want %q", tc.flagPort, tc.envPort, tc.configPort, got, tc.want)
		}
	}
}
