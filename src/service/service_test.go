package service

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/testutil"
)

func TestConfirmUninstall(t *testing.T) {
	for _, test := range []struct {
		answer  string
		wantErr bool
	}{{"y\n", false}, {"YES\n", false}, {"\n", true}, {"n\n", true}} {
		var output bytes.Buffer
		err := confirmUninstall(strings.NewReader(test.answer), &output)
		if (err != nil) != test.wantErr {
			t.Errorf("confirmUninstall(%q) error = %v", test.answer, err)
		}
		if !strings.Contains(output.String(), "This will delete ALL data, configs, and the system user. Continue? [y/N]") {
			t.Error("confirmation prompt was not shown")
		}
	}
}

func TestReadCreatedIdentity(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "account")
	if err := os.WriteFile(marker, []byte("countries 234 234 true true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := readCreatedIdentity(marker)
	if err != nil || identity == nil || !identity.Created || !identity.GroupCreated || identity.UID != 234 {
		t.Fatalf("readCreatedIdentity() = %#v, %v", identity, err)
	}
	if err := os.WriteFile(marker, []byte("countries 1000 1001 true true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCreatedIdentity(marker); err == nil {
		t.Fatal("invalid marker was accepted")
	}
}

func TestRemoveServiceDataUsesResolvedDirectories(t *testing.T) {
	root := t.TempDir()
	var dirs UninstallPaths
	for _, name := range []string{"config", "data", "cache", "logs", "backup", "pid"} {
		path := filepath.Join(root, "countries", name)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if name == "config" {
			dirs.Config = path
		}
		if name == "data" {
			dirs.Data = path
		}
		if name == "cache" {
			dirs.Cache = path
		}
		if name == "logs" {
			dirs.Logs = path
		}
		if name == "backup" {
			dirs.Backup = path
		}
		if name == "pid" {
			dirs.PID = path
		}
	}
	if err := removeServiceData(dirs); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup, dirs.PID} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("service path %s still exists (err %v)", path, err)
		}
	}
}

func TestSystemdServiceContentMatchesPrivilegeStartupRules(t *testing.T) {
	content := systemdServiceContent("/usr/local/bin/countries")
	for _, forbidden := range []string{"User=root", "Group=root", "NoNewPrivileges=true", "ProtectSystem=strict"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("systemd unit contains forbidden setting %q", forbidden)
		}
	}
	for _, required := range []string{
		"ExecStart=/usr/local/bin/countries",
		"Documentation=https://apimgr.github.io/countries",
		"ProtectSystem=true",
		"ProtectHome=yes",
		"ReadWritePaths=-/var/cache/apimgr/countries",
		"ReadWritePaths=-/var/run/apimgr/countries",
		"ReadWritePaths=-/usr/local/bin",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("systemd unit is missing %q", required)
		}
	}
	if strings.Contains(content, "countries.apimgr.us") {
		t.Fatal("systemd unit contains an unconfigured official-site URL")
	}
}

func TestOpenRCUsesSeparateSupervisorPIDFile(t *testing.T) {
	content := openRCServiceContent("/usr/local/bin/countries")
	for _, required := range []string{
		"command=\"/usr/local/bin/countries\"",
		"pidfile=\"/var/run/apimgr-countries.pid\"",
		"command_background=true",
		"retry=\"TERM/60\"",
		"checkpath -d -m 0755 -o countries:countries /var/run/apimgr/countries",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("OpenRC service is missing %q", required)
		}
	}
	if strings.Contains(content, "pidfile=\"/var/run/apimgr/countries/countries.pid\"") {
		t.Fatal("OpenRC supervisor PID file shares the server PID path")
	}
}

func TestDetectLinuxServiceManagerOrder(t *testing.T) {
	info, err := os.Stat(testutil.TempDir(t))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		paths    map[string]bool
		commands map[string]bool
		want     ServiceType
	}{
		{name: "systemd precedes other markers", paths: map[string]bool{"/run/systemd/system": true, "/sbin/openrc-run": true, "/run/openrc": true}, commands: map[string]bool{"sv": true}, want: ServiceSystemd},
		{name: "OpenRC precedes SysV", paths: map[string]bool{"/sbin/openrc-run": true, "/run/openrc": true, "/etc/init.d": true}, commands: map[string]bool{"update-rc.d": true}, want: ServiceOpenRC},
		{name: "runit executable", commands: map[string]bool{"sv": true}, want: ServiceRunit},
		{name: "SysV with supported enable command", paths: map[string]bool{"/etc/init.d": true}, commands: map[string]bool{"chkconfig": true}, want: ServiceSysV},
		{name: "unsupported", want: ServiceUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stat := func(path string) (os.FileInfo, error) {
				if test.paths[path] {
					return info, nil
				}
				return nil, os.ErrNotExist
			}
			lookPath := func(command string) (string, error) {
				if test.commands[command] {
					return "/fake/" + command, nil
				}
				return "", os.ErrNotExist
			}
			got := detectLinuxServiceManager(stat, lookPath, os.ReadFile)
			if got != test.want {
				t.Errorf("detectLinuxServiceManager() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestS6ScanHelpersUseInjectedProcDirectory(t *testing.T) {
	procDir := testutil.TempDir(t)
	writeCmdline := func(pid, cmdline string) {
		t.Helper()
		pidDir := filepath.Join(procDir, pid)
		if err := os.MkdirAll(pidDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(cmdline), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeCmdline("not-a-pid", "s6-svscan\x00/tmp/ignored")
	writeCmdline("101", "/usr/bin/s6-svscan\x00/run/s6/services\x00")

	if !s6ScanRunningIn(procDir, os.ReadDir, os.ReadFile) {
		t.Fatal("s6ScanRunningIn() did not find the s6-svscan process")
	}
	if got, want := s6ScanDirIn(procDir, os.ReadDir, os.ReadFile), "/run/s6/services"; got != want {
		t.Errorf("s6ScanDirIn() = %q, want %q", got, want)
	}
}

func TestS6ScanHelpersHandleMissingProcDirectory(t *testing.T) {
	missing := filepath.Join(testutil.TempDir(t), "missing")
	if s6ScanRunningIn(missing, os.ReadDir, os.ReadFile) {
		t.Error("s6ScanRunningIn() = true for a missing proc directory")
	}
	if got, want := s6ScanDirIn(missing, os.ReadDir, os.ReadFile), "/run/service"; got != want {
		t.Errorf("s6ScanDirIn() = %q, want fallback %q", got, want)
	}
}

func TestLaunchdUsesDerivedStableLabel(t *testing.T) {
	if got, want := plistName(), "io.github.apimgr.countries"; got != want {
		t.Errorf("plistName() = %q, want %q", got, want)
	}
}

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
	logPath := setupServiceCommandStubs(t, "systemctl")
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

func TestServiceControlDispatchesToAvailableManagers(t *testing.T) {
	logPath := setupServiceCommandStubs(t,
		"systemctl", "rc-service", "rc-update", "sv", "s6-svc", "s6-svstat", "launchctl", "sc.exe", "service", "sysrc",
	)
	cases := []struct {
		name       string
		manager    ServiceType
		operations []func() error
		want       []string
	}{
		{
			name: "OpenRC", manager: ServiceOpenRC,
			operations: []func() error{Start, Stop, Restart, Reload, Status, Disable},
			want:       []string{"rc-service countries start", "rc-service countries stop", "rc-service countries restart", "rc-service countries restart", "rc-service countries status", "rc-service countries stop", "rc-update del countries default"},
		},
		{
			name: "runit", manager: ServiceRunit,
			operations: []func() error{Start, Stop, Restart, Reload, Status},
			want:       []string{"sv start countries", "sv stop countries", "sv restart countries", "sv hup countries", "sv status countries"},
		},
		{
			name: "s6", manager: ServiceS6,
			operations: []func() error{Start, Stop, Restart, Reload, Status},
			want:       []string{"s6-svc -u " + filepath.Join(s6ScanDir(), appName), "s6-svc -d " + filepath.Join(s6ScanDir(), appName), "s6-svc -r " + filepath.Join(s6ScanDir(), appName), "s6-svc -h " + filepath.Join(s6ScanDir(), appName), "s6-svstat " + filepath.Join(s6ScanDir(), appName)},
		},
		{
			name: "launchd", manager: ServiceLaunchd,
			operations: []func() error{Start, Stop, Restart, Reload, Status, Disable},
			want: []string{
				"launchctl load /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl unload /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl unload /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl load /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl unload /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl load /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl list io.github.apimgr.countries",
				"launchctl unload /Library/LaunchDaemons/io.github.apimgr.countries.plist",
				"launchctl disable system/io.github.apimgr.countries",
			},
		},
		{
			name: "Windows", manager: ServiceWindows,
			operations: []func() error{Start, Stop, Restart, Reload, Status, Disable},
			want:       []string{"sc.exe start countries", "sc.exe stop countries", "sc.exe stop countries", "sc.exe start countries", "sc.exe stop countries", "sc.exe start countries", "sc.exe query countries", "sc.exe config countries start= disabled"},
		},
		{
			name: "BSD rc.d", manager: ServiceBSDRC,
			operations: []func() error{Start, Stop, Restart, Reload, Status, Disable},
			want:       []string{"service countries start", "service countries stop", "service countries restart", "service countries restart", "service countries status", "service countries stop", "sysrc countries_enable=NO"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(logPath, nil, 0600); err != nil {
				t.Fatal(err)
			}
			oldDetect := detectServiceManager
			detectServiceManager = func() ServiceType { return test.manager }
			t.Cleanup(func() { detectServiceManager = oldDetect })
			for _, operation := range test.operations {
				if err := operation(); err != nil {
					t.Fatalf("service operation failed for %s: %v", test.name, err)
				}
			}
			calls, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(string(calls), want) {
					t.Errorf("service calls %q missing %q", calls, want)
				}
			}
		})
	}
}

func TestServiceControlReturnsCommandFailure(t *testing.T) {
	setupServiceCommandStubs(t, "systemctl")
	t.Setenv("TEST_SERVICE_COMMAND_FAIL", "systemctl")
	oldDetect := detectServiceManager
	detectServiceManager = func() ServiceType { return ServiceSystemd }
	t.Cleanup(func() { detectServiceManager = oldDetect })
	if err := Start(); err == nil {
		t.Fatal("Start() returned nil when systemctl failed")
	}
}

func TestCopyBinaryCreatesDestinationAndPreservesExecutableMode(t *testing.T) {
	root := testutil.TempDir(t)
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "nested", "bin", "countries")
	if err := os.WriteFile(source, []byte("binary fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyBinary(source, destination); err != nil {
		t.Fatalf("copyBinary() error = %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary fixture" {
		t.Errorf("copied bytes = %q, want source bytes", got)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("copied mode = %04o, want 0755", info.Mode().Perm())
	}
}

func TestCopyBinaryReportsSourceAndDestinationErrors(t *testing.T) {
	root := testutil.TempDir(t)
	if err := copyBinary(filepath.Join(root, "missing"), filepath.Join(root, "out", "binary")); err == nil {
		t.Fatal("copyBinary() accepted a missing source")
	}
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("binary fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	parentFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyBinary(source, filepath.Join(parentFile, "binary")); err == nil {
		t.Fatal("copyBinary() accepted a destination below a regular file")
	}
}

func TestCopyExecutableToInstallPathUsesCurrentBinaryAndReportsErrors(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := copyExecutableToInstallPath(executable); err != nil {
		t.Errorf("copyExecutableToInstallPath(current executable) error = %v", err)
	}
	destination := filepath.Join(testutil.TempDir(t), "bin", "countries")
	if err := copyExecutableToInstallPath(destination); err != nil {
		t.Fatalf("copyExecutableToInstallPath(temp destination) error = %v", err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Errorf("installed executable is missing: %v", err)
	}
	root := testutil.TempDir(t)
	parentFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutableToInstallPath(filepath.Join(parentFile, "binary")); err == nil {
		t.Fatal("copyExecutableToInstallPath() accepted a destination below a regular file")
	}
}

func setupServiceCommandStubs(t *testing.T, names ...string) string {
	t.Helper()
	binDir := filepath.Join(testutil.TempDir(t), "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(testutil.TempDir(t), "service-commands.log")
	script := `#!/bin/sh
printf '%s %s\n' "${0##*/}" "$*" >> "$TEST_SERVICE_COMMAND_LOG"
if [ "$TEST_SERVICE_COMMAND_FAIL" = "${0##*/}" ]; then
  exit 17
fi
`
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_SERVICE_COMMAND_LOG", logPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}
