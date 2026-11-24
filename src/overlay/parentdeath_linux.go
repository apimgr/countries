//go:build linux

package overlay

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func setParentDeathSignal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

func terminateI2PD(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func reapStaleI2PD(pidPath, binaryPath, dataDir string) error {
	content, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return nil
	}
	args, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	parts := strings.Split(string(bytes.TrimRight(args, "\x00")), "\x00")
	if len(parts) == 0 || filepath.Base(parts[0]) != filepath.Base(binaryPath) {
		return nil
	}
	processPath, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil || filepath.Clean(processPath) != filepath.Clean(binaryPath) {
		return nil
	}
	matchedDataDir := false
	for index := 1; index+1 < len(parts); index++ {
		if parts[index] == "--datadir" && filepath.Clean(parts[index+1]) == filepath.Clean(dataDir) {
			matchedDataDir = true
			break
		}
	}
	if !matchedDataDir {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err = process.Signal(syscall.SIGTERM); err != nil && err != syscall.ESRCH {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err = process.Kill(); err != nil && err != syscall.ESRCH {
		return err
	}
	return nil
}
