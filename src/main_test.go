package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/notifications"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/privilege"
	backgroundscheduler "github.com/apimgr/countries/src/scheduler"
	"github.com/apimgr/countries/src/testutil"
)

// printHelp writes usage text directly to stdout; capture it via an
// os.Pipe to confirm the help text is produced and contains the
// documented flags and command sections.
func TestPrintHelp(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"/tmp/renamed-server"}
	defer func() { os.Args = originalArgs }()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w

	printHelp()

	if err := w.Close(); err != nil {
		t.Fatalf("w.Close() error = %v", err)
	}
	os.Stdout = origStdout

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	got := string(out)

	wantSubstrings := []string{
		"Countries API Server",
		"Usage: renamed-server [options]",
		"--port PORT",
		"--address ADDRESS",
		"--config DIR",
		"--data DIR",
		"--log DIR",
		"--help",
		"Mode Commands:",
		"Service Commands:",
		"Maintenance Commands:",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("printHelp() output missing %q, got: %s", want, got)
		}
	}
}

func TestPrintVersionUsesExecutableNameAndBuildMetadata(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"/tmp/renamed-server"}
	defer func() { os.Args = originalArgs }()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer
	printVersion()
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	os.Stdout = originalStdout

	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	for _, expected := range []string{
		"renamed-server " + Version + " (" + CommitID + ")",
		"Built: " + BuildDate,
		"Go: " + runtime.Version(),
		"OS/Arch: " + runtime.GOOS + "/" + runtime.GOARCH,
	} {
		if !strings.Contains(string(output), expected) {
			t.Errorf("version output missing %q: %s", expected, output)
		}
	}
}

func TestRunServerAndGracefulShutdown(t *testing.T) {
	t.Setenv("SMTP_HOST", "127.0.0.1")
	t.Setenv("SMTP_PORT", "1")
	port := ""
	for candidate := 64000; candidate <= 64999; candidate++ {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(candidate)))
		if err == nil {
			port = strconv.Itoa(candidate)
			_ = listener.Close()
			break
		}
	}
	if port == "" {
		t.Fatal("no available port in AI.md's default range")
	}
	root := testutil.TempDir(t)
	configDir, dataDir, cacheDir, logsDir, backupDir := filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "cache"), filepath.Join(root, "logs"), filepath.Join(root, "backup")
	t.Setenv("DATABASE_DIR", filepath.Join(dataDir, "db"))
	cfg := config.DefaultConfig()
	cfg.Server.Port = port
	cfg.Server.Address = "127.0.0.1"
	cfg.Server.Notifications.Email.Enabled = false
	cfg.Server.I2P.Enabled = false
	cfg.Server.GeoIP.Enabled = false
	cfg.Server.Security.Blocklists.Enabled = false
	cfg.Data.Trivy.Enabled = false
	for name, task := range cfg.Server.Scheduler.Tasks {
		task.Enabled = false
		cfg.Server.Scheduler.Tasks[name] = task
	}
	configPath := filepath.Join(configDir, "server.yml")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	oldArgs, oldFlags := os.Args, flag.CommandLine
	oldEnsureAccount, oldDropPrivileges := ensureServiceAccount, dropServicePrivileges
	ensureServiceAccount = func(name, _ string) (privilege.Identity, error) {
		return privilege.Identity{Name: name, UID: os.Getuid(), GID: os.Getgid()}, nil
	}
	dropServicePrivileges = func(privilege.Identity) error { return nil }
	os.Args = []string{"countries", "--port", port, "--address", "127.0.0.1", "--config", configDir, "--data", dataDir, "--cache", cacheDir, "--log", logsDir, "--backup", backupDir}
	flag.CommandLine = flag.NewFlagSet("countries", flag.ExitOnError)
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
		ensureServiceAccount = oldEnsureAccount
		dropServicePrivileges = oldDropPrivileges
	})
	result := make(chan int, 1)
	guardSignals := make(chan os.Signal, 4)
	signal.Notify(guardSignals, os.Interrupt)
	t.Cleanup(func() { signal.Stop(guardSignals) })
	go func() { result <- run() }()
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	endpoint := "http://127.0.0.1:" + port + "/server/about"
	deadline := time.Now().Add(30 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		select {
		case code := <-result:
			t.Fatalf("run() exited before serving, code=%d", code)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		// run() installs its shutdown handler only after initialization. Retry
		// the signal until that handler is ready; guardSignals prevents an
		// early signal from terminating the go test process itself.
		shutdownDeadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(shutdownDeadline) {
			_ = process.Signal(os.Interrupt)
			select {
			case <-result:
				goto serverStopped
			case <-time.After(100 * time.Millisecond):
			}
		}
	serverStopped:
		t.Fatal("server did not become ready within 30 seconds")
	}
	if !paths.IsContainer() {
		pidPath := filepath.Join(dataDir, "countries.pid")
		pidData, err := os.ReadFile(pidPath)
		if err != nil || strings.TrimSpace(string(pidData)) != strconv.Itoa(os.Getpid()) {
			t.Fatalf("server PID file = %q, error = %v", pidData, err)
		}
	}
	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send graceful shutdown signal: %v", err)
	}
	select {
	case code := <-result:
		if code != 0 {
			t.Fatalf("run() exit code = %d, want 0", code)
		}
		if !paths.IsContainer() {
			if _, err := os.Stat(filepath.Join(dataDir, "countries.pid")); !os.IsNotExist(err) {
				t.Fatalf("server PID file remains after graceful shutdown: %v", err)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down gracefully")
	}
}

func TestUpdateCommandBranches(t *testing.T) {
	releaseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	}))
	defer releaseServer.Close()
	previousReleaseURL := updaterReleaseURL
	updaterReleaseURL = releaseServer.URL
	t.Cleanup(func() { updaterReleaseURL = previousReleaseURL })
	cases := []struct {
		name  string
		cmd   string
		args  []string
		check func(*testing.T, *config.Config, string)
	}{
		{name: "check", cmd: "check", check: func(t *testing.T, _ *config.Config, output string) {
			if !strings.Contains(output, "up to date") {
				t.Errorf("check output = %q", output)
			}
		}},
		{name: "install", cmd: "yes", check: func(t *testing.T, _ *config.Config, output string) {
			if !strings.Contains(output, "up to date") {
				t.Errorf("install output = %q", output)
			}
		}},
		{name: "show branch", cmd: "branch", check: func(t *testing.T, cfg *config.Config, output string) {
			if !strings.Contains(output, cfg.Server.Update.Branch) {
				t.Errorf("branch output = %q, want branch %q", output, cfg.Server.Update.Branch)
			}
		}},
		{name: "set branch", cmd: "branch", args: []string{"beta"}, check: func(t *testing.T, cfg *config.Config, output string) {
			if cfg.Server.Update.Branch != "beta" || !strings.Contains(output, "Branch set to: beta") {
				t.Errorf("branch config/output = %q / %q", cfg.Server.Update.Branch, output)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldFlags := flag.CommandLine
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			flag.CommandLine = fs
			t.Cleanup(func() { flag.CommandLine = oldFlags })
			cfg := config.DefaultConfig()
			configPath := filepath.Join(testutil.TempDir(t), "server.yml")
			if err := config.Save(configPath, cfg); err != nil {
				t.Fatal(err)
			}
			output := captureOutput(t, func() { _ = handleUpdateCommand(tc.cmd, cfg, configPath) })
			tc.check(t, cfg, output)
			if tc.name == "set branch" {
				loaded, err := config.Load(configPath)
				if err != nil || loaded.Server.Update.Branch != "beta" {
					t.Fatalf("saved branch = %q, err = %v", loaded.Server.Update.Branch, err)
				}
			}
		})
	}
}

func TestResolveUpdateInvocation(t *testing.T) {
	for _, test := range []struct {
		value string
		args  []string
		cmd   string
		want  []string
	}{
		{value: "true", cmd: "yes"},
		{value: "true", args: []string{"check"}, cmd: "check"},
		{value: "true", args: []string{"branch", "beta"}, cmd: "branch", want: []string{"beta"}},
		{value: "check", cmd: "check"},
		{value: "branch", args: []string{"devel"}, cmd: "branch", want: []string{"devel"}},
	} {
		gotCommand, gotArgs := resolveUpdateInvocation(test.value, test.args)
		if gotCommand != test.cmd || strings.Join(gotArgs, " ") != strings.Join(test.want, " ") {
			t.Errorf("resolveUpdateInvocation(%q, %v) = (%q, %v), want (%q, %v)", test.value, test.args, gotCommand, gotArgs, test.cmd, test.want)
		}
	}
}

func TestResolveRuntimeModePriority(t *testing.T) {
	for _, test := range []struct {
		cli, env, configured, want string
	}{
		{cli: "debug", env: "development", configured: "production", want: "debug"},
		{env: "development", configured: "production", want: "development"},
		{configured: "development", want: "development"},
		{want: ""},
	} {
		if got := resolveRuntimeMode(test.cli, test.env, test.configured); got != test.want {
			t.Errorf("resolveRuntimeMode(%q, %q, %q) = %q, want %q", test.cli, test.env, test.configured, got, test.want)
		}
	}
}

func TestDetectSMTPUsesBoundedProbeTimeout(t *testing.T) {
	var probes []notifications.SMTPConfig
	host, port, ok := detectSMTPWithCheck(config.SMTPConfig{TLS: "auto"}, "localhost", func(probe notifications.SMTPConfig) error {
		probes = append(probes, probe)
		if probe.Host == "127.0.0.1" && probe.Port == 587 {
			return nil
		}
		return errors.New("SMTP endpoint unavailable")
	})
	if !ok || host != "127.0.0.1" || port != 587 {
		t.Fatalf("detectSMTPWithCheck() = (%q, %d, %t), want (127.0.0.1, 587, true)", host, port, ok)
	}
	if len(probes) != 3 {
		t.Fatalf("SMTP probe count = %d, want 3", len(probes))
	}
	for index, probe := range probes {
		if probe.Timeout != smtpAutodetectTimeout {
			t.Errorf("SMTP probe %d timeout = %s, want %s", index, probe.Timeout, smtpAutodetectTimeout)
		}
	}
}

func TestUpdateSchedulerConfiguration(t *testing.T) {
	scheduler := backgroundscheduler.NewWithOptions(backgroundscheduler.Options{Timezone: "UTC", CatchUpWindow: time.Hour})
	if err := scheduler.Register(backgroundscheduler.Task{ID: "healthcheck_self", Name: "health check", Schedule: "@daily", Enabled: true, Func: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig().Server.Scheduler
	cfg.Timezone = "Europe/Paris"
	cfg.CatchUpWindow = "2h"
	cfg.Tasks["healthcheck_self"] = config.SchedulerTaskConfig{Enabled: false, Schedule: "@hourly", RetryOnFail: true, RetryDelay: "3m"}
	if err := updateSchedulerConfiguration(scheduler, cfg); err != nil {
		t.Fatalf("updateSchedulerConfiguration() error = %v", err)
	}
	got := scheduler.GetTasks()[0]
	if got.Enabled || got.Schedule != "@hourly" || !got.RetryOnFail || got.RetryDelay != 3*time.Minute {
		t.Fatalf("reloaded scheduler task = %+v", got)
	}
	if err := updateSchedulerConfiguration(scheduler, config.SchedulerConfig{Timezone: "invalid", CatchUpWindow: "2h"}); err == nil {
		t.Fatal("updateSchedulerConfiguration() accepted an invalid timezone")
	}
}
