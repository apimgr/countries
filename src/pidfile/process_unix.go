//go:build !windows

package pidfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func processIsThisExecutable(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil || process.Signal(syscall.Signal(0)) != nil {
		return false
	}
	current, err := os.Executable()
	if err != nil {
		return false
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return false
	}
	procExe := "/proc/" + strconv.Itoa(pid) + "/exe"
	if _, err := os.Readlink(procExe); err != nil {
		output, psErr := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if psErr != nil {
			return false
		}
		return strings.TrimSpace(string(output)) == filepath.Base(current)
	}
	processInfo, err := os.Stat(procExe)
	if err != nil {
		return false
	}
	currentInfo, err := os.Stat(current)
	return err == nil && os.SameFile(processInfo, currentInfo)
}
