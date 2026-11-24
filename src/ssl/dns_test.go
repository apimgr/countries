package ssl

import (
	"context"
	"encoding/base64"
	"errors"
	"github.com/apimgr/countries/src/testutil"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const (
	rfc2136Zone = "example.test."
	rfc2136Key  = "acme.example.test."
)

type dnsUpdateObservation struct {
	mu       sync.Mutex
	inserted []string
	removed  []string
}

func (o *dnsUpdateObservation) handler(w dns.ResponseWriter, request *dns.Msg) {
	if request.Opcode == dns.OpcodeUpdate {
		if err := w.TsigStatus(); err != nil {
			response := new(dns.Msg)
			response.SetRcode(request, dns.RcodeNotAuth)
			if signature := request.IsTsig(); signature != nil {
				response.SetTsig(signature.Hdr.Name, signature.Algorithm, 0, time.Now().Unix())
				response.IsTsig().Error = dns.RcodeBadSig
			}
			_ = w.WriteMsg(response)
			return
		}
		o.mu.Lock()
		for _, record := range request.Ns {
			txt, ok := record.(*dns.TXT)
			if !ok || dns.CanonicalName(txt.Hdr.Name) != "_acme-challenge."+rfc2136Zone {
				continue
			}
			switch txt.Hdr.Class {
			case dns.ClassINET:
				o.inserted = append(o.inserted, strings.Join(txt.Txt, ""))
			case dns.ClassNONE:
				o.removed = append(o.removed, strings.Join(txt.Txt, ""))
			}
		}
		o.mu.Unlock()
	}
	response := new(dns.Msg)
	response.SetReply(request)
	_ = w.WriteMsg(response)
}

func startRFC2136TestServer(t *testing.T, key, secret string) (string, *dnsUpdateObservation) {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for in-process DNS server: %v", err)
	}
	observation := &dnsUpdateObservation{}
	server := &dns.Server{
		PacketConn: packetConn,
		Handler:    dns.HandlerFunc(observation.handler),
		MsgAcceptFunc: func(header dns.Header) dns.MsgAcceptAction {
			if int(header.Bits>>11)&0x0f == dns.OpcodeUpdate {
				return dns.MsgAccept
			}
			return dns.DefaultMsgAcceptFunc(header)
		},
		TsigSecret: map[string]string{key: secret},
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	return packetConn.LocalAddr().String(), observation
}

func rfc2136TestProvider(t *testing.T, providerName, prefix, nameserver, secret string) (*Manager, interface {
	Present(context.Context, string, string, string) error
	CleanUp(context.Context, string, string, string) error
}) {
	t.Helper()
	credentials := map[string]string{
		prefix + "NAMESERVER": nameserver,
		prefix + "ZONES":      rfc2136Zone,
	}
	if secret != "" {
		credentials[prefix+"TSIG_KEY"] = rfc2136Key
		credentials[prefix+"TSIG_SECRET"] = secret
		credentials[prefix+"TSIG_ALGORITHM"] = "hmac-sha256."
	}
	manager := NewManager(Config{
		CertPath: testutil.TempDir(t),
		LetsEncrypt: LetsEncryptConfig{
			DNSProviderType: providerName,
			DNSCredentials:  credentials,
		},
	})
	provider, err := manager.newDNSChallengeProvider()
	if err != nil {
		t.Fatalf("construct %s DNS provider: %v", providerName, err)
	}
	challengeProvider, ok := provider.(interface {
		Present(context.Context, string, string, string) error
		CleanUp(context.Context, string, string, string) error
	})
	if !ok {
		t.Fatal("DNS provider does not implement the challenge lifecycle")
	}
	return manager, challengeProvider
}

func TestRFC2136SignedUpdateInsertAndExactCleanup(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("test-tsig-secret"))
	for _, providerConfiguration := range []struct {
		name   string
		prefix string
	}{
		{name: "dnsupdate", prefix: "DNSUPDATE_"},
		{name: "rfc2136", prefix: "RFC2136_"},
	} {
		t.Run(providerConfiguration.prefix, func(t *testing.T) {
			nameserver, observation := startRFC2136TestServer(t, rfc2136Key, secret)
			_, provider := rfc2136TestProvider(t, providerConfiguration.name, providerConfiguration.prefix, nameserver, secret)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := provider.Present(ctx, "example.test", "token", "key-authorization"); err != nil {
				t.Fatalf("signed RFC2136 INSERT failed: %v", err)
			}
			if err := provider.CleanUp(ctx, "example.test", "token", "key-authorization"); err != nil {
				t.Fatalf("signed RFC2136 REMOVE failed: %v", err)
			}
			observation.mu.Lock()
			defer observation.mu.Unlock()
			if len(observation.inserted) != 1 || len(observation.removed) != 1 {
				t.Fatalf("observed TXT updates = INSERT %v, REMOVE %v", observation.inserted, observation.removed)
			}
			if observation.inserted[0] == "" || observation.inserted[0] != observation.removed[0] {
				t.Fatalf("REMOVE value %q did not match the inserted challenge value %q", observation.removed[0], observation.inserted[0])
			}
		})
	}
}

func TestRFC2136WrongTSIGKeyIsRejected(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("right-tsig-secret"))
	nameserver, _ := startRFC2136TestServer(t, rfc2136Key, secret)
	_, provider := rfc2136TestProvider(t, "dnsupdate", "DNSUPDATE_", nameserver,
		base64.StdEncoding.EncodeToString([]byte("wrong-tsig-secret")))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := provider.Present(ctx, "example.test", "token", "key-authorization")
	if err == nil {
		t.Fatal("signed update with the wrong TSIG secret unexpectedly succeeded")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "BADSIG") {
		t.Fatalf("wrong TSIG secret error = %v, want BADSIG", err)
	}
}

func TestRFC2136UnsignedUpdate(t *testing.T) {
	nameserver, observation := startRFC2136TestServer(t, rfc2136Key, "")
	_, provider := rfc2136TestProvider(t, "dnsupdate", "DNSUPDATE_", nameserver, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Present(ctx, "example.test", "token", "key-authorization"); err != nil {
		t.Fatalf("unsigned RFC2136 INSERT failed: %v", err)
	}
	if err := provider.CleanUp(ctx, "example.test", "token", "key-authorization"); err != nil {
		t.Fatalf("unsigned RFC2136 REMOVE failed: %v", err)
	}
	observation.mu.Lock()
	defer observation.mu.Unlock()
	if len(observation.inserted) != 1 || len(observation.removed) != 1 {
		t.Fatalf("observed unsigned TXT updates = INSERT %v, REMOVE %v", observation.inserted, observation.removed)
	}
}

func TestRFC2136PortDefault(t *testing.T) {
	for input, expected := range map[string]string{
		"ns1.example.test": "ns1.example.test:53",
		"::1":              "[::1]:53",
		"[::1]":            "[::1]:53",
	} {
		actual, err := normalizeRFC2136Nameserver(input)
		if err != nil || actual != expected {
			t.Errorf("normalizeRFC2136Nameserver(%q) = %q, %v; want %q", input, actual, err, expected)
		}
	}
	if _, err := normalizeRFC2136Nameserver("ns1.example.test:invalid"); err == nil {
		t.Error("invalid nameserver port was accepted")
	}
}

func TestRFC2136WrongTSIGErrorDoesNotExposeSecret(t *testing.T) {
	secret := "super-secret-tsig-material"
	err := errors.New("BADSIG: " + secret)
	if strings.Contains(sanitizeProviderError(err, map[string]string{"DNSUPDATE_TSIG_SECRET": secret}).Error(), secret) {
		t.Fatal("sanitized RFC2136 error exposed the TSIG secret")
	}
}
