// Package overlay manages the application's isolated Tor and I2P listeners.
package overlay

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/paths"
	"github.com/cretz/bine/control"
	"github.com/pires/go-proxyproto"
	"golang.org/x/net/proxy"
)

type TorStatus struct {
	Enabled  bool
	Running  bool
	Status   string
	Hostname string
}

type I2PStatus struct {
	Enabled  bool
	Running  bool
	Status   string
	State    string
	Hostname string
	Provider string
}

type Options struct {
	Config          config.Config
	ConfigDir       string
	DataDir         string
	LogsDir         string
	Handler         http.Handler
	TorHandler      http.Handler
	OnStatus        func(TorStatus, I2PStatus)
	OnTorBandwidth  func(uint64)
	OnTorHTTPClient func(*http.Client)
}

type Manager struct {
	opts               Options
	mu                 sync.RWMutex
	tor                TorStatus
	i2p                I2PStatus
	cmd                *exec.Cmd
	processDone        chan struct{}
	ctrl               *control.Conn
	backend            net.Listener
	server             *http.Server
	i2pCmd             *exec.Cmd
	i2pProcessDone     chan struct{}
	i2pControl         net.Conn
	i2pForward         net.Conn
	i2pBackend         net.Listener
	i2pServer          *http.Server
	i2pSAMAddress      string
	closeOnce          sync.Once
	runtimeCancel      context.CancelFunc
	bandwidthWarnings  map[int]bool
	torDialer          proxy.ContextDialer
	torHTTPClient      *http.Client
	torRestart         chan struct{}
	i2pRestart         chan struct{}
	cookieRecoveryUsed bool
}

var controlPortLine = regexp.MustCompile(`^PORT=(127\.0\.0\.1:[0-9]{1,5})\n$`)

// NewManager constructs the per-process overlay runtime.
func NewManager(opts Options) *Manager {
	return &Manager{opts: opts, tor: TorStatus{Status: "disabled"}, i2p: I2PStatus{Status: "disabled", State: "disabled", Provider: "none"}, bandwidthWarnings: make(map[int]bool), torRestart: make(chan struct{}, 1), i2pRestart: make(chan struct{}, 1)}
}

// Start returns immediately; provider discovery and bootstrap run in the background.
func (m *Manager) Start(ctx context.Context) {
	runtimeCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.runtimeCancel = cancel
	m.mu.Unlock()
	go m.runTor(runtimeCtx)
	if m.opts.Config.Server.I2P.Enabled {
		m.setI2P(I2PStatus{Enabled: true, Status: "starting", State: "starting", Provider: "none"})
		go m.runI2P(runtimeCtx)
	}
}

func (m *Manager) runTor(ctx context.Context) {
	delay := 5 * time.Second
	for ctx.Err() == nil && m.TorBinaryFound() {
		m.startTor(ctx)
		if ctx.Err() != nil {
			return
		}
		restarted := false
		if m.TorStatus().Status == "healthy" {
			m.mu.RLock()
			done := m.processDone
			m.mu.RUnlock()
			if done != nil {
				select {
				case <-ctx.Done():
					return
				case <-done:
				case <-m.torRestart:
					restarted = true
				}
			}
		}
		m.stopTorRuntime()
		if ctx.Err() != nil {
			return
		}
		if restarted {
			continue
		}
		log.Print("Tor hidden service unavailable; retrying")
		select {
		case <-ctx.Done():
			return
		case <-m.torRestart:
			continue
		case <-time.After(delay):
		}
		if delay < 300*time.Second {
			delay *= 2
			if delay > 300*time.Second {
				delay = 300 * time.Second
			}
		}
	}
}

func (m *Manager) TorStatus() TorStatus { m.mu.RLock(); defer m.mu.RUnlock(); return m.tor }
func (m *Manager) I2PStatus() I2PStatus { m.mu.RLock(); defer m.mu.RUnlock(); return m.i2p }

func (m *Manager) initializeTorOutbound(ctrl *control.Conn, enabled bool) {
	var dialer proxy.ContextDialer
	var client *http.Client
	if enabled {
		address, err := torSOCKSAddress(ctrl)
		if err == nil {
			var candidate proxy.Dialer
			candidate, err = proxy.SOCKS5("tcp", address, nil, proxy.Direct)
			if contextual, ok := candidate.(proxy.ContextDialer); ok && err == nil {
				dialer = contextual
			} else if err == nil {
				err = fmt.Errorf("SOCKS dialer does not support context cancellation")
			}
		}
		if dialer == nil {
			log.Printf("Tor outbound connections unavailable: %v", err)
		} else {
			client = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DialContext: dialer.DialContext, ForceAttemptHTTP2: true}}
			log.Print("Tor outbound connections enabled")
		}
	}
	m.mu.Lock()
	m.torDialer, m.torHTTPClient = dialer, client
	m.mu.Unlock()
	if m.opts.OnTorHTTPClient != nil {
		m.opts.OnTorHTTPClient(client)
	}
}

func torSOCKSAddress(ctrl *control.Conn) (string, error) {
	values, err := ctrl.GetInfo("net/listeners/socks")
	if err != nil {
		return "", err
	}
	for _, value := range values {
		for _, candidate := range strings.Fields(strings.Trim(value.Val, "\"")) {
			candidate = strings.Trim(candidate, "\" ,")
			host, portText, splitErr := net.SplitHostPort(candidate)
			if splitErr != nil || host != "127.0.0.1" {
				continue
			}
			port, parseErr := strconv.Atoi(portText)
			if parseErr == nil && port > 0 && port <= 65535 {
				return net.JoinHostPort(host, portText), nil
			}
		}
	}
	return "", fmt.Errorf("tor did not report a loopback SOCKS listener")
}

// TorBinaryFound reports whether the configured or auto-detected Tor executable exists.
func (m *Manager) TorBinaryFound() bool {
	cfg := m.opts.Config.Server.Tor
	return resolveBinary(cfg.Binary, "tor", []string{"/usr/bin/tor", "/usr/sbin/tor", "/usr/local/bin/tor", "/opt/homebrew/bin/tor"}) != ""
}

// RestartTor replaces the managed Tor process and hidden-service listener.
func (m *Manager) RestartTor(ctx context.Context) error {
	if !m.TorBinaryFound() {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.torRestart <- struct{}{}:
	default:
	}
	return nil
}

// RestartI2P recreates the SAM session and forwarding tunnel using the saved destination.
func (m *Manager) RestartI2P(ctx context.Context) error {
	if !m.opts.Config.Server.I2P.Enabled {
		return nil
	}
	m.stopI2PRuntime()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.i2pRestart <- struct{}{}:
	default:
	}
	return nil
}

func (m *Manager) runI2P(ctx context.Context) {
	delay := 5 * time.Second
	for ctx.Err() == nil {
		m.startI2P(ctx)
		if ctx.Err() != nil {
			return
		}
		m.stopI2PRuntime()
		select {
		case <-ctx.Done():
			return
		case <-m.i2pRestart:
			continue
		case <-time.After(delay):
		}
		if delay < 300*time.Second {
			delay *= 2
			if delay > 300*time.Second {
				delay = 300 * time.Second
			}
		}
	}
}

func (m *Manager) stopTorRuntime() {
	m.mu.Lock()
	ctrl, server, cmd, done := m.ctrl, m.server, m.cmd, m.processDone
	m.ctrl, m.server, m.backend, m.cmd, m.processDone = nil, nil, nil, nil, nil
	m.torDialer, m.torHTTPClient = nil, nil
	m.mu.Unlock()
	if m.opts.OnTorHTTPClient != nil {
		m.opts.OnTorHTTPClient(nil)
	}
	if ctrl != nil {
		_ = ctrl.Signal("SHUTDOWN")
		_ = ctrl.Close()
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	}
	if cmd != nil && cmd.Process != nil && done != nil {
		select {
		case <-done:
			return
		case <-time.After(10 * time.Second):
		}
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
			return
		case <-time.After(5 * time.Second):
		}
		_ = cmd.Process.Kill()
		<-done
	}
}

func (m *Manager) stopI2PRuntime() {
	m.mu.Lock()
	cmd, done, controlConn, forward, server := m.i2pCmd, m.i2pProcessDone, m.i2pControl, m.i2pForward, m.i2pServer
	m.i2pCmd, m.i2pProcessDone, m.i2pControl, m.i2pForward, m.i2pBackend, m.i2pServer = nil, nil, nil, nil, nil, nil
	m.mu.Unlock()
	if forward != nil {
		_ = forward.Close()
	}
	if controlConn != nil {
		_ = controlConn.Close()
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	}
	stopI2PProcess(cmd, done)
	if cmd != nil && cmd.Process != nil {
		removeManagedI2PPID(filepath.Join(m.opts.DataDir, "i2p", "i2pd.pid"), cmd.Process.Pid)
	}
}

func (m *Manager) setTor(s TorStatus) {
	m.mu.Lock()
	m.tor = s
	t, i := m.tor, m.i2p
	m.mu.Unlock()
	if m.opts.OnStatus != nil {
		m.opts.OnStatus(t, i)
	}
}
func (m *Manager) setI2P(s I2PStatus) {
	m.mu.Lock()
	m.i2p = s
	t, i := m.tor, m.i2p
	m.mu.Unlock()
	if m.opts.OnStatus != nil {
		m.opts.OnStatus(t, i)
	}
}

func (m *Manager) startTor(ctx context.Context) {
	cfg := m.opts.Config.Server.Tor
	binary := resolveBinary(cfg.Binary, "tor", []string{"/usr/bin/tor", "/usr/sbin/tor", "/usr/local/bin/tor", "/opt/homebrew/bin/tor"})
	if binary == "" {
		log.Print("Tor binary not found; hidden service disabled")
		return
	}
	if err := paths.EnsurePrivateTree(filepath.Join(m.opts.ConfigDir, "tor")); err != nil {
		log.Printf("Tor unavailable: private configuration directory: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	if err := paths.EnsurePrivateTree(filepath.Join(m.opts.DataDir, "tor", "site")); err != nil {
		log.Printf("Tor unavailable: private data directory: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Printf("Tor unavailable: backend listener: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	backend := &proxyproto.Listener{Listener: ln, ConnPolicy: func(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) { return proxyproto.REQUIRE, nil }, ReadHeaderTimeout: 5 * time.Second}
	torHandler := m.opts.TorHandler
	if torHandler == nil {
		torHandler = m.opts.Handler
	}
	server := &http.Server{Handler: torHandler, ReadHeaderTimeout: 15 * time.Second}
	m.mu.Lock()
	m.backend, m.server = backend, server
	m.mu.Unlock()
	backendDone := make(chan struct{})
	runtimeRetained := false
	defer func() {
		if !runtimeRetained {
			m.stopTorRuntime()
			select {
			case <-backendDone:
			case <-time.After(5 * time.Second):
				log.Print("Tor backend did not stop before cleanup deadline")
			}
		}
	}()
	go func() {
		defer close(backendDone)
		if err := server.Serve(backend); err != nil && err != http.ErrServerClosed {
			log.Printf("Tor backend stopped: %v", err)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	dataDir := filepath.Join(m.opts.DataDir, "tor")
	hsDir := filepath.Join(dataDir, "site")
	controlFile := filepath.Join(dataDir, "control_port")
	if err := removeStaleControlFile(controlFile); err != nil {
		log.Printf("Tor unavailable: remove stale control-port file: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	torrc := strings.Join([]string{
		"DataDirectory " + dataDir,
		"ControlPort 127.0.0.1:auto",
		"ControlPortWriteToFile " + controlFile,
		"CookieAuthentication 1",
		"DisableNetwork 1",
		fmt.Sprintf("__OwningControllerProcess %d", os.Getpid()),
		fmt.Sprintf("SocksPort %s", map[bool]string{true: "127.0.0.1:auto", false: "0"}[cfg.UseNetwork]), "SocksPolicy accept 127.0.0.1",
		"ExitRelay 0", "ExitPolicy reject *:*", "ORPort 0", "PublishServerDescriptor 0", "DirPort 0",
		"SafeLogging 1", "MaxCircuitDirtiness 600", "VanguardsLiteEnabled 1", "HiddenServiceSingleHopMode 0", "FetchDirInfoEarly 1", "FetchDirInfoExtraEarly 1", "DisableDebuggerAttachment 1",
		fmt.Sprintf("MaxClientCircuitsPending %d", valueOr(cfg.MaxCircuits, 32)), "LearnCircuitBuildTimeout 0", fmt.Sprintf("CircuitBuildTimeout %d", valueOr(cfg.CircuitTimeout, 60)),
		"BandwidthRate " + valueOrString(cfg.BandwidthRate, "1 MB"), "BandwidthBurst " + valueOrString(cfg.BandwidthBurst, "2 MB"),
		"HiddenServiceDir " + hsDir,
		fmt.Sprintf("HiddenServicePort %d 127.0.0.1:%d", valueOr(cfg.VirtualPort, 80), port),
		"HiddenServiceVersion 3", fmt.Sprintf("HiddenServiceNumIntroductionPoints %d", valueOr(cfg.NumIntroPoints, 3)), fmt.Sprintf("HiddenServiceMaxStreams %d", valueOr(cfg.MaxStreamsPerCircuit, 100)), fmt.Sprintf("HiddenServiceMaxStreamsCloseCircuit %d", map[bool]int{true: 1, false: 0}[cfg.CloseCircuitOnStreamLimit]), "HiddenServiceExportCircuitID haproxy",
	}, "\n") + "\n"
	if strings.ToLower(strings.TrimSpace(cfg.MaxMonthlyBandwidth)) != "unlimited" {
		torrc = strings.Replace(torrc, "HiddenServiceDir "+hsDir, "AccountingStart month 1 00:00\nAccountingMax "+valueOrString(cfg.MaxMonthlyBandwidth, "100 GB")+"\nHiddenServiceDir "+hsDir, 1)
	}
	torrcPath := filepath.Join(m.opts.ConfigDir, "tor", "torrc")
	if err := os.WriteFile(torrcPath, []byte(torrc), 0600); err != nil {
		log.Printf("Tor unavailable: write torrc: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	if err := os.MkdirAll(m.opts.LogsDir, 0700); err != nil {
		log.Printf("Tor unavailable: log directory: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	logFile, err := os.OpenFile(filepath.Join(m.opts.LogsDir, "tor.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("Tor unavailable: log file: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	cmd := exec.Command(binary, "-f", torrcPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		if closeErr := logFile.Close(); closeErr != nil {
			log.Printf("Tor unavailable: start: %v; close Tor log: %v", err, closeErr)
		} else {
			log.Printf("Tor unavailable: start: %v", err)
		}
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	torLogPath := filepath.Join(m.opts.LogsDir, "tor.log")
	processDone := make(chan struct{})
	m.mu.Lock()
	m.cmd = cmd
	m.processDone = processDone
	m.mu.Unlock()
	m.setTor(TorStatus{Enabled: true, Status: "starting"})
	go func() {
		waitErr := cmd.Wait()
		close(processDone)
		if closeErr := logFile.Close(); closeErr != nil {
			log.Printf("Close Tor log file: %v", closeErr)
		}
		if ctx.Err() == nil {
			if waitErr != nil {
				log.Printf("Tor process exited: %v", waitErr)
			} else {
				log.Printf("Tor process exited")
			}
			m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		}
	}()
	startup := time.Duration(valueOr(cfg.StartupTimeout, 60)) * time.Second
	address, waitErr := waitForTorControlPort(ctx, controlFile, startup, processDone)
	if ctx.Err() != nil {
		return
	}
	if waitErr != nil {
		if waitErr == errTorExitedBeforeControlPort {
			log.Printf("Tor process exited before its control port opened: status=%v diagnostics=%q", cmd.ProcessState, sanitizedProcessTail(torLogPath))
		} else {
			log.Printf("Tor control port did not become available: %v diagnostics=%q", waitErr, sanitizedProcessTail(torLogPath))
		}
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	hostnameLogged := false
	if b, err := os.ReadFile(filepath.Join(hsDir, "hostname")); err == nil {
		host := strings.TrimSpace(string(b))
		if validOnion(host) {
			status := m.TorStatus()
			status.Enabled, status.Status, status.Hostname = true, "starting", host
			m.setTor(status)
			log.Printf("Tor hidden service address available: %s", host)
			hostnameLogged = true
		}
	}
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		log.Printf("Tor control connection failed: %v diagnostics=%q", err, sanitizedProcessTail(torLogPath))
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	ctrl := control.NewConn(textproto.NewConn(conn))
	if err = ctrl.Authenticate(""); err != nil {
		_ = conn.Close()
		if m.recoverTorCookie(dataDir, torLogPath, err) {
			return
		}
		log.Printf("Tor control authentication failed: %v diagnostics=%q", err, sanitizedProcessTail(torLogPath))
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	m.mu.Lock()
	m.ctrl = ctrl
	m.mu.Unlock()
	events := make(chan control.Event, 32)
	if err = ctrl.AddEventListener(events, control.EventCodeStatusClient, control.EventCodeHSDesc); err != nil {
		log.Printf("Tor event subscription failed: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	go func() {
		if eventErr := ctrl.HandleEvents(ctx); eventErr != nil && ctx.Err() == nil {
			log.Printf("Tor control connection lost: %v", eventErr)
			m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
			_ = cmd.Process.Signal(os.Interrupt)
		}
	}()
	uploaded := make(chan struct{}, 1)
	bootstrappedEvent := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-events:
				if item, ok := event.(*control.HSDescEvent); ok && item.Action == "UPLOADED" {
					select {
					case uploaded <- struct{}{}:
					default:
					}
				}
				if strings.Contains(fmt.Sprint(event), "BOOTSTRAP PROGRESS=100") {
					select {
					case bootstrappedEvent <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	if err = ctrl.SetConf(&control.KeyVal{Key: "DisableNetwork", Val: "0"}); err != nil {
		log.Printf("Tor bootstrap could not start: %v", err)
		m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
		return
	}
	bootstrap := time.Duration(valueOr(cfg.BootstrapTimeout, 180)) * time.Second
	deadline := time.Now().Add(bootstrap)
	bootstrapped := false
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if phase, phaseErr := ctrl.GetInfo("status/bootstrap-phase"); phaseErr == nil && len(phase) > 0 && strings.Contains(phase[0].Val, "PROGRESS=100") {
			bootstrapped = true
		}
		select {
		case <-bootstrappedEvent:
			bootstrapped = true
		default:
		}
		if bootstrapped {
			if b, e := os.ReadFile(filepath.Join(hsDir, "hostname")); e == nil {
				candidate := strings.TrimSpace(string(b))
				if validOnion(candidate) {
					m.initializeTorOutbound(ctrl, cfg.UseNetwork)
					m.mu.Lock()
					m.cookieRecoveryUsed = false
					m.mu.Unlock()
					m.setTor(TorStatus{Enabled: true, Running: true, Status: "healthy", Hostname: candidate})
					runtimeRetained = true
					if !hostnameLogged {
						log.Printf("Tor hidden service: %s", candidate)
					}
					go m.monitorTorAccounting(ctx, ctrl, cfg.MaxMonthlyBandwidth)
					select {
					case <-uploaded:
					case <-ctx.Done():
						return
					case <-time.After(60 * time.Second):
						log.Print("Tor hidden service descriptor upload was not confirmed within 60 seconds")
					}
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-m.processDone:
			log.Printf("Tor process exited during bootstrap: status=%v diagnostics=%q", cmd.ProcessState, sanitizedProcessTail(torLogPath))
			m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	log.Printf("Tor hidden service bootstrap timed out: diagnostics=%q", sanitizedProcessTail(torLogPath))
	m.setTor(TorStatus{Enabled: true, Status: "unavailable"})
}

func (m *Manager) recoverTorCookie(dataDir, logPath string, authErr error) bool {
	message := strings.ToLower(authErr.Error())
	if !strings.Contains(message, "515") && !strings.Contains(message, "authentication failed") {
		return false
	}
	m.mu.Lock()
	if m.cookieRecoveryUsed {
		m.mu.Unlock()
		return false
	}
	m.cookieRecoveryUsed = true
	m.mu.Unlock()
	m.stopTorRuntime()
	cookiePath := filepath.Join(dataDir, "control_auth_cookie")
	if err := os.Remove(cookiePath); err != nil && !os.IsNotExist(err) {
		log.Printf("Tor stale control cookie could not be removed: %v diagnostics=%q", err, sanitizedProcessTail(logPath))
		return false
	}
	log.Printf("Tor control cookie was stale or unreadable; removed it and restarting once: diagnostics=%q", sanitizedProcessTail(logPath))
	select {
	case m.torRestart <- struct{}{}:
	default:
	}
	return true
}

func removeStaleControlFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale tor control-port file: %w", err)
	}
	return nil
}

var errTorExitedBeforeControlPort = fmt.Errorf("tor process exited before its control port became available")

func waitForTorControlPort(ctx context.Context, path string, timeout time.Duration, processDone <-chan struct{}) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if address, ok := parseTorControlPortFile(data); ok {
				return address, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-processDone:
			return "", errTorExitedBeforeControlPort
		case <-time.After(250 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("control port file not ready after %s", timeout)
}

func parseTorControlPortFile(data []byte) (string, bool) {
	match := controlPortLine.FindSubmatch(data)
	if match == nil {
		return "", false
	}
	address := string(match[1])
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	return address, true
}

func sanitizedProcessTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("Could not close Tor log file: %v", closeErr)
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	const tailBytes = 8192
	if info.Size() > tailBytes {
		if _, err = file.Seek(info.Size()-tailBytes, io.SeekStart); err != nil {
			return ""
		}
	}
	contents, err := io.ReadAll(io.LimitReader(file, tailBytes))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(contents), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 8 {
		lines = lines[len(lines)-8:]
	}
	for index, line := range lines {
		lines[index] = strings.Map(func(value rune) rune {
			if value < 32 && value != '\t' {
				return ' '
			}
			return value
		}, line)
	}
	return strings.TrimSpace(strings.Join(lines, " | "))
}

func validOnion(host string) bool {
	if !strings.HasSuffix(host, ".onion") || len(host) != 62 {
		return false
	}
	for _, char := range strings.TrimSuffix(host, ".onion") {
		if (char < 'a' || char > 'z') && (char < '2' || char > '7') {
			return false
		}
	}
	return true
}

func (m *Manager) monitorTorAccounting(ctx context.Context, ctrl *control.Conn, limit string) {
	maximum := parseBandwidth(limit)
	if maximum == 0 {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			values, err := ctrl.GetInfo("accounting/bytes")
			if err != nil || len(values) == 0 {
				continue
			}
			var used uint64
			for _, field := range strings.Fields(values[0].Val) {
				if _, value, ok := strings.Cut(field, "="); ok {
					number, _ := strconv.ParseUint(value, 10, 64)
					used += number
				}
			}
			if m.opts.OnTorBandwidth != nil {
				m.opts.OnTorBandwidth(used)
			}
			ratio := float64(used) / float64(maximum)
			for index, threshold := range []float64{.8, .9, .95} {
				if ratio >= threshold && !m.bandwidthWarnings[index] {
					m.bandwidthWarnings[index] = true
					log.Printf("Tor monthly bandwidth usage reached %.0f%%", threshold*100)
				}
			}
		}
	}
}

func parseBandwidth(value string) uint64 {
	parts := strings.Fields(strings.ToUpper(strings.TrimSpace(value)))
	if len(parts) != 2 {
		return 0
	}
	amount, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || amount <= 0 {
		return 0
	}
	switch parts[1] {
	case "KB", "KIB":
		return uint64(amount * 1024)
	case "MB", "MIB":
		return uint64(amount * 1024 * 1024)
	case "GB", "GIB":
		return uint64(amount * 1024 * 1024 * 1024)
	case "TB", "TIB":
		return uint64(amount * 1024 * 1024 * 1024 * 1024)
	default:
		return 0
	}
}

// HealthCheckTor verifies that the managed Tor process and hidden service remain active.
func (m *Manager) HealthCheckTor(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s := m.TorStatus()
	if !s.Enabled {
		return nil
	}
	if !s.Running {
		return fmt.Errorf("tor hidden service unavailable")
	}
	m.mu.RLock()
	cmd, done := m.cmd, m.processDone
	m.mu.RUnlock()
	if cmd == nil {
		return fmt.Errorf("tor process unavailable")
	}
	select {
	case <-done:
		return fmt.Errorf("tor process unavailable")
	default:
		return nil
	}
}

// HealthCheckI2P verifies the current I2P lifecycle state.
func (m *Manager) HealthCheckI2P(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s := m.I2PStatus()
	if !s.Enabled || s.State == "no_provider" {
		return nil
	}
	if !s.Running {
		return fmt.Errorf("I2P eepsite unavailable")
	}
	m.mu.RLock()
	address := m.i2pSAMAddress
	m.mu.RUnlock()
	if address != "" && !samReachable(ctx, address) {
		return fmt.Errorf("I2P SAM provider unavailable")
	}
	return nil
}

// Close gracefully stops the managed overlays and their private listeners.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		m.mu.RLock()
		cancel := m.runtimeCancel
		m.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		m.stopTorRuntime()
		m.stopI2PRuntime()
	})
}

func resolveBinary(explicit, name string, candidates []string) string {
	if strings.TrimSpace(explicit) != "" {
		if path, err := exec.LookPath(explicit); err == nil {
			return path
		}
		if info, err := os.Stat(explicit); err == nil && !info.IsDir() {
			return explicit
		}
		return ""
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	path, _ := exec.LookPath(name)
	return path
}

func valueOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
func valueOrString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
