package overlay

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/testutil"
)

func TestPureOverlayHelpers(t *testing.T) {
	t.Run("bandwidth", func(t *testing.T) {
		for _, test := range []struct {
			input string
			want  uint64
		}{
			{"1 KB", 1024},
			{"2 MiB", 2 << 20},
			{"0.5 GB", 1 << 29},
			{"1 TiB", 1 << 40},
			{"unlimited", 0},
			{"-1 GB", 0},
			{"1 PB", 0},
			{"1 GB extra", 0},
		} {
			if got := parseBandwidth(test.input); got != test.want {
				t.Errorf("parseBandwidth(%q) = %d, want %d", test.input, got, test.want)
			}
		}
	})

	t.Run("onion hostname", func(t *testing.T) {
		valid := strings.Repeat("a", 56) + ".onion"
		if !validOnion(valid) {
			t.Fatalf("validOnion(%q) = false", valid)
		}
		for _, invalid := range []string{
			strings.Repeat("a", 55) + ".onion",
			strings.Repeat("a", 56) + ".ONION",
			strings.Repeat("0", 56) + ".onion",
		} {
			if validOnion(invalid) {
				t.Errorf("validOnion(%q) = true", invalid)
			}
		}
	})

	t.Run("binary resolution", func(t *testing.T) {
		root := testutil.TempDir(t)
		candidate := filepath.Join(root, "candidate")
		if err := os.WriteFile(candidate, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := resolveBinary("", "not-on-path", []string{filepath.Join(root, "missing"), candidate}); got != candidate {
			t.Fatalf("resolveBinary(candidate) = %q, want %q", got, candidate)
		}
		if got := resolveBinary(candidate, "not-on-path", nil); got != candidate {
			t.Fatalf("resolveBinary(explicit) = %q, want %q", got, candidate)
		}
		if got := resolveBinary(filepath.Join(root, "missing"), "not-on-path", nil); got != "" {
			t.Fatalf("resolveBinary(missing) = %q, want empty", got)
		}
	})

	t.Run("fallback values", func(t *testing.T) {
		if got := valueOr(0, 12); got != 12 {
			t.Errorf("valueOr(0, 12) = %d", got)
		}
		if got := valueOr(7, 12); got != 7 {
			t.Errorf("valueOr(7, 12) = %d", got)
		}
		if got := valueOrString("  ", "fallback"); got != "fallback" {
			t.Errorf("valueOrString(blank) = %q", got)
		}
		if got := valueOrString("custom", "fallback"); got != "custom" {
			t.Errorf("valueOrString(custom) = %q", got)
		}
	})

	t.Run("stale Tor control file removal", func(t *testing.T) {
		root := testutil.TempDir(t)
		missing := filepath.Join(root, "missing")
		if err := removeStaleControlFile(missing); err != nil {
			t.Fatalf("removeStaleControlFile(missing) error = %v", err)
		}
		file := filepath.Join(root, "control_port")
		if err := os.WriteFile(file, []byte("PORT=127.0.0.1:1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := removeStaleControlFile(file); err != nil {
			t.Fatalf("removeStaleControlFile(file) error = %v", err)
		}
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("stale control file still exists: %v", err)
		}
		nonemptyDir := filepath.Join(root, "control_dir")
		if err := os.Mkdir(nonemptyDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nonemptyDir, "child"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := removeStaleControlFile(nonemptyDir); err == nil {
			t.Fatal("removeStaleControlFile(non-empty directory) succeeded")
		}
	})

	t.Run("process log tail", func(t *testing.T) {
		path := filepath.Join(testutil.TempDir(t), "tor.log")
		if err := os.WriteFile(path, []byte("first\nsecond\nthird\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := sanitizedProcessTail(path); got != "first | second | third" {
			t.Fatalf("sanitizedProcessTail() = %q", got)
		}
		if got := sanitizedProcessTail(filepath.Join(filepath.Dir(path), "missing")); got != "" {
			t.Fatalf("missing log tail = %q, want empty", got)
		}
	})
}

func TestI2PKeyParsing(t *testing.T) {
	if _, err := i2pDestination("invalid"); err == nil {
		t.Fatal("i2pDestination accepted malformed base64")
	}
	truncated := make([]byte, 387)
	binary.BigEndian.PutUint16(truncated[385:387], 10)
	if _, err := i2pDestination(i2pBase64.EncodeToString(truncated)); err == nil {
		t.Fatal("i2pDestination accepted a truncated key")
	}
	key := make([]byte, 387)
	encoded := i2pBase64.EncodeToString(key)
	destination, err := i2pDestination(encoded)
	if err != nil || len(destination) != len(key) {
		t.Fatalf("i2pDestination(valid) = %d bytes, %v", len(destination), err)
	}
	hostname, err := i2pHostname(encoded)
	if err != nil || !strings.HasSuffix(hostname, ".b32.i2p") || len(hostname) != 60 {
		t.Fatalf("i2pHostname(valid) = %q, %v", hostname, err)
	}
}

func TestManagerStatusAndNoProviderClose(t *testing.T) {
	manager := NewManager(Options{})
	if manager.TorStatus().Status != "disabled" || manager.I2PStatus().Status != "disabled" {
		t.Fatalf("initial status = tor %+v, i2p %+v", manager.TorStatus(), manager.I2PStatus())
	}
	manager.setTor(TorStatus{Enabled: true, Status: "unavailable"})
	manager.setI2P(I2PStatus{Enabled: true, Status: "unavailable", State: "error"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.HealthCheckTor(ctx); err == nil {
		t.Fatal("HealthCheckTor() succeeded without a Tor process")
	}
	if err := manager.HealthCheckI2P(ctx); err == nil {
		t.Fatal("HealthCheckI2P() succeeded without an I2P provider")
	}
	manager.Close()
}

func TestTorStartupFailureCleansBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Tor.Binary = binary
	root := testutil.TempDir(t)
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	logsDir := filepath.Join(root, "logs")
	controlDir := filepath.Join(dataDir, "tor", "control_port")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{Config: *cfg, ConfigDir: configDir, DataDir: dataDir, LogsDir: logsDir})
	manager.startTor(context.Background())
	if got := manager.TorStatus().Status; got != "unavailable" {
		t.Fatalf("Tor status after stale control file failure = %q", got)
	}
	manager.mu.RLock()
	backend, server, process := manager.backend, manager.server, manager.cmd
	manager.mu.RUnlock()
	if backend != nil || server != nil || process != nil {
		t.Fatalf("Tor resources remain after failed startup: backend=%v server=%v process=%v", backend != nil, server != nil, process != nil)
	}
}

func TestTorControlPortWaitAcceptsLateCompleteFileAndRejectsPartialData(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("PORT="), []byte("PORT=127.0.0.1:"), []byte("PORT=127.0.0.1:0\n"), []byte("PORT=0.0.0.0:9051\n"), []byte("PORT=127.0.0.1:9051")} {
		if address, ok := parseTorControlPortFile(data); ok {
			t.Errorf("parseTorControlPortFile(%q) = %q, true; want invalid", data, address)
		}
	}
	if address, ok := parseTorControlPortFile([]byte("PORT=127.0.0.1:9051\n")); !ok || address != "127.0.0.1:9051" {
		t.Fatalf("parseTorControlPortFile(valid) = %q, %t", address, ok)
	}

	path := filepath.Join(testutil.TempDir(t), "control_port")
	if err := os.WriteFile(path, []byte("PORT=127.0.0.1:"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		if err := os.WriteFile(path, []byte("PORT=127.0.0.1:"), 0o600); err != nil {
			t.Errorf("write partial Tor control-port file: %v", err)
			return
		}
		time.Sleep(30 * time.Millisecond)
		if err := os.WriteFile(path, []byte("PORT=127.0.0.1:9051\n"), 0o600); err != nil {
			t.Errorf("complete Tor control-port file: %v", err)
		}
	}()
	address, err := waitForTorControlPort(context.Background(), path, time.Second, make(chan struct{}))
	if err != nil || address != "127.0.0.1:9051" {
		t.Fatalf("waitForTorControlPort(late complete file) = %q, %v", address, err)
	}
	processDone := make(chan struct{})
	close(processDone)
	if _, err := waitForTorControlPort(context.Background(), filepath.Join(filepath.Dir(path), "missing"), time.Second, processDone); err != errTorExitedBeforeControlPort {
		t.Fatalf("waitForTorControlPort(child exit) error = %v", err)
	}
}

func TestTorCookieRecoveryIsLimitedToOneRestart(t *testing.T) {
	dataDir := testutil.TempDir(t)
	cookiePath := filepath.Join(dataDir, "control_auth_cookie")
	if err := os.WriteFile(cookiePath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{DataDir: dataDir})
	if !manager.recoverTorCookie(dataDir, filepath.Join(dataDir, "tor.log"), fmt.Errorf("515 Authentication failed")) {
		t.Fatal("first stale-cookie recovery was rejected")
	}
	if _, err := os.Stat(cookiePath); !os.IsNotExist(err) {
		t.Fatalf("stale cookie remains after recovery: %v", err)
	}
	select {
	case <-manager.torRestart:
	default:
		t.Fatal("first cookie recovery did not queue a Tor restart")
	}
	if manager.recoverTorCookie(dataDir, filepath.Join(dataDir, "tor.log"), fmt.Errorf("515 Authentication failed")) {
		t.Fatal("second stale-cookie recovery was accepted")
	}
	select {
	case <-manager.torRestart:
		t.Fatal("cookie recovery queued more than one restart")
	default:
	}
}

func TestI2PNoProviderAndDisabledRestart(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.I2P.Enabled = true
	cfg.Server.I2P.Binary = filepath.Join(testutil.TempDir(t), "missing-i2pd")
	cfg.Server.I2P.SAMAddress = "127.0.0.1:1"
	configDir := filepath.Join(testutil.TempDir(t), "config")
	dataDir := filepath.Join(testutil.TempDir(t), "data")
	manager := NewManager(Options{Config: *cfg, ConfigDir: configDir, DataDir: dataDir})

	manager.startI2P(context.Background())
	status := manager.I2PStatus()
	if status.Status != "disabled" || status.State != "no_provider" || status.Provider != "none" {
		t.Fatalf("I2P status without provider = %+v", status)
	}
	if _, err := os.Stat(configDir); !os.IsNotExist(err) {
		t.Fatalf("I2P config directory created without a provider: err=%v", err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("I2P data directory created without a provider: err=%v", err)
	}

	disabled := NewManager(Options{})
	if err := disabled.RestartI2P(context.Background()); err != nil {
		t.Fatalf("RestartI2P() while disabled: %v", err)
	}
	if err := disabled.RestartTor(context.Background()); err != nil {
		t.Fatalf("RestartTor() without Tor binary: %v", err)
	}
	disabled.Start(context.Background())
	disabled.Close()
}
