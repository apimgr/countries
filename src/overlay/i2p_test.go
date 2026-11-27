package overlay

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
)

func TestSAMCommandParsesValuesAndRejectsBadReply(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		wantErr  bool
	}{
		{name: "success", response: "RESULT=OK MESSAGE=ready\n"},
		{name: "failure", response: "RESULT=I2P_ERROR\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, peer := net.Pipe()
			closePipe(t, client)
			closePipe(t, peer)
			go func() {
				_, _ = bufio.NewReader(peer).ReadString('\n')
				_, _ = io.WriteString(peer, test.response)
			}()
			err := ensureSAMReply(client, bufio.NewReader(client), time.Second, "SESSION CREATE ID=%s", "test-session")
			if (err != nil) != test.wantErr {
				t.Fatalf("ensureSAMReply() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}

	client, peer := net.Pipe()
	closePipe(t, client)
	closePipe(t, peer)
	go func() {
		_, _ = bufio.NewReader(peer).ReadString('\n')
		_, _ = io.WriteString(peer, "RESULT=OK VERSION=3.3 MESSAGE=ready\n")
	}()
	values, err := samCommand(client, bufio.NewReader(client), time.Second, "HELLO VERSION MIN=%d", 3)
	if err != nil || values["RESULT"] != "OK" || values["VERSION"] != "3.3" || values["MESSAGE"] != "ready" {
		t.Fatalf("samCommand() = %#v, %v", values, err)
	}
}

func TestI2PBase64UsesAlternateAlphabetAndRoundTrips(t *testing.T) {
	input := []byte{0xfb, 0xef, 0xff}
	encoded := i2pBase64.EncodeToString(input)
	if encoded != "--~~" {
		t.Fatalf("I2P base64 = %q, want alternate alphabet encoding --~~", encoded)
	}
	decoded, err := i2pBase64.DecodeString(encoded)
	if err != nil || !bytes.Equal(decoded, input) {
		t.Fatalf("I2P base64 round trip = %x, %v", decoded, err)
	}
}

func TestI2PKeyGenerationPersistenceAndValidation(t *testing.T) {
	encodedKey := i2pBase64.EncodeToString(make([]byte, 387))
	client, peer := net.Pipe()
	closePipe(t, client)
	closePipe(t, peer)
	keyPath := filepath.Join(t.TempDir(), "site", "site-keys.dat")
	go func() {
		command, _ := bufio.NewReader(peer).ReadString('\n')
		if strings.TrimSpace(command) == "DEST GENERATE SIGNATURE_TYPE=7" {
			_, _ = io.WriteString(peer, "RESULT=OK PRIV="+encodedKey+" PUB=public-key\n")
		} else {
			_, _ = io.WriteString(peer, "RESULT=I2P_ERROR\n")
		}
	}()
	created, err := loadOrCreateI2PKey(client, bufio.NewReader(client), keyPath, 0)
	if err != nil || created != encodedKey {
		t.Fatalf("loadOrCreateI2PKey(generated) = %q, %v", created, err)
	}
	info, err := os.Stat(keyPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("persisted key mode = %v, err = %v", info.Mode().Perm(), err)
	}
	persisted, err := loadOrCreateI2PKey(nil, nil, keyPath, 7)
	if err != nil || persisted != encodedKey {
		t.Fatalf("loadOrCreateI2PKey(persisted) = %q, %v", persisted, err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid-key")
	if err := os.WriteFile(invalidPath, []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateI2PKey(nil, nil, invalidPath, 7); err == nil {
		t.Fatal("loadOrCreateI2PKey accepted an invalid persisted destination")
	}
	if err := os.WriteFile(invalidPath, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, peer = net.Pipe()
	closePipe(t, client)
	closePipe(t, peer)
	go func() {
		_, _ = bufio.NewReader(peer).ReadString('\n')
		_, _ = io.WriteString(peer, "RESULT=OK PRIV=bad\n")
	}()
	if _, err := loadOrCreateI2PKey(client, bufio.NewReader(client), invalidPath, 7); err == nil {
		t.Fatal("loadOrCreateI2PKey accepted an invalid key returned by SAM")
	}
}

func TestFakeSAMLifecycleUsesPersistentKeyAndSecondSilentForwardSocket(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Errorf("close fake SAM listener: %v", err)
		}
	})
	commands := make(chan string, 32)
	var mu sync.Mutex
	accepted := 0
	sessionCreates := 0
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			accepted++
			connectionID := accepted
			mu.Unlock()
			go func() {
				defer func() { _ = conn.Close() }()
				reader := bufio.NewReader(conn)
				line, readErr := reader.ReadString('\n')
				if readErr != nil || !strings.HasPrefix(line, "HELLO VERSION ") {
					return
				}
				_, _ = io.WriteString(conn, "HELLO REPLY RESULT=OK VERSION=3.3\n")
				for {
					line, readErr = reader.ReadString('\n')
					if readErr != nil {
						return
					}
					command := strings.TrimSpace(line)
					select {
					case commands <- fmt.Sprintf("%d:%s", connectionID, command):
					default:
					}
					switch {
					case command == "QUIT":
						_, _ = io.WriteString(conn, "QUIT REPLY RESULT=OK\n")
						return
					case strings.HasPrefix(command, "DEST GENERATE "):
						key := i2pBase64.EncodeToString(make([]byte, 387))
						_, _ = fmt.Fprintf(conn, "DEST REPLY RESULT=OK PRIV=%s PUB=public\n", key)
					case strings.HasPrefix(command, "SESSION CREATE "):
						time.Sleep(50 * time.Millisecond)
						mu.Lock()
						sessionCreates++
						createIndex := sessionCreates
						mu.Unlock()
						if createIndex == 1 {
							_, _ = io.WriteString(conn, "SESSION STATUS RESULT=DUPLICATED_ID\n")
						} else {
							_, _ = io.WriteString(conn, "SESSION STATUS RESULT=OK\n")
						}
					case strings.HasPrefix(command, "STREAM FORWARD "):
						_, _ = io.WriteString(conn, "STREAM STATUS RESULT=OK\n")
					default:
						_, _ = io.WriteString(conn, "RESULT=I2P_ERROR\n")
					}
				}
			}()
		}
	}()

	oldOutput := log.Writer()
	var logOutput bytes.Buffer
	log.SetOutput(&logOutput)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	dataRoot := filepath.Join(t.TempDir(), "data")
	var firstAddress string
	for run := 0; run < 2; run++ {
		cfg := config.DefaultConfig()
		cfg.Server.I2P.Enabled = true
		cfg.Server.I2P.Binary = filepath.Join(t.TempDir(), "missing-i2pd")
		cfg.Server.I2P.SAMAddress = listener.Addr().String()
		cfg.Server.I2P.BootstrapTimeout = 60
		manager := NewManager(Options{Config: *cfg, ConfigDir: filepath.Join(t.TempDir(), "config"), DataDir: dataRoot, LogsDir: filepath.Join(t.TempDir(), "logs")})
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan struct{})
		go func() {
			manager.startI2P(ctx)
			close(finished)
		}()
		deadline := time.After(3 * time.Second)
		for manager.I2PStatus().Status != "healthy" {
			select {
			case <-deadline:
				cancel()
				manager.Close()
				t.Fatalf("fake SAM startup did not become healthy: %+v", manager.I2PStatus())
			case <-time.After(5 * time.Millisecond):
			}
		}
		address := manager.I2PStatus().Hostname
		if run == 0 {
			firstAddress = address
		} else if address != firstAddress {
			t.Fatalf("persisted I2P address changed: first=%q second=%q", firstAddress, address)
		}
		cancel()
		manager.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("fake SAM session did not stop after cancellation")
		}
	}
	if strings.Contains(logOutput.String(), "PRIV=") {
		t.Fatal("I2P private destination key appeared in application logs")
	}
	mu.Lock()
	gotConnections, gotSessionCreates := accepted, sessionCreates
	mu.Unlock()
	if gotConnections != 6 {
		t.Errorf("SAM connections = %d, want 6 (probe, control, forward for each start)", gotConnections)
	}
	if gotSessionCreates != 3 {
		t.Errorf("SESSION CREATE commands = %d, want 3 (one retry, then one reuse)", gotSessionCreates)
	}
	seenForwardConnections := map[string]bool{}
	seenSilentForward := false
	seenDestinationGenerate := 0
	for len(commands) > 0 {
		entry := <-commands
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.HasPrefix(parts[1], "DEST GENERATE ") {
			seenDestinationGenerate++
		}
		if strings.HasPrefix(parts[1], "STREAM FORWARD ") {
			seenForwardConnections[parts[0]] = true
			seenSilentForward = seenSilentForward || strings.Contains(parts[1], "SILENT=true")
		}
	}
	if seenDestinationGenerate != 1 {
		t.Errorf("DEST GENERATE commands = %d, want 1 for a reused key", seenDestinationGenerate)
	}
	if len(seenForwardConnections) != 2 || !seenSilentForward {
		t.Errorf("forward sockets = %v, silent forwarding = %t; want a separate socket per run with SILENT=true", seenForwardConnections, seenSilentForward)
	}
}

func TestI2PDConfigDisablesRouterAndProxyListeners(t *testing.T) {
	configText := renderI2PDConfig(12345, "i2pd.log")
	for _, required := range []string{"notransit = true", "floodfill = false", "[sam]\nenabled = true\naddress = 127.0.0.1\nport = 12345", "[httpproxy]\nenabled = false", "[socksproxy]\nenabled = false", "[bob]\nenabled = false", "[upnp]\nenabled = false"} {
		if !strings.Contains(configText, required) {
			t.Errorf("i2pd config missing %q", required)
		}
	}
	for _, forbiddenPort := range []string{"7070", "4444", "4447", "7654"} {
		if strings.Contains(configText, forbiddenPort) {
			t.Errorf("i2pd config contains forbidden listener port %s", forbiddenPort)
		}
	}
}

func TestControlConnectionClosedSignal(t *testing.T) {
	client, peer := net.Pipe()
	closePipe(t, client)
	closePipe(t, peer)
	done := controlConnClosed(client)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("control connection close was not detected")
	}
}

func closePipe(t *testing.T, conn net.Conn) {
	t.Helper()
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close test pipe: %v", err)
		}
	})
}
