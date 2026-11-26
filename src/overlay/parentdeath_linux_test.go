//go:build linux

package overlay

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
)

func TestParentDeathSignalKillsManagedChild(t *testing.T) {
	if os.Getenv("COUNTRIES_PDEATH_HELPER") == "1" {
		pidFile := os.Getenv("COUNTRIES_PDEATH_PID_FILE")
		child := exec.Command(os.Args[0], "-test.run=^TestParentDeathSignalBlocker$")
		child.Env = append(os.Environ(), "COUNTRIES_PDEATH_BLOCKER=1")
		setParentDeathSignal(child)
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			_ = child.Process.Kill()
			os.Exit(3)
		}
		os.Exit(0)
	}
	if os.Getenv("COUNTRIES_PDEATH_BLOCKER") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}

	pidFile := filepath.Join(testutil.TempDir(t), "managed-child.pid")
	helper := exec.Command(os.Args[0], "-test.run=^TestParentDeathSignalKillsManagedChild$")
	helper.Env = append(os.Environ(), "COUNTRIES_PDEATH_HELPER=1", "COUNTRIES_PDEATH_PID_FILE="+pidFile)
	if err := helper.Run(); err != nil {
		t.Fatalf("start parent-death helper: %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read managed child PID: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil || pid <= 1 {
		t.Fatalf("managed child PID %q is invalid: %v", data, err)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("managed child PID %d remained alive after its parent exited", pid)
}

func TestParentDeathSignalBlocker(t *testing.T) {
	if os.Getenv("COUNTRIES_PDEATH_BLOCKER") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}
