package overlay

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/paths"
)

var i2pBase64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")

func (m *Manager) startI2P(ctx context.Context) {
	cfg := m.opts.Config.Server.I2P
	binaryPath := resolveBinary(cfg.Binary, "i2pd", []string{"/usr/bin/i2pd", "/usr/sbin/i2pd", "/usr/local/bin/i2pd", "/opt/homebrew/bin/i2pd"})
	provider, samAddress := "sam", cfg.SAMAddress
	if binaryPath != "" {
		provider = "i2pd"
	} else if !samReachable(ctx, samAddress) {
		log.Print("I2P enabled but no i2pd binary or SAM provider is available")
		m.setI2P(I2PStatus{Status: "disabled", State: "no_provider", Provider: "none"})
		return
	}
	configRoot, dataRoot := filepath.Join(m.opts.ConfigDir, "i2p"), filepath.Join(m.opts.DataDir, "i2p")
	if err := paths.EnsurePrivateTree(configRoot); err != nil {
		m.failI2P(provider, "secure config directory", err)
		return
	}
	if err := paths.EnsurePrivateTree(filepath.Join(dataRoot, "site")); err != nil {
		m.failI2P(provider, "secure data directory", err)
		return
	}
	if provider == "i2pd" {
		var err error
		samAddress, err = m.startI2PD(ctx, binaryPath, configRoot, dataRoot)
		if err != nil {
			m.failI2P(provider, "start i2pd", err)
			return
		}
	}
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.failI2P(provider, "backend listener", err)
		return
	}
	m.mu.Lock()
	m.i2pBackend = backend
	m.mu.Unlock()
	backendServer := &http.Server{Handler: m.opts.Handler, ReadHeaderTimeout: 15 * time.Second}
	m.mu.Lock()
	m.i2pServer = backendServer
	m.mu.Unlock()
	go func() {
		if err := backendServer.Serve(backend); err != nil && err != http.ErrServerClosed {
			log.Printf("I2P backend stopped: %v", err)
		}
	}()
	keyPath := filepath.Join(dataRoot, "site", "site-keys.dat")
	controlConn, controlReader, err := samDial(ctx, samAddress)
	if err != nil {
		_ = backendServer.Close()
		m.failI2P(provider, "SAM control", err)
		return
	}
	m.mu.Lock()
	m.i2pControl = controlConn
	m.i2pSAMAddress = samAddress
	m.mu.Unlock()
	priv, err := loadOrCreateI2PKey(controlConn, controlReader, keyPath, cfg.SignatureType)
	if err != nil {
		_ = controlConn.Close()
		_ = backendServer.Close()
		m.failI2P(provider, "destination key", err)
		return
	}
	sessionID := fmt.Sprintf("countries-%d-%d", os.Getpid(), time.Now().UnixNano())
	buildTimeout := time.Duration(cfg.BootstrapTimeout) * time.Second
	if buildTimeout < 30*time.Second || buildTimeout > 600*time.Second {
		buildTimeout = 300 * time.Second
	}
	m.setI2P(I2PStatus{Enabled: true, Status: "starting", State: "building", Provider: provider})
	options := fmt.Sprintf("i2cp.leaseSetEncType=4 inbound.length=%d outbound.length=%d inbound.quantity=%d outbound.quantity=%d", cfg.InboundLength, cfg.OutboundLength, cfg.InboundQuantity, cfg.OutboundQuantity)
	if err = ensureSAMReply(controlConn, controlReader, buildTimeout, "SESSION CREATE STYLE=STREAM ID=%s DESTINATION=%s %s", sessionID, priv, options); err != nil {
		if strings.Contains(err.Error(), "DUPLICATED_ID") {
			sessionID += "-retry"
			err = ensureSAMReply(controlConn, controlReader, buildTimeout, "SESSION CREATE STYLE=STREAM ID=%s DESTINATION=%s %s", sessionID, priv, options)
		}
	}
	if err != nil {
		_ = controlConn.Close()
		_ = backendServer.Close()
		m.failI2P(provider, "SAM session", err)
		return
	}
	forwardConn, forwardReader, err := samDial(ctx, samAddress)
	if err == nil {
		m.mu.Lock()
		m.i2pForward = forwardConn
		m.mu.Unlock()
		err = ensureSAMReply(forwardConn, forwardReader, 10*time.Second, "STREAM FORWARD ID=%s PORT=%d HOST=127.0.0.1 SILENT=true", sessionID, backend.Addr().(*net.TCPAddr).Port)
	}
	if err != nil {
		if forwardConn != nil {
			_ = forwardConn.Close()
		}
		_ = controlConn.Close()
		_ = backendServer.Close()
		m.failI2P(provider, "SAM forward", err)
		return
	}
	hostname, err := i2pHostname(priv)
	if err != nil {
		_ = forwardConn.Close()
		_ = controlConn.Close()
		_ = backendServer.Close()
		m.failI2P(provider, "destination address", err)
		return
	}
	m.setI2P(I2PStatus{Enabled: true, Running: true, Status: "healthy", State: "running", Hostname: hostname, Provider: provider})
	log.Printf("I2P eepsite started (%s): %s", provider, hostname)
	closed := controlConnClosed(controlConn)
	forwardClosed := controlConnClosed(forwardConn)
	probeTicker := time.NewTicker(30 * time.Second)
	defer probeTicker.Stop()
	probeFailures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-closed:
			m.failI2P(provider, "SAM session", fmt.Errorf("control socket closed"))
			return
		case <-forwardClosed:
			m.failI2P(provider, "SAM forward", fmt.Errorf("forward socket closed"))
			return
		case <-probeTicker.C:
			if provider == "sam" {
				if samReachable(ctx, samAddress) {
					probeFailures = 0
				} else {
					probeFailures++
				}
				if probeFailures >= 3 {
					m.failI2P(provider, "SAM liveness", fmt.Errorf("three consecutive probes failed"))
					_ = controlConn.Close()
					return
				}
			}
		}
	}
}

func (m *Manager) failI2P(provider, action string, err error) {
	log.Printf("I2P %s failed: %v", action, err)
	m.setI2P(I2PStatus{Enabled: true, Status: "unavailable", State: "error", Provider: provider})
}

func samReachable(ctx context.Context, addr string) bool {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, reader, err := samDial(probe, addr)
	if err != nil {
		return false
	}
	replyErr := ensureSAMReply(conn, reader, 5*time.Second, "QUIT")
	closeErr := conn.Close()
	return replyErr == nil && closeErr == nil
}

func samDial(ctx context.Context, addr string) (net.Conn, *bufio.Reader, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	r := bufio.NewReader(conn)
	reply, err := samCommand(conn, r, 10*time.Second, "HELLO VERSION MIN=3.1 MAX=3.3")
	if err != nil || reply["RESULT"] != "OK" {
		_ = conn.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("SAM HELLO %s", reply["RESULT"])
	}
	return conn, r, nil
}

func samCommand(conn net.Conn, reader *bufio.Reader, timeout time.Duration, format string, args ...any) (values map[string]string, resultErr error) {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set SAM deadline: %w", err)
	}
	defer func() {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clear SAM deadline: %w", err))
		}
	}()
	if _, err := fmt.Fprintf(conn, format+"\n", args...); err != nil {
		return nil, fmt.Errorf("SAM write: %w", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("SAM read: %w", err)
	}
	values = make(map[string]string)
	for _, part := range strings.Fields(strings.TrimSpace(line)) {
		if key, value, ok := strings.Cut(part, "="); ok {
			values[key] = strings.Trim(value, "\"")
		}
	}
	return values, nil
}

func ensureSAMReply(conn net.Conn, reader *bufio.Reader, timeout time.Duration, format string, args ...any) error {
	reply, err := samCommand(conn, reader, timeout, format, args...)
	if err != nil {
		return err
	}
	if reply["RESULT"] != "OK" {
		return fmt.Errorf("SAM command failed: %s", reply["RESULT"])
	}
	return nil
}

func loadOrCreateI2PKey(conn net.Conn, reader *bufio.Reader, path string, signatureType int) (key string, resultErr error) {
	contents, err := os.ReadFile(path)
	if err == nil {
		priv := strings.TrimSpace(string(contents))
		if _, err := i2pDestination(priv); err != nil {
			return "", fmt.Errorf("stored destination key is invalid")
		}
		return priv, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if signatureType <= 0 {
		signatureType = 7
	}
	reply, err := samCommand(conn, reader, 10*time.Second, "DEST GENERATE SIGNATURE_TYPE=%d", signatureType)
	if err != nil {
		return "", err
	}
	priv := reply["PRIV"]
	if _, err := i2pDestination(priv); err != nil {
		return "", fmt.Errorf("SAM returned invalid destination key")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".site-keys-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() {
		if removeErr := os.Remove(tmpName); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary I2P key: %w", removeErr))
		}
	}()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString(priv + "\n")
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(tmpName, path); err != nil {
		return "", err
	}
	return priv, nil
}

func i2pDestination(priv string) ([]byte, error) {
	decoded, err := i2pBase64.DecodeString(priv)
	if err != nil || len(decoded) < 387 {
		return nil, fmt.Errorf("invalid key")
	}
	length := 387 + int(binary.BigEndian.Uint16(decoded[385:387]))
	if len(decoded) < length {
		return nil, fmt.Errorf("truncated key")
	}
	return decoded[:length], nil
}

func i2pHostname(priv string) (string, error) {
	destination, err := i2pDestination(priv)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(destination)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(hash[:])) + ".b32.i2p", nil
}

func (m *Manager) startI2PD(ctx context.Context, binaryPath, configRoot, dataRoot string) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	samPort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	confPath := filepath.Join(configRoot, "i2pd.conf")
	tunnelPath := filepath.Join(configRoot, "tunnels.conf")
	tunnelDir := filepath.Join(configRoot, "tunnels.d")
	pidPath := filepath.Join(dataRoot, "i2pd.pid")
	settings := fmt.Sprintf("daemon = false\nipv4 = true\nipv6 = false\nnotransit = true\nfloodfill = false\nlog = file\nlogfile = %s\nloglevel = warn\n\n[http]\nenabled = false\n[httpproxy]\nenabled = false\n[socksproxy]\nenabled = false\n[sam]\nenabled = true\naddress = 127.0.0.1\nport = %d\n[bob]\nenabled = false\n[i2cp]\nenabled = false\n[i2pcontrol]\nenabled = false\n[upnp]\nenabled = false\n", filepath.Join(m.opts.LogsDir, "i2pd.log"), samPort)
	if err = os.WriteFile(confPath, []byte(settings), 0600); err != nil {
		return "", err
	}
	if err = os.WriteFile(tunnelPath, nil, 0600); err != nil {
		return "", err
	}
	if err = os.MkdirAll(tunnelDir, 0700); err != nil {
		return "", err
	}
	logFile, err := os.OpenFile(filepath.Join(m.opts.LogsDir, "i2pd.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	if err = reapStaleI2PD(pidPath, binaryPath, dataRoot); err != nil {
		_ = logFile.Close()
		return "", err
	}
	cmd := exec.Command(binaryPath, "--conf", confPath, "--tunconf", tunnelPath, "--tunnelsdir", tunnelDir, "--datadir", dataRoot, "--pidfile", pidPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	setParentDeathSignal(cmd)
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", err
	}
	if err = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0600); err != nil {
		_ = cmd.Process.Kill()
		_ = logFile.Close()
		return "", err
	}
	done := make(chan struct{})
	m.mu.Lock()
	m.i2pCmd = cmd
	m.i2pProcessDone = done
	m.mu.Unlock()
	go func() { _ = cmd.Wait(); close(done); _ = logFile.Close() }()
	address := fmt.Sprintf("127.0.0.1:%d", samPort)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if conn, _, dialErr := samDial(ctx, address); dialErr == nil {
			_ = conn.Close()
			return address, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-done:
			return "", fmt.Errorf("i2pd exited during startup")
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("i2pd SAM port startup timeout")
}

func controlConnClosed(conn net.Conn) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		var b [256]byte
		for {
			if _, err := conn.Read(b[:]); err != nil {
				close(done)
				return
			}
		}
	}()
	return done
}

func stopI2PProcess(cmd *exec.Cmd, done <-chan struct{}) {
	terminateI2PD(cmd, done)
}

func removeManagedI2PPID(pidPath string, pid int) {
	contents, err := os.ReadFile(pidPath)
	if err != nil || strings.TrimSpace(string(contents)) != strconv.Itoa(pid) {
		return
	}
	_ = os.Remove(pidPath)
}
