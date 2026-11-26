// Package service installs and controls the application as an OS service.
package service

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/privilege"
	"io"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var detectServiceManager = DetectServiceManager

const (
	appName = "countries"
	orgName = "apimgr"
)

// UninstallPaths contains the resolved runtime directories that uninstall removes.
type UninstallPaths struct {
	Config string
	Data   string
	Cache  string
	Logs   string
	Backup string
	PID    string
}

// ServiceType represents the type of service manager
type ServiceType int

const (
	ServiceUnknown ServiceType = iota
	ServiceSystemd
	ServiceOpenRC
	ServiceRunit
	ServiceS6
	ServiceSysV
	ServiceLaunchd
	ServiceWindows
	ServiceBSDRC
)

// DetectServiceManager detects the system's service manager
func DetectServiceManager() ServiceType {
	switch runtime.GOOS {
	case "linux":
		return detectLinuxServiceManager(os.Stat, exec.LookPath, os.ReadFile)

	case "darwin":
		return ServiceLaunchd

	case "windows":
		return ServiceWindows

	case "freebsd", "openbsd", "netbsd":
		return ServiceBSDRC

	default:
		return ServiceUnknown
	}
}

func detectLinuxServiceManager(stat func(string) (os.FileInfo, error), lookPath func(string) (string, error), readFile func(string) ([]byte, error)) ServiceType {
	if _, err := stat("/run/systemd/system"); err == nil {
		return ServiceSystemd
	}
	if _, err := stat("/sbin/openrc-run"); err == nil {
		if _, err := stat("/run/openrc"); err == nil {
			return ServiceOpenRC
		}
	}
	if _, err := stat("/etc/runit"); err == nil {
		return ServiceRunit
	}
	if _, err := lookPath("sv"); err == nil {
		return ServiceRunit
	}
	if _, err := lookPath("s6-svc"); err == nil {
		if s6ScanRunning(readFile) {
			return ServiceS6
		}
	}
	if _, err := stat("/etc/init.d"); err == nil {
		for _, command := range []string{"update-rc.d", "chkconfig"} {
			if _, err := lookPath(command); err == nil {
				return ServiceSysV
			}
		}
	}
	return ServiceUnknown
}

func s6ScanRunning(readFile func(string) ([]byte, error)) bool {
	return s6ScanRunningIn("/proc", os.ReadDir, readFile)
}

func s6ScanRunningIn(procDir string, readDir func(string) ([]os.DirEntry, error), readFile func(string) ([]byte, error)) bool {
	entries, err := readDir(procDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, err := readFile(filepath.Join(procDir, entry.Name(), "cmdline"))
		if err == nil && strings.Contains(strings.ReplaceAll(string(data), "\x00", " "), "s6-svscan") {
			return true
		}
	}
	return false
}

func s6ScanDir() string {
	return s6ScanDirIn("/proc", os.ReadDir, os.ReadFile)
}

func s6ScanDirIn(procDir string, readDir func(string) ([]os.DirEntry, error), readFile func(string) ([]byte, error)) string {
	entries, err := readDir(procDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, err := strconv.Atoi(entry.Name()); err != nil {
				continue
			}
			data, err := readFile(filepath.Join(procDir, entry.Name(), "cmdline"))
			if err != nil {
				continue
			}
			args := strings.Split(string(data), "\x00")
			if len(args) > 1 && strings.Contains(args[0], "s6-svscan") && args[1] != "" {
				return args[1]
			}
		}
	}
	return "/run/service"
}

// Install installs the service for the detected service manager
func Install() error {
	serviceType := detectServiceManager()
	if !privilege.IsElevated() {
		switch runtime.GOOS {
		case "linux":
			if serviceType != ServiceSystemd {
				return errors.New("unprivileged service installation requires a systemd user manager")
			}
			return installUserSystemd()
		case "darwin":
			return installUserLaunchAgent()
		default:
			return errors.New("unprivileged service installation is unsupported on this platform")
		}
	}

	switch serviceType {
	case ServiceSystemd:
		return installSystemd()
	case ServiceOpenRC:
		return installOpenRC()
	case ServiceRunit:
		return installRunit()
	case ServiceS6:
		return installS6()
	case ServiceSysV:
		return installSysV()
	case ServiceLaunchd:
		return installLaunchd()
	case ServiceWindows:
		return installWindows()
	case ServiceBSDRC:
		return installBSDRC()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

func isUserServiceMode() bool {
	return !privilege.IsElevated() && (runtime.GOOS == "linux" || runtime.GOOS == "darwin")
}

func installUserSystemd() error {
	unitDir, err := userSystemdUnitDirectory()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve current executable path: %w", err)
	}
	if err := os.MkdirAll(unitDir, 0700); err != nil {
		return fmt.Errorf("create user systemd unit directory: %w", err)
	}
	unitPath := filepath.Join(unitDir, appName+".service")
	unit := fmt.Sprintf(`[Unit]
Description=Countries API service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, executable)
	if err := os.WriteFile(unitPath, []byte(unit), 0600); err != nil {
		return fmt.Errorf("write user systemd unit: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("reload user systemd manager: %w", err)
	}
	if err := exec.Command("systemctl", "--user", "enable", "--now", appName+".service").Run(); err != nil {
		return fmt.Errorf("enable and start user service: %w", err)
	}
	return nil
}

func userSystemdUnitDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("user home directory is empty")
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func userLaunchAgentIdentity() (string, string, error) {
	identity, err := osuser.Current()
	if err != nil {
		return "", "", fmt.Errorf("resolve current user: %w", err)
	}
	uid, err := strconv.Atoi(identity.Uid)
	if err != nil || uid < 0 {
		return "", "", errors.New("current user has an invalid UID")
	}
	return fmt.Sprintf("gui/%d", uid), "com." + orgName + "." + appName, nil
}

func userLaunchAgentPath() (string, string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", fmt.Errorf("resolve user home directory: %w", err)
	}
	domain, label, err := userLaunchAgentIdentity()
	if err != nil {
		return "", "", "", err
	}
	return domain, label, filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func uninstallUserLaunchAgent() error {
	domain, label, path, err := userLaunchAgentPath()
	if err != nil {
		return err
	}
	if err := exec.Command("launchctl", "bootout", domain, path).Run(); err != nil {
		return fmt.Errorf("stop user LaunchAgent: %w", err)
	}
	if err := exec.Command("launchctl", "disable", domain+"/"+label).Run(); err != nil {
		return fmt.Errorf("disable user LaunchAgent: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove user LaunchAgent: %w", err)
	}
	return nil
}

func installUserLaunchAgent() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve user home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return errors.New("user home directory is empty")
	}
	identity, err := osuser.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}
	uid, err := strconv.Atoi(identity.Uid)
	if err != nil || uid < 0 {
		return errors.New("current user has an invalid UID")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve current executable path: %w", err)
	}
	agentDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		return fmt.Errorf("create user LaunchAgents directory: %w", err)
	}
	label := "com." + orgName + "." + appName
	plistPath := filepath.Join(agentDir, label+".plist")
	var escapedExecutable strings.Builder
	if err := xml.EscapeText(&escapedExecutable, []byte(executable)); err != nil {
		return fmt.Errorf("encode executable path for launchd: %w", err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
</dict></plist>
`, label, escapedExecutable.String())
	if err := os.WriteFile(plistPath, []byte(plist), 0600); err != nil {
		return fmt.Errorf("write user LaunchAgent: %w", err)
	}
	domain := fmt.Sprintf("gui/%d", uid)
	if err := exec.Command("launchctl", "bootstrap", domain, plistPath).Run(); err != nil {
		return fmt.Errorf("bootstrap user LaunchAgent: %w", err)
	}
	if err := exec.Command("launchctl", "enable", domain+"/"+label).Run(); err != nil {
		return fmt.Errorf("enable user LaunchAgent: %w", err)
	}
	return exec.Command("launchctl", "kickstart", "-k", domain+"/"+label).Run()
}

// RecordServiceAccount stores root-owned ownership metadata for later uninstall.
func RecordServiceAccount(identity privilege.Identity, serviceName string) error {
	if !identity.Created && !identity.GroupCreated {
		return nil
	}
	rollback := func(cause error) error {
		if rollbackErr := privilege.RemoveSystemAccount(identity); rollbackErr != nil {
			return errors.Join(cause, fmt.Errorf("roll back service account creation: %w", rollbackErr))
		}
		return cause
	}
	marker := paths.GetServiceAccountMarkerPath(serviceName)
	if marker == "" {
		return rollback(errors.New("service account marker path is unavailable"))
	}
	markerDir := filepath.Dir(marker)
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		return rollback(fmt.Errorf("prepare service account marker directory: %w", err))
	}
	info, err := os.Lstat(markerDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return rollback(fmt.Errorf("service account marker directory is not a safe directory: %s", markerDir))
	}
	if err := os.Chown(markerDir, 0, 0); err != nil {
		return rollback(fmt.Errorf("secure service account marker directory: %w", err))
	}
	if err := os.Chmod(markerDir, 0755); err != nil {
		return rollback(fmt.Errorf("secure service account marker directory: %w", err))
	}
	contents := fmt.Sprintf("%s %d %d %t %t\n", identity.Name, identity.UID, identity.GID, identity.Created, identity.GroupCreated)
	if err := os.WriteFile(marker, []byte(contents), 0600); err != nil {
		return rollback(fmt.Errorf("write service account marker: %w", err))
	}
	return nil
}

// Uninstall removes the service
func Uninstall() error {
	configDir, dataDir, logsDir := paths.GetDefaultDirs(appName)
	return UninstallWithPaths(UninstallPaths{Config: configDir, Data: dataDir, Cache: paths.GetCacheDir(appName), Logs: logsDir, Backup: paths.GetBackupDir(appName), PID: PIDDirectory()})
}

// PIDDirectory returns the service PID directory when the platform uses one.
func PIDDirectory() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return filepath.Join("/var/run", orgName, appName)
}

// UninstallWithPaths uninstalls the service and removes the resolved runtime directories.
func UninstallWithPaths(dirs UninstallPaths) error {
	if err := confirmUninstall(os.Stdin, os.Stdout); err != nil {
		return err
	}
	serviceType := detectServiceManager()
	var err error
	if isUserServiceMode() {
		switch runtime.GOOS {
		case "linux":
			err = uninstallUserSystemd()
		case "darwin":
			err = uninstallUserLaunchAgent()
		}
		if err != nil {
			return err
		}
		if err := removeServiceData(dirs); err != nil {
			return err
		}
		fmt.Println("Service uninstalled. Delete binary manually if no longer needed.")
		return nil
	}

	switch serviceType {
	case ServiceSystemd:
		err = uninstallSystemd()
	case ServiceOpenRC:
		err = uninstallOpenRC()
	case ServiceRunit:
		err = uninstallRunit()
	case ServiceS6:
		err = uninstallS6()
	case ServiceSysV:
		err = uninstallSysV()
	case ServiceLaunchd:
		err = uninstallLaunchd()
	case ServiceWindows:
		err = uninstallWindows()
	case ServiceBSDRC:
		err = uninstallBSDRC()
	default:
		return fmt.Errorf("unsupported service manager")
	}
	if err != nil {
		return err
	}
	if err := removeServiceData(dirs); err != nil {
		return err
	}
	fmt.Printf("Service uninstalled. Delete binary manually: rm %s\n", GetBinaryPath())
	return nil
}

func uninstallUserSystemd() error {
	if err := exec.Command("systemctl", "--user", "disable", "--now", appName+".service").Run(); err != nil {
		return fmt.Errorf("disable user service: %w", err)
	}
	unitDir, err := userSystemdUnitDirectory()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(unitDir, appName+".service")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove user systemd unit: %w", err)
	}
	return exec.Command("systemctl", "--user", "daemon-reload").Run()
}

func confirmUninstall(input io.Reader, output io.Writer) error {
	if _, err := fmt.Fprintln(output, "This will delete ALL data, configs, and the system user. Continue? [y/N]"); err != nil {
		return err
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.ToLower(strings.TrimSpace(answer)) != "y" && strings.ToLower(strings.TrimSpace(answer)) != "yes" {
		return errors.New("service uninstall canceled")
	}
	return nil
}

func removeServiceData(dirs UninstallPaths) error {
	markerPath := paths.GetServiceAccountMarkerPath(appName)
	var identity *privilege.Identity
	if markerPath != "" {
		var markerErr error
		identity, markerErr = readCreatedIdentity(markerPath)
		if markerErr != nil {
			return markerErr
		}
	}
	cleanup := []string{dirs.Config, dirs.Data, dirs.Cache, dirs.Logs, dirs.Backup}
	if dirs.PID != "" {
		cleanup = append(cleanup, dirs.PID)
	}
	for _, path := range cleanup {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("refusing to remove non-absolute service path %q", path)
		}
		clean := filepath.Clean(path)
		if isUnsafeCleanupRoot(clean) {
			return fmt.Errorf("refusing to remove unsafe service path %q", path)
		}
	}
	sort.Slice(cleanup, func(i, j int) bool { return len(cleanup[i]) > len(cleanup[j]) })
	for _, path := range cleanup {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove service data %s: %w", path, err)
		}
	}
	if identity != nil {
		if err := privilege.RemoveSystemAccount(*identity); err != nil {
			return err
		}
		if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove service account marker: %w", err)
		}
	}
	return nil
}

func isUnsafeCleanupRoot(path string) bool {
	if path == string(filepath.Separator) {
		return true
	}
	for _, root := range []string{"/var", "/var/lib", "/var/log", "/var/cache", "/etc", "/usr", "/home", "/root", "/tmp", "/opt", "/mnt", "/data", "/config", `C:\\`, `C:\\ProgramData`, `C:\\Program Files`} {
		if strings.EqualFold(filepath.Clean(path), filepath.Clean(root)) {
			return true
		}
	}
	return false
}

func readCreatedIdentity(path string) (*privilege.Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read service account marker: %w", err)
	}
	values := strings.Fields(string(data))
	if len(values) != 5 || values[0] != appName {
		return nil, errors.New("invalid service account marker; refusing uninstall")
	}
	uid, uidErr := strconv.Atoi(values[1])
	gid, gidErr := strconv.Atoi(values[2])
	userCreated, userErr := config.ParseBool(values[3], false)
	groupCreated, groupErr := config.ParseBool(values[4], false)
	if uidErr != nil || gidErr != nil || userErr != nil || groupErr != nil || (!userCreated && !groupCreated) || uid < 200 || uid != gid {
		return nil, errors.New("invalid service account marker; refusing uninstall")
	}
	return &privilege.Identity{Name: values[0], UID: uid, GID: gid, Created: userCreated, GroupCreated: groupCreated}, nil
}

// GetBinaryPath returns the path where the binary should be installed
func GetBinaryPath() string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf(`C:\Program Files\%s\%s\%s.exe`, orgName, appName, appName)
	default:
		return fmt.Sprintf("/usr/local/bin/%s", appName)
	}
}

// installSystemd creates systemd service file
func installSystemd() error {
	binaryPath := GetBinaryPath()

	serviceContent := systemdServiceContent(binaryPath)
	servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", appName)

	// Create directories
	dirs := []string{
		fmt.Sprintf("/var/lib/%s/%s", orgName, appName),
		fmt.Sprintf("/var/cache/%s/%s", orgName, appName),
		fmt.Sprintf("/var/log/%s/%s", orgName, appName),
		fmt.Sprintf("/var/run/%s/%s", orgName, appName),
		fmt.Sprintf("/etc/%s/%s", orgName, appName),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write service file
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	// Copy binary if not already in place
	if exePath, err := os.Executable(); err == nil && exePath != binaryPath {
		if err := copyBinary(exePath, binaryPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
	}

	// Reload systemd
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	// Enable service
	if err := exec.Command("systemctl", "enable", appName).Run(); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}
	if err := exec.Command("systemctl", "start", appName).Run(); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	fmt.Printf("✅ Service installed at: %s\n", servicePath)
	fmt.Printf("✅ Binary installed at: %s\n", binaryPath)
	fmt.Println()
	fmt.Println("To start the service:")
	fmt.Printf("  sudo systemctl start %s\n", appName)
	fmt.Println()
	fmt.Println("To check status:")
	fmt.Printf("  sudo systemctl status %s\n", appName)

	return nil
}

func systemdServiceContent(binaryPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Countries API Server
Documentation=https://apimgr.github.io/countries
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
LimitNOFILE=65535

ProtectSystem=true
ProtectHome=yes
PrivateTmp=true
ExecStartPre=+/usr/bin/install -d -m 0755 /etc/%[2]s/%[3]s /var/lib/%[2]s/%[3]s /var/cache/%[2]s/%[3]s /var/log/%[2]s/%[3]s /var/run/%[2]s/%[3]s
ReadWritePaths=-/etc/%[2]s/%[3]s
ReadWritePaths=-/var/lib/%[2]s/%[3]s
ReadWritePaths=-/var/cache/%[2]s/%[3]s
ReadWritePaths=-/var/log/%[2]s/%[3]s
ReadWritePaths=-/var/run/%[2]s/%[3]s
ReadWritePaths=-/mnt/Backups/%[2]s/%[3]s
ReadWritePaths=-/usr/local/bin

[Install]
WantedBy=multi-user.target
`, binaryPath, orgName, appName)
}

// uninstallSystemd removes systemd service
func uninstallSystemd() error {
	servicePath := fmt.Sprintf("/etc/systemd/system/%s.service", appName)

	// Stop service if running
	_ = exec.Command("systemctl", "stop", appName).Run()

	// Disable service
	_ = exec.Command("systemctl", "disable", appName).Run()

	// Remove service file
	if err := os.Remove(servicePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove service file: %w", err)
	}

	// Reload systemd
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}

	fmt.Printf("✅ Service uninstalled: %s\n", servicePath)
	return nil
}

// installRunit creates runit service
func installRunit() error {
	svDir := fmt.Sprintf("/etc/sv/%s", appName)
	binaryPath := GetBinaryPath()
	if err := copyExecutableToInstallPath(binaryPath); err != nil {
		return err
	}

	// Create service directory
	if err := os.MkdirAll(svDir, 0755); err != nil {
		return fmt.Errorf("failed to create service directory: %w", err)
	}

	runScript := fmt.Sprintf(`#!/bin/sh
exec %s 2>&1
`, binaryPath)

	runPath := filepath.Join(svDir, "run")
	if err := os.WriteFile(runPath, []byte(runScript), 0755); err != nil {
		return fmt.Errorf("failed to write run script: %w", err)
	}

	// Create log directory
	logDir := filepath.Join(svDir, "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logRunScript := fmt.Sprintf(`#!/bin/sh
exec svlogd -tt /var/log/%s/%s
`, orgName, appName)
	logRunPath := filepath.Join(logDir, "run")
	if err := os.WriteFile(logRunPath, []byte(logRunScript), 0755); err != nil {
		return fmt.Errorf("failed to write log run script: %w", err)
	}

	// Link to service directory
	linkPath := fmt.Sprintf("/var/service/%s", appName)
	if err := os.Symlink(svDir, linkPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to link runit service: %w", err)
	}

	fmt.Printf("✅ Runit service installed at: %s\n", svDir)
	return nil
}

func installOpenRC() error {
	identity, err := privilege.EnsureSystemAccount(appName, paths.GetDataDir(appName))
	if err != nil {
		return fmt.Errorf("prepare OpenRC service account: %w", err)
	}
	if err := RecordServiceAccount(identity, appName); err != nil {
		return fmt.Errorf("record OpenRC service account: %w", err)
	}
	path := filepath.Join("/etc/init.d", appName)
	if err := copyExecutableToInstallPath(GetBinaryPath()); err != nil {
		return err
	}
	content := openRCServiceContent(GetBinaryPath())
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write OpenRC service: %w", err)
	}
	if err := exec.Command("rc-update", "add", appName, "default").Run(); err != nil {
		return fmt.Errorf("enable OpenRC service: %w", err)
	}
	return exec.Command("rc-service", appName, "start").Run()
}

func openRCServiceContent(binaryPath string) string {
	return fmt.Sprintf(`#!/sbin/openrc-run
name="%s"
description="Countries API Server"
command="%s"
command_args=""
command_background=true
retry="TERM/60"
# OpenRC supervisor PID file; do not share the server's own PID path.
pidfile="/var/run/%s-%s.pid"
output_log="/var/log/%s/%s/server.log"
error_log="/var/log/%s/%s/error.log"

depend() {
    need net
    after firewall
    use dns logger
}

start_pre() {
    checkpath -d -m 0755 /var/run/%s
    checkpath -d -m 0755 /var/log/%s
    checkpath -d -m 0755 -o %s:%s /var/run/%s/%s
    checkpath -d -m 0755 -o %s:%s /var/log/%s/%s
}
`, appName, binaryPath, orgName, appName, orgName, appName, orgName, appName,
		orgName, orgName, appName, appName, orgName, appName, appName, appName, orgName, appName)
}

func uninstallOpenRC() error {
	_ = exec.Command("rc-service", appName, "stop").Run()
	_ = exec.Command("rc-update", "del", appName, "default").Run()
	if err := os.Remove(filepath.Join("/etc/init.d", appName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove OpenRC service: %w", err)
	}
	return nil
}

func installSysV() error {
	path := filepath.Join("/etc/init.d", appName)
	if err := copyExecutableToInstallPath(GetBinaryPath()); err != nil {
		return err
	}
	content := fmt.Sprintf(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          %s
# Required-Start:    $network $remote_fs $syslog
# Required-Stop:     $network $remote_fs $syslog
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Countries API Server
# Description:       Countries API Server daemon
### END INIT INFO

NAME=%s
DAEMON=%s
DAEMON_USER=%s
PIDFILE=/var/run/%s/%s/%s.pid
LOGFILE=/var/log/%s/%s/server.log

case "$1" in
    start)
        mkdir -p /var/run/%s/%s /var/log/%s/%s
        chown -R "$DAEMON_USER:$DAEMON_USER" /var/run/%s/%s /var/log/%s/%s
        start-stop-daemon --start --quiet --background --make-pidfile --pidfile "$PIDFILE" --exec "$DAEMON" --no-close >> "$LOGFILE" 2>&1
        ;;
    stop)
        start-stop-daemon --stop --quiet --pidfile "$PIDFILE" --retry 30
        rm -f "$PIDFILE"
        ;;
    restart) "$0" stop; sleep 1; "$0" start ;;
    status) start-stop-daemon --status --pidfile "$PIDFILE" --exec "$DAEMON" ;;
    *) echo "Usage: $0 {start|stop|restart|status}"; exit 1 ;;
esac
`, appName, appName, GetBinaryPath(), appName, orgName, appName, appName, orgName, appName,
		orgName, appName, orgName, appName, orgName, appName, orgName, appName)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write SysV service: %w", err)
	}
	if _, err := exec.LookPath("update-rc.d"); err == nil {
		if err := exec.Command("update-rc.d", appName, "defaults").Run(); err != nil {
			return fmt.Errorf("enable SysV service with update-rc.d: %w", err)
		}
	} else if err := exec.Command("chkconfig", "--add", appName).Run(); err != nil {
		return fmt.Errorf("enable SysV service with chkconfig: %w", err)
	}
	return exec.Command(path, "start").Run()
}

func uninstallSysV() error {
	path := filepath.Join("/etc/init.d", appName)
	_ = exec.Command(path, "stop").Run()
	if _, err := exec.LookPath("update-rc.d"); err == nil {
		_ = exec.Command("update-rc.d", "-f", appName, "remove").Run()
	} else {
		_ = exec.Command("chkconfig", "--del", appName).Run()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove SysV service: %w", err)
	}
	return nil
}

func installS6() error {
	svDir := filepath.Join("/etc/s6/sv", appName)
	if err := copyExecutableToInstallPath(GetBinaryPath()); err != nil {
		return err
	}
	logDir := filepath.Join(svDir, "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("create s6 service directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(svDir, "run"), []byte(fmt.Sprintf("#!/bin/sh\nexec %s 2>&1\n", GetBinaryPath())), 0755); err != nil {
		return fmt.Errorf("write s6 run script: %w", err)
	}
	logScript := fmt.Sprintf("#!/bin/sh\nexec s6-log n10 s1000000 /var/log/%s/%s\n", orgName, appName)
	if err := os.WriteFile(filepath.Join(logDir, "run"), []byte(logScript), 0755); err != nil {
		return fmt.Errorf("write s6 log script: %w", err)
	}
	scanDir := s6ScanDir()
	link := filepath.Join(scanDir, appName)
	if err := os.Symlink(svDir, link); err != nil && !os.IsExist(err) {
		return fmt.Errorf("enable s6 service: %w", err)
	}
	return exec.Command("s6-svscanctl", "-a", scanDir).Run()
}

func uninstallS6() error {
	scanDir := s6ScanDir()
	serviceDir := filepath.Join(scanDir, appName)
	_ = exec.Command("s6-svc", "-d", serviceDir).Run()
	if err := os.Remove(serviceDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("disable s6 service: %w", err)
	}
	if err := exec.Command("s6-svscanctl", "-a", scanDir).Run(); err != nil {
		return fmt.Errorf("refresh s6 scan directory: %w", err)
	}
	if err := os.RemoveAll(filepath.Join("/etc/s6/sv", appName)); err != nil {
		return fmt.Errorf("remove s6 service definition: %w", err)
	}
	return nil
}

// uninstallRunit removes runit service
func uninstallRunit() error {
	svDir := fmt.Sprintf("/etc/sv/%s", appName)
	linkPath := fmt.Sprintf("/var/service/%s", appName)

	// Stop service
	_ = exec.Command("sv", "stop", appName).Run()

	// Remove link
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove runit service link: %w", err)
	}

	// Remove service directory
	if err := os.RemoveAll(svDir); err != nil {
		return fmt.Errorf("failed to remove runit service directory: %w", err)
	}

	fmt.Printf("✅ Runit service uninstalled\n")
	return nil
}

// installLaunchd creates macOS launchd plist
func installLaunchd() error {
	binaryPath := GetBinaryPath()
	plistPath := "/Library/LaunchDaemons/" + plistName() + ".plist"

	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/Library/Logs/%s/%s/error.log</string>
    <key>StandardOutPath</key>
    <string>/Library/Logs/%s/%s/output.log</string>
</dict>
</plist>
`, plistName(), binaryPath, orgName, appName, orgName, appName)

	// Create directories
	dirs := []string{
		fmt.Sprintf("/Library/Application Support/%s/%s", orgName, appName),
		fmt.Sprintf("/Library/Logs/%s/%s", orgName, appName),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write plist file
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	// Copy binary
	if exePath, err := os.Executable(); err == nil && exePath != binaryPath {
		if err := copyBinary(exePath, binaryPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
	}

	fmt.Printf("✅ LaunchDaemon installed at: %s\n", plistPath)
	fmt.Println()
	fmt.Println("To load the service:")
	fmt.Printf("  sudo launchctl load %s\n", plistPath)
	return exec.Command("launchctl", "load", plistPath).Run()
}

// uninstallLaunchd removes macOS launchd plist
func uninstallLaunchd() error {
	plistPath := "/Library/LaunchDaemons/" + plistName() + ".plist"

	// Unload if running
	_ = exec.Command("launchctl", "unload", plistPath).Run()

	// Remove plist
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove plist file: %w", err)
	}

	fmt.Printf("✅ LaunchDaemon uninstalled\n")
	return nil
}

// installWindows creates Windows service
func installWindows() error {
	binaryPath := GetBinaryPath()

	// Copy binary
	binDir := filepath.Dir(binaryPath)
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	if exePath, err := os.Executable(); err == nil && exePath != binaryPath {
		if err := copyBinary(exePath, binaryPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
	}

	// Create service using sc.exe
	displayName := "Countries API"
	cmd := exec.Command("sc.exe", "create", appName,
		"binPath=", windowsServiceBinaryPath(binaryPath),
		"DisplayName=", displayName,
		"obj=", `NT SERVICE\`+appName,
		"password=", "",
		"start=", "auto")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create Windows service: %w", err)
	}
	rollback := func(cause error) error {
		if removeErr := exec.Command("sc.exe", "delete", appName).Run(); removeErr != nil {
			return errors.Join(cause, fmt.Errorf("remove partially installed Windows service: %w", removeErr))
		}
		return cause
	}
	configDir, dataDir, logsDir := paths.GetDefaultDirs(appName)
	serviceDirs := []string{configDir, filepath.Join(configDir, "yaml.d"), filepath.Join(configDir, "ssl"), filepath.Join(configDir, "template"), dataDir, filepath.Join(dataDir, "db"), paths.GetCacheDir(appName), logsDir, paths.GetBackupDir(appName)}
	for _, dir := range serviceDirs {
		if strings.TrimSpace(dir) == "" {
			return rollback(errors.New("windows service runtime directory is empty"))
		}
		if err := os.MkdirAll(dir, 0750); err != nil {
			return rollback(fmt.Errorf("create Windows service directory %s: %w", dir, err))
		}
		grant := `NT SERVICE\` + appName + ":(OI)(CI)F"
		if err := exec.Command("icacls.exe", dir, "/grant", grant, "/T").Run(); err != nil {
			return rollback(fmt.Errorf("grant Windows service account access to %s: %w", dir, err))
		}
	}
	if err := exec.Command("sc.exe", "description", appName, "Countries API service").Run(); err != nil {
		return rollback(fmt.Errorf("set Windows service description: %w", err))
	}
	if err := exec.Command("sc.exe", "start", appName).Run(); err != nil {
		return rollback(fmt.Errorf("start Windows service: %w", err))
	}

	fmt.Printf("✅ Windows service '%s' installed\n", appName)
	fmt.Println()
	fmt.Println("To start the service:")
	fmt.Printf("  sc.exe start %s\n", appName)

	return nil
}

func windowsServiceBinaryPath(binaryPath string) string {
	return `"` + binaryPath + `"`
}

// uninstallWindows removes Windows service
func uninstallWindows() error {
	if err := stopWindowsServiceIfRunning(); err != nil {
		return fmt.Errorf("stop Windows service before uninstall: %w", err)
	}

	// Delete service
	if err := exec.Command("sc.exe", "delete", appName).Run(); err != nil {
		return fmt.Errorf("failed to delete Windows service: %w", err)
	}

	fmt.Printf("✅ Windows service '%s' uninstalled\n", appName)
	return nil
}

func stopWindowsServiceIfRunning() error {
	output, err := exec.Command("sc.exe", "query", appName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("query Windows service state: %w", err)
	}
	fields := strings.Fields(string(output))
	for i := 0; i+2 < len(fields); i++ {
		if fields[i] != "STATE" || fields[i+1] != ":" {
			continue
		}
		state, parseErr := strconv.Atoi(fields[i+2])
		if parseErr != nil {
			return fmt.Errorf("parse Windows service state: %w", parseErr)
		}
		switch state {
		case 1:
			return nil
		case 4:
			if err := exec.Command("sc.exe", "stop", appName).Run(); err != nil {
				return fmt.Errorf("stop running Windows service: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("windows service is in transition state %d", state)
		}
	}
	return errors.New("windows service query did not include a state")
}

// installBSDRC creates BSD rc.d script
func installBSDRC() error {
	binaryPath := GetBinaryPath()
	rcPath := fmt.Sprintf("/usr/local/etc/rc.d/%s", appName)

	rcContent := fmt.Sprintf(`#!/bin/sh
# PROVIDE: %s
# REQUIRE: NETWORKING
# KEYWORD: shutdown

. /etc/rc.subr

name="%s"
rcvar="%s_enable"
pidfile="/var/run/%s/%s/%s.pid"
command="/usr/sbin/daemon"
command_args="-P ${pidfile} -r -f %s"
start_precmd="%s_prestart"

%s_prestart()
{
    install -d -m 0755 /var/run/%s/%s /var/log/%s/%s
}

load_rc_config $name
run_rc_command "$1"
`, appName, appName, appName, orgName, appName, appName, binaryPath, appName,
		appName, orgName, appName, orgName, appName)

	if err := os.WriteFile(rcPath, []byte(rcContent), 0755); err != nil {
		return fmt.Errorf("failed to write rc.d script: %w", err)
	}

	// Copy binary
	if exePath, err := os.Executable(); err == nil && exePath != binaryPath {
		if err := copyBinary(exePath, binaryPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
	}

	fmt.Printf("✅ BSD rc.d script installed at: %s\n", rcPath)
	if err := exec.Command("sysrc", appName+"_enable=YES").Run(); err != nil {
		return fmt.Errorf("enable BSD service: %w", err)
	}
	return exec.Command("service", appName, "start").Run()
}

// uninstallBSDRC removes BSD rc.d script
func uninstallBSDRC() error {
	rcPath := fmt.Sprintf("/usr/local/etc/rc.d/%s", appName)

	// Stop service
	_ = exec.Command("service", appName, "stop").Run()
	_ = exec.Command("sysrc", appName+"_enable=NO").Run()

	// Remove script
	if err := os.Remove(rcPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove rc.d script: %w", err)
	}

	fmt.Printf("✅ BSD rc.d script uninstalled\n")
	return nil
}

// copyBinary copies the binary to the destination
func copyBinary(src, dst string) error {
	// Create destination directory if needed
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Read source
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// Write to destination
	if err := os.WriteFile(dst, data, 0755); err != nil {
		return err
	}

	return nil
}

func copyExecutableToInstallPath(destination string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	if filepath.Clean(executable) == filepath.Clean(destination) {
		return nil
	}
	if err := copyBinary(executable, destination); err != nil {
		return fmt.Errorf("install executable at %s: %w", destination, err)
	}
	return nil
}

// Start starts the service
func Start() error {
	serviceType := detectServiceManager()
	if isUserServiceMode() {
		if runtime.GOOS == "linux" {
			return exec.Command("systemctl", "--user", "start", appName+".service").Run()
		}
		domain, label, _, err := userLaunchAgentPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "kickstart", domain+"/"+label).Run()
	}

	switch serviceType {
	case ServiceSystemd:
		return exec.Command("systemctl", "start", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "start").Run()
	case ServiceRunit:
		return exec.Command("sv", "start", appName).Run()
	case ServiceS6:
		return exec.Command("s6-svc", "-u", filepath.Join(s6ScanDir(), appName)).Run()
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "start").Run()
	case ServiceLaunchd:
		plistPath := "/Library/LaunchDaemons/" + plistName() + ".plist"
		return exec.Command("launchctl", "load", plistPath).Run()
	case ServiceWindows:
		return exec.Command("sc.exe", "start", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "start").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Stop stops the service
func Stop() error {
	serviceType := detectServiceManager()
	if isUserServiceMode() {
		if runtime.GOOS == "linux" {
			return exec.Command("systemctl", "--user", "stop", appName+".service").Run()
		}
		domain, _, path, err := userLaunchAgentPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "bootout", domain, path).Run()
	}

	switch serviceType {
	case ServiceSystemd:
		return exec.Command("systemctl", "stop", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "stop").Run()
	case ServiceRunit:
		return exec.Command("sv", "stop", appName).Run()
	case ServiceS6:
		return exec.Command("s6-svc", "-d", filepath.Join(s6ScanDir(), appName)).Run()
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "stop").Run()
	case ServiceLaunchd:
		plistPath := "/Library/LaunchDaemons/" + plistName() + ".plist"
		return exec.Command("launchctl", "unload", plistPath).Run()
	case ServiceWindows:
		return exec.Command("sc.exe", "stop", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "stop").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Restart restarts the service
func Restart() error {
	serviceType := detectServiceManager()
	if isUserServiceMode() {
		if runtime.GOOS == "linux" {
			return exec.Command("systemctl", "--user", "restart", appName+".service").Run()
		}
		domain, label, _, err := userLaunchAgentPath()
		if err != nil {
			return err
		}
		return exec.Command("launchctl", "kickstart", "-k", domain+"/"+label).Run()
	}

	switch serviceType {
	case ServiceSystemd:
		return exec.Command("systemctl", "restart", appName).Run()
	case ServiceOpenRC:
		return exec.Command("rc-service", appName, "restart").Run()
	case ServiceRunit:
		return exec.Command("sv", "restart", appName).Run()
	case ServiceS6:
		return exec.Command("s6-svc", "-r", filepath.Join(s6ScanDir(), appName)).Run()
	case ServiceSysV:
		return exec.Command(filepath.Join("/etc/init.d", appName), "restart").Run()
	case ServiceLaunchd:
		if err := Stop(); err != nil {
			return err
		}
		return Start()
	case ServiceWindows:
		if err := stopWindowsServiceIfRunning(); err != nil {
			return fmt.Errorf("stop Windows service before restart: %w", err)
		}
		return exec.Command("sc.exe", "start", appName).Run()
	case ServiceBSDRC:
		return exec.Command("service", appName, "restart").Run()
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Reload sends reload signal to the service
func Reload() error {
	serviceType := detectServiceManager()
	if isUserServiceMode() {
		return Restart()
	}

	switch serviceType {
	case ServiceSystemd:
		return exec.Command("systemctl", "reload", appName).Run()
	case ServiceRunit:
		return exec.Command("sv", "hup", appName).Run()
	case ServiceS6:
		return exec.Command("s6-svc", "-h", filepath.Join(s6ScanDir(), appName)).Run()
	default:
		// For others, restart is the fallback
		return Restart()
	}
}

// Status reports service state through the detected platform manager.
func Status() error {
	if isUserServiceMode() {
		if runtime.GOOS == "linux" {
			return runServiceCommand("systemctl", "--user", "status", appName+".service")
		}
		domain, label, _, err := userLaunchAgentPath()
		if err != nil {
			return err
		}
		return runServiceCommand("launchctl", "print", domain+"/"+label)
	}
	switch detectServiceManager() {
	case ServiceSystemd:
		return runServiceCommand("systemctl", "status", appName)
	case ServiceOpenRC:
		return runServiceCommand("rc-service", appName, "status")
	case ServiceRunit:
		return runServiceCommand("sv", "status", appName)
	case ServiceS6:
		return runServiceCommand("s6-svstat", filepath.Join(s6ScanDir(), appName))
	case ServiceSysV:
		return runServiceCommand(filepath.Join("/etc/init.d", appName), "status")
	case ServiceLaunchd:
		return runServiceCommand("launchctl", "list", plistName())
	case ServiceWindows:
		return runServiceCommand("sc.exe", "query", appName)
	case ServiceBSDRC:
		return runServiceCommand("service", appName, "status")
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

// Disable prevents the service manager from starting the application automatically.
func Disable() error {
	if isUserServiceMode() {
		if runtime.GOOS == "linux" {
			return runServiceCommand("systemctl", "--user", "disable", "--now", appName+".service")
		}
		domain, label, path, err := userLaunchAgentPath()
		if err != nil {
			return err
		}
		if err := runServiceCommand("launchctl", "bootout", domain, path); err != nil {
			return err
		}
		return runServiceCommand("launchctl", "disable", domain+"/"+label)
	}
	switch detectServiceManager() {
	case ServiceSystemd:
		if err := runServiceCommand("systemctl", "stop", appName); err != nil {
			return err
		}
		return runServiceCommand("systemctl", "disable", appName)
	case ServiceOpenRC:
		if err := runServiceCommand("rc-service", appName, "stop"); err != nil {
			return err
		}
		return runServiceCommand("rc-update", "del", appName, "default")
	case ServiceRunit:
		_ = exec.Command("sv", "stop", appName).Run()
		linkPath := filepath.Join("/var/service", appName)
		if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove runit service link: %w", err)
		}
		return nil
	case ServiceS6:
		scanDir := s6ScanDir()
		linkPath := filepath.Join(scanDir, appName)
		_ = exec.Command("s6-svc", "-d", linkPath).Run()
		if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove s6 service link: %w", err)
		}
		return exec.Command("s6-svscanctl", "-a", scanDir).Run()
	case ServiceSysV:
		if err := exec.Command(filepath.Join("/etc/init.d", appName), "stop").Run(); err != nil {
			return err
		}
		if _, err := exec.LookPath("update-rc.d"); err == nil {
			return exec.Command("update-rc.d", "-f", appName, "remove").Run()
		}
		return exec.Command("chkconfig", appName, "off").Run()
	case ServiceLaunchd:
		if err := runServiceCommand("launchctl", "unload", "/Library/LaunchDaemons/"+plistName()+".plist"); err != nil {
			return err
		}
		return runServiceCommand("launchctl", "disable", "system/"+plistName())
	case ServiceWindows:
		return runServiceCommand("sc.exe", "config", appName, "start=", "disabled")
	case ServiceBSDRC:
		if err := runServiceCommand("service", appName, "stop"); err != nil {
			return err
		}
		return runServiceCommand("sysrc", appName+"_enable=NO")
	default:
		return fmt.Errorf("unsupported service manager")
	}
}

func runServiceCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func plistName() string {
	return "io.github." + orgName + "." + appName
}
