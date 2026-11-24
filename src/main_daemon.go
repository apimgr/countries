package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const daemonChildMarker = "_DAEMON_CHILD"

func daemonChildArgs(args []string) []string {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--daemon" || arg == "--daemon=true" || arg == "-daemon" || arg == "-daemon=true" {
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered
}

func daemonChildEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, daemonChildMarker+"=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, daemonChildMarker+"=1")
}

func shouldDaemonize(daemonFlag, configValue, serviceStart, container, managed, child bool) bool {
	return !child && !serviceStart && !container && !managed && (daemonFlag || configValue)
}

func inContainer() bool {
	for _, path := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if serviceManagerEnvironmentPresent() {
		return false
	}
	for _, path := range []string{"/dev/lxc"} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	if os.Getenv("container") != "" {
		return true
	}
	if parent := parentProcessName(); parent == "tini" || parent == "dumb-init" || parent == "s6-svscan" || parent == "runsv" || parent == "runsvdir" || parent == "catatonit" || parent == projectName {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := strings.ToLower(string(data))
		for _, marker := range []string{"docker", "kubepods", "lxc"} {
			if strings.Contains(content, marker) {
				return true
			}
		}
	}
	return false
}

func serviceManagerEnvironmentPresent() bool {
	for _, name := range []string{"INVOCATION_ID", "RC_SVCNAME", "SVDIR", "S6_LOGGING"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	if os.Getppid() == 1 {
		for _, path := range []string{"/run/systemd/system", "/run/openrc"} {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
		if runtime.GOOS == "darwin" {
			return true
		}
		if _, err := os.Stat("/etc/rc.subr"); err == nil {
			return true
		}
		if _, err := os.Stat("/etc/init.d"); err == nil {
			if _, err := os.Stat("/run/systemd/system"); os.IsNotExist(err) {
				return true
			}
		}
	}
	return false
}

func parentProcessName() string {
	parent := strconv.Itoa(os.Getppid())
	if data, err := os.ReadFile(filepath.Join("/proc", parent, "comm")); err == nil {
		return strings.TrimSpace(string(data))
	}
	output, err := exec.Command("ps", "-p", parent, "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func startDaemonIfRequested(daemonFlag, configValue, serviceStart bool) (bool, error) {
	child := os.Getenv(daemonChildMarker) == "1"
	if !shouldDaemonize(daemonFlag, configValue, serviceStart, inContainer(), serviceManagerEnvironmentPresent(), child) {
		return false, nil
	}
	if err := launchDaemonChild(); err != nil {
		return false, fmt.Errorf("start daemon process: %w", err)
	}
	return true, nil
}
