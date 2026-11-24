package overlay

import (
	"bufio"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("persisted key mode = %v, err = %v", info.Mode().Perm(), err)
	}
	persisted, err := loadOrCreateI2PKey(nil, nil, keyPath, 7)
	if err != nil || persisted != encodedKey {
		t.Fatalf("loadOrCreateI2PKey(persisted) = %q, %v", persisted, err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid-key")
	if err := os.WriteFile(invalidPath, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateI2PKey(nil, nil, invalidPath, 7); err == nil {
		t.Fatal("loadOrCreateI2PKey accepted an invalid persisted destination")
	}
	if err := os.WriteFile(invalidPath, []byte("not a key"), 0600); err != nil {
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
