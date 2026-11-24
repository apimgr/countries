//go:build windows

package overlay

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func setParentDeathSignal(cmd *exec.Cmd) {}

func terminateI2PD(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
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
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	script := "$p=Get-CimInstance Win32_Process -Filter 'ProcessId=" + strconv.Itoa(pid) + "'; if ($p -and [IO.Path]::GetFullPath($p.ExecutablePath) -ieq [IO.Path]::GetFullPath(" + quote(binaryPath) + ") -and $p.CommandLine.Contains(" + quote("--datadir "+dataDir) + ")) { Stop-Process -Id " + strconv.Itoa(pid) + " -Force }"
	command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	return command.Run()
}
