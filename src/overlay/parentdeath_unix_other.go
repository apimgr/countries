//go:build !linux && !windows

package overlay

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func setParentDeathSignal(cmd *exec.Cmd) {}

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
	contents, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return nil
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return nil
	}
	command := strings.TrimSpace(string(output))
	if !strings.Contains(command, binaryPath) || !strings.Contains(command, "--datadir "+dataDir) {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err = process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return process.Kill()
}
