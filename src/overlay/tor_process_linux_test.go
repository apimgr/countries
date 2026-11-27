//go:build linux

package overlay

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/testutil"
)

func TestTorBootstrapTimeoutReapsFakeProcessAndBackend(t *testing.T) {
	root := testutil.TempDir(t)
	torStub := filepath.Join(root, "tor")
	if err := os.WriteFile(torStub, []byte("#!/bin/sh\nexec sleep 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Server.Tor.Binary = torStub
	cfg.Server.Tor.StartupTimeout = 50
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	logsDir := filepath.Join(root, "logs")
	manager := NewManager(Options{Config: *cfg, ConfigDir: configDir, DataDir: dataDir, LogsDir: logsDir})
	manager.startTor(context.Background())
	if got := manager.TorStatus().Status; got != "unavailable" {
		t.Fatalf("Tor status after startup timeout = %q", got)
	}
	manager.mu.RLock()
	command, done, backend, server := manager.cmd, manager.processDone, manager.backend, manager.server
	manager.mu.RUnlock()
	if command != nil || done != nil || backend != nil || server != nil {
		t.Fatalf("Tor resources remain after timeout: process=%v done=%v backend=%v server=%v", command != nil, done != nil, backend != nil, server != nil)
	}
	torrc, err := os.ReadFile(filepath.Join(configDir, "tor", "torrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(torrc), "__OwningControllerProcess "+strconv.Itoa(os.Getpid())) {
		t.Fatal("torrc is missing its parent-death ownership backstop")
	}
}
