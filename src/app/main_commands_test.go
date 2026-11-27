package app

import (
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/backup"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	appmode "github.com/apimgr/countries/src/mode"
	"github.com/apimgr/countries/src/testutil"
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
	configPath := filepath.Join(testutil.TempDir(t), "server.yml")
	cfg := config.DefaultConfig()
	cfg.Server.Port = "54321"
	cfg.Server.Mode = "development"
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	if got := resolveRuntimeMode("debug", "production", cfg.Server.Mode); got != "debug" {
		t.Fatalf("CLI mode priority = %q, want debug", got)
	}
	if got := resolveRuntimeMode("", "production", cfg.Server.Mode); got != "production" {
		t.Fatalf("environment mode priority = %q, want production", got)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Mode != "development" {
		t.Fatalf("saved mode = %q", loaded.Server.Mode)
	}
	if err := appmode.Initialize(loaded.Server.Mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appmode.Initialize("production") })

	oldFlags := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = oldFlags })
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if output := captureOutput(t, func() {
		handleMaintenanceCommand("mode", "", testutil.TempDir(t), testutil.TempDir(t), configPath)
	}); !strings.Contains(output, "development") {
		t.Fatalf("maintenance mode output = %q", output)
	}
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if output := captureOutput(t, func() {
		handleMaintenanceCommand("setup", "", testutil.TempDir(t), testutil.TempDir(t), configPath)
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
	if output := captureOutput(t, func() { handleMaintenanceCommand("update", "", testutil.TempDir(t), testutil.TempDir(t), configPath) }); !strings.Contains(output, "up to date") {
		t.Fatalf("maintenance update output = %q", output)
	}
}

func TestServiceHelpAndRunCommand(t *testing.T) {
	if output := captureOutput(t, func() { handleServiceCommand("--help", testutil.TempDir(t)) }); !strings.Contains(output, "Service commands:") {
		t.Fatalf("service help output = %q", output)
	}
	runCmd("true")
	runCmd("countries-test-command-that-does-not-exist")
}

func TestMaintenanceBackupAndRestoreUseTempPaths(t *testing.T) {
	tempDir := testutil.TempDir(t)
	configDir := filepath.Join(tempDir, "config")
	dataDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "server.yml")
	cfg := config.DefaultConfig()
	cfg.Server.Backup.Password = "test-backup-password"
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(dataDir, "db", "server.db")
	t.Setenv("DATABASE_DIR", filepath.Dir(databasePath))
	db, err := database.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	filename, includeSSL, includeData, err := parseBackupArgs([]string{"--include-data", "--include-ssl"})
	if err != nil || filename != "" || !includeSSL || !includeData {
		t.Fatalf("parseBackupArgs() = (%q, %v, %v, %v)", filename, includeSSL, includeData, err)
	}
	options, err := manualBackupOptions(configDir, dataDir, cfg, includeSSL, includeData)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(tempDir, "countries_backup_2026-11-22_010203.tar.gz.enc")
	created, err := backup.Create(t.Context(), archive, options)
	if err != nil {
		t.Fatalf("backup.Create() error = %v", err)
	}
	if !created.Manifest.Encrypted {
		t.Fatal("backup manifest is not encrypted")
	}
	if _, err := backup.Verify(t.Context(), archive, options.Password, options.Limits); err != nil {
		t.Fatalf("backup.Verify() error = %v", err)
	}
	restoredConfig := filepath.Join(tempDir, "restored-config")
	restoredData := filepath.Join(tempDir, "restored-data")
	restoredDatabase := filepath.Join(restoredData, "db", "server.db")
	if _, err := backup.Restore(t.Context(), archive, options.Password, backup.RestoreOptions{
		ConfigDir: restoredConfig, DataDir: restoredData, DatabasePath: restoredDatabase, Limits: options.Limits,
	}); err != nil {
		t.Fatalf("backup.Restore() error = %v", err)
	}
	if restored, err := os.ReadFile(filepath.Join(restoredConfig, "server.yml")); err != nil || len(restored) == 0 {
		t.Fatalf("restored server.yml size = %d, error = %v", len(restored), err)
	}
	if _, err := os.Stat(restoredDatabase); err != nil {
		t.Fatalf("restored database is missing: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(configDir, "server.yml")); err != nil || len(contents) == 0 {
		t.Fatalf("source server.yml size = %d, error = %v", len(contents), err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "should-not-appear.txt"), []byte("outside backup roots"), 0600); err != nil {
		t.Fatal(err)
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

func TestResolveServerAddressPrecedence(t *testing.T) {
	for _, tc := range []struct {
		flagAddress, listenEnv, configAddress, want string
	}{
		{"127.0.0.1", "0.0.0.0", "::", "127.0.0.1"},
		{"", "0.0.0.0", "::", "0.0.0.0"},
		{"", "", "127.0.0.1", "127.0.0.1"},
		{"", "", "", "::"},
		{" ", " ", " ", "::"},
	} {
		if got := resolveServerAddress(tc.flagAddress, tc.listenEnv, tc.configAddress); got != tc.want {
			t.Errorf("resolveServerAddress(%q, %q, %q) = %q, want %q", tc.flagAddress, tc.listenEnv, tc.configAddress, got, tc.want)
		}
	}
}

func TestFallbackIPv4ListenerAddress(t *testing.T) {
	address, fallback, err := fallbackIPv4ListenerAddress("::")
	if err != nil {
		t.Fatalf("fallbackIPv4ListenerAddress() error = %v", err)
	}
	if fallback && address != "0.0.0.0" {
		t.Fatalf("fallback address = %q, want 0.0.0.0", address)
	}
	if !fallback && address != "::" {
		t.Fatalf("dual-stack address = %q, want ::", address)
	}
	address, fallback, err = fallbackIPv4ListenerAddress("127.0.0.1")
	if err != nil || fallback || address != "127.0.0.1" {
		t.Fatalf("explicit IPv4 address = (%q, %t, %v), want (127.0.0.1, false, nil)", address, fallback, err)
	}
}
