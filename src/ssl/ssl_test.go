package ssl

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
	"github.com/go-acme/lego/v5/certificate"
)

func TestParseChallenge(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http-01", "http-01"},
		{"http01", "http-01"},
		{"http", "http-01"},
		{"HTTP-01", "http-01"},
		{"  http  ", "http-01"},
		{"tls-alpn-01", "tls-alpn-01"},
		{"tlsalpn01", "tls-alpn-01"},
		{"tls-alpn", "tls-alpn-01"},
		{"tls", "tls-alpn-01"},
		{"dns-01", "dns-01"},
		{"dns01", "dns-01"},
		{"dns", "dns-01"},
		// Unrecognized values fall back to the http-01 default.
		{"bogus", "http-01"},
		{"", "http-01"},
	}

	for _, c := range cases {
		if got := ParseChallenge(c.in); got != c.want {
			t.Errorf("ParseChallenge(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestChallengeServer_SetAndServe(t *testing.T) {
	cs := NewChallengeServer()
	cs.SetToken("tok123", "tok123.keyauth")

	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/tok123", nil)
	rec := httptest.NewRecorder()

	handled := cs.ServeHTTP(rec, req)
	if !handled {
		t.Fatal("ServeHTTP() = false, want true for a matching challenge path")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "tok123.keyauth" {
		t.Errorf("body = %q, want tok123.keyauth", rec.Body.String())
	}
}

// A path outside the ACME challenge prefix must be left unhandled so the
// caller's normal routing can process it.
func TestChallengeServer_IgnoresNonChallengePath(t *testing.T) {
	cs := NewChallengeServer()
	req := httptest.NewRequest(http.MethodGet, "/some/other/path", nil)
	rec := httptest.NewRecorder()

	if handled := cs.ServeHTTP(rec, req); handled {
		t.Error("ServeHTTP() = true, want false for a non-challenge path")
	}
}

// A challenge path with an unknown token must 404, and still report the
// request as handled (it owns the /.well-known/ namespace).
func TestChallengeServer_UnknownToken(t *testing.T) {
	cs := NewChallengeServer()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/unknown", nil)
	rec := httptest.NewRecorder()

	handled := cs.ServeHTTP(rec, req)
	if !handled {
		t.Error("ServeHTTP() = false, want true even for an unknown token")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ClearToken must remove a token so subsequent requests 404.
func TestChallengeServer_ClearToken(t *testing.T) {
	cs := NewChallengeServer()
	cs.SetToken("tok", "auth")
	cs.ClearToken("tok")

	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/tok", nil)
	rec := httptest.NewRecorder()
	cs.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status after ClearToken() = %d, want 404", rec.Code)
	}
}

// GetTLSConfig must return (nil, nil) when SSL is disabled, without
// touching the filesystem or network at all.
func TestGetTLSConfig_Disabled(t *testing.T) {
	m := NewManager(Config{Enabled: false})

	cfg, err := m.GetTLSConfig([]string{"example.com"})
	if err != nil {
		t.Fatalf("GetTLSConfig() unexpected error: %v", err)
	}
	if cfg != nil {
		t.Errorf("GetTLSConfig() = %+v, want nil when disabled", cfg)
	}
}

// GetTLSConfig must error out when enabled but no certificate source is
// available (no existing certs, Let's Encrypt disabled, no manual certs).
func TestGetTLSConfig_NoCertsAvailable(t *testing.T) {
	dir := testutil.TempDir(t)
	m := NewManager(Config{Enabled: true, CertPath: dir})

	_, err := m.GetTLSConfig([]string{"example.invalid"})
	if err == nil {
		t.Error("GetTLSConfig() expected error when no certificates are available, got nil")
	}
}

// findManualCerts must locate certs in the "<domain>.crt/.key" naming
// convention.
func TestFindManualCerts_CrtKeyPair(t *testing.T) {
	dir := testutil.TempDir(t)
	domain := "example.com"

	writeFixture(t, filepath.Join(dir, domain+".crt"))
	writeFixture(t, filepath.Join(dir, domain+".key"))

	m := NewManager(Config{CertPath: dir})
	cert, key := m.findManualCerts([]string{domain})
	if cert == "" || key == "" {
		t.Errorf("findManualCerts() = (%q, %q), want non-empty paths", cert, key)
	}
}

// findManualCerts must also locate certs in the fullchain/privkey naming
// convention under a per-domain subdirectory.
func TestFindManualCerts_FullchainPrivkeyPair(t *testing.T) {
	dir := testutil.TempDir(t)
	domain := "example.org"
	domainDir := filepath.Join(dir, domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("failed to create fixture dir: %v", err)
	}

	writeFixture(t, filepath.Join(domainDir, "fullchain.pem"))
	writeFixture(t, filepath.Join(domainDir, "privkey.pem"))

	m := NewManager(Config{CertPath: dir})
	cert, key := m.findManualCerts([]string{domain})
	if cert == "" || key == "" {
		t.Errorf("findManualCerts() = (%q, %q), want non-empty paths", cert, key)
	}
}

// findManualCerts must return empty strings when nothing matches, rather
// than a half-populated pair.
func TestFindManualCerts_NotFound(t *testing.T) {
	dir := testutil.TempDir(t)
	m := NewManager(Config{CertPath: dir})

	cert, key := m.findManualCerts([]string{"nope.example"})
	if cert != "" || key != "" {
		t.Errorf("findManualCerts() = (%q, %q), want empty strings", cert, key)
	}
}

// An empty CertPath must never match, guarding against accidentally
// resolving relative paths from the working directory.
func TestFindManualCerts_EmptyCertPath(t *testing.T) {
	m := NewManager(Config{CertPath: ""})
	cert, key := m.findManualCerts([]string{"example.com"})
	if cert != "" || key != "" {
		t.Errorf("findManualCerts() with empty CertPath = (%q, %q), want empty strings", cert, key)
	}
}

func writeFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fixture"), 0644); err != nil {
		t.Fatalf("failed to write fixture %q: %v", path, err)
	}
}

func TestGetTLSConfigManualCertificateAndHTTPHandler(t *testing.T) {
	dir := testutil.TempDir(t)
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "localhost.crt")
	keyPath := filepath.Join(dir, "localhost.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(Config{Enabled: true, CertPath: dir})
	tlsConfig, err := manager.GetTLSConfig([]string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig.MinVersion != 0x0303 || tlsConfig.GetCertificate == nil {
		t.Fatalf("TLS config = %+v", tlsConfig)
	}
	if _, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "localhost"}); err != nil {
		t.Fatalf("TLS certificate callback error = %v", err)
	}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	recorder := httptest.NewRecorder()
	manager.GetHTTPHandler(fallback).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("fallback handler status = %d", recorder.Code)
	}
}

func TestGetTLSConfigLetsEncryptConfiguration(t *testing.T) {
	manager := NewManager(Config{
		Enabled:     true,
		CertPath:    testutil.TempDir(t),
		LetsEncrypt: LetsEncryptConfig{Enabled: true, Email: "ops@example.test"},
	})
	tlsConfig, err := manager.GetTLSConfig([]string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig == nil || !manager.config.LetsEncrypt.Enabled {
		t.Fatal("Let's Encrypt manager was not initialized")
	}
}

func TestGetHTTPHandlerWithoutACME(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	recorder := httptest.NewRecorder()
	NewManager(Config{}).GetHTTPHandler(fallback).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("fallback handler status = %d", recorder.Code)
	}
}

func TestValidateChallengeConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		config      LetsEncryptConfig
		certPath    string
		wantErrText string
	}{
		{name: "disabled", config: LetsEncryptConfig{}},
		{name: "missing certificate directory", config: LetsEncryptConfig{Enabled: true, Email: "ops@example.test"}, wantErrText: "certificate directory"},
		{name: "invalid email", certPath: testutil.TempDir(t), config: LetsEncryptConfig{Enabled: true, Email: "not-an-email"}, wantErrText: "valid email"},
		{name: "unsupported challenge", certPath: testutil.TempDir(t), config: LetsEncryptConfig{Enabled: true, Email: "ops@example.test", Challenge: "dns-02"}, wantErrText: "unsupported ACME challenge"},
		{name: "missing DNS provider", certPath: testutil.TempDir(t), config: LetsEncryptConfig{Enabled: true, Email: "ops@example.test", Challenge: "dns-01"}, wantErrText: "dns_provider is required"},
		{name: "missing RFC2136 nameserver", certPath: testutil.TempDir(t), config: LetsEncryptConfig{Enabled: true, Email: "ops@example.test", Challenge: "dns-01", DNSProviderType: "rfc2136", DNSCredentials: map[string]string{"DNSUPDATE_TSIG_SECRET": "private-secret"}}, wantErrText: "DNSUPDATE_NAMESERVER"},
		{name: "valid unsigned RFC2136 configuration", certPath: testutil.TempDir(t), config: LetsEncryptConfig{Enabled: true, Email: "ops@example.test", Challenge: "dns-01", DNSProviderType: "rfc2136", DNSCredentials: map[string]string{"DNSUPDATE_NAMESERVER": "127.0.0.1:53"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewManager(Config{CertPath: test.certPath, LetsEncrypt: test.config})
			err := manager.Validate()
			if test.wantErrText == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
				t.Fatalf("Validate() error = %v, want text %q", err, test.wantErrText)
			}
			if strings.Contains(err.Error(), "private-secret") {
				t.Fatal("Validate() exposed a DNS credential value")
			}
		})
	}
}

func TestNormalizeDomains(t *testing.T) {
	got, err := normalizeDomains([]string{" Example.COM. ", "example.com", "api.example.com"})
	if err != nil {
		t.Fatalf("normalizeDomains() error = %v", err)
	}
	if strings.Join(got, ",") != "example.com,api.example.com" {
		t.Fatalf("normalizeDomains() = %v, want normalized unique domains", got)
	}
	for _, invalid := range [][]string{nil, {""}, {"example.com/path"}, {"example.com\\path"}, {"example.com\x00path"}} {
		if _, err := normalizeDomains(invalid); err == nil {
			t.Errorf("normalizeDomains(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestMinimumTLSVersion(t *testing.T) {
	for _, test := range []struct {
		value string
		want  uint16
	}{
		{value: "", want: tls.VersionTLS12},
		{value: "TLS1.3", want: tls.VersionTLS13},
		{value: " tls 1.3 ", want: tls.VersionTLS13},
		{value: "1.3", want: tls.VersionTLS13},
		{value: "TLS1.1", want: tls.VersionTLS12},
	} {
		manager := NewManager(Config{MinVersion: test.value})
		if got := manager.minimumTLSVersion(); got != test.want {
			t.Errorf("minimumTLSVersion(%q) = %#x, want %#x", test.value, got, test.want)
		}
	}
}

func TestHTTPChallengeHandlerContract(t *testing.T) {
	manager := NewManager(Config{})
	provider := httpChallengeProvider{manager: manager}
	if err := provider.Present(context.Background(), "example.test", "token", "token.answer"); err != nil {
		t.Fatal(err)
	}
	fallbackCalls := 0
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls++
		w.WriteHeader(http.StatusAccepted)
	})
	handler := manager.GetHTTPHandler(fallback)
	for _, test := range []struct {
		method string
		path   string
		status int
		body   string
	}{
		{method: http.MethodGet, path: "/.well-known/acme-challenge/token", status: http.StatusOK, body: "token.answer"},
		{method: http.MethodHead, path: "/.well-known/acme-challenge/token", status: http.StatusOK},
		{method: http.MethodGet, path: "/.well-known/acme-challenge/missing", status: http.StatusNotFound, body: "404 page not found\n"},
		{method: http.MethodGet, path: "/.well-known/acme-challenge/token/extra", status: http.StatusNotFound, body: "404 page not found\n"},
		{method: http.MethodPost, path: "/.well-known/acme-challenge/token", status: http.StatusMethodNotAllowed, body: "Method Not Allowed\n"},
		{method: http.MethodGet, path: "/ordinary", status: http.StatusAccepted},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
		if recorder.Code != test.status || recorder.Body.String() != test.body {
			t.Errorf("%s %s = (%d, %q), want (%d, %q)", test.method, test.path, recorder.Code, recorder.Body.String(), test.status, test.body)
		}
		if test.path == "/.well-known/acme-challenge/token" && (test.method == http.MethodGet || test.method == http.MethodHead) {
			if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
				t.Errorf("challenge Content-Type = %q", got)
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("challenge Cache-Control = %q", got)
			}
		}
		if test.method == http.MethodPost && recorder.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("challenge Allow header = %q", recorder.Header().Get("Allow"))
		}
	}
	if fallbackCalls != 1 {
		t.Errorf("fallback called %d times, want once for non-challenge route", fallbackCalls)
	}
	if err := provider.CleanUp(context.Background(), "example.test", "token", "token.answer"); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/token", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("cleaned challenge status = %d, want 404", recorder.Code)
	}
}

func TestTLSALPNChallengeLifecycle(t *testing.T) {
	directory := testutil.TempDir(t)
	certPath := filepath.Join(directory, "example.test.crt")
	keyPath := filepath.Join(directory, "example.test.key")
	writeTLSCertificate(t, certPath, keyPath, []string{"example.test"}, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	manager := NewManager(Config{Enabled: true, CertPath: directory})
	tlsConfig, err := manager.GetTLSConfig([]string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	provider := tlsALPNChallengeProvider{manager: manager}
	if err := provider.Present(context.Background(), "example.test", "token", "test-key-authorization"); err != nil {
		t.Fatalf("Present() error = %v", err)
	}
	challengeCert, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.test", SupportedProtos: []string{tlsALPNProtocol}})
	if err != nil {
		t.Fatalf("TLS-ALPN certificate callback error = %v", err)
	}
	if len(challengeCert.Certificate) == 0 {
		t.Fatal("TLS-ALPN callback returned no certificate chain")
	}
	challengeLeaf, err := x509.ParseCertificate(challengeCert.Certificate[0])
	if err != nil {
		t.Fatalf("parse TLS-ALPN challenge certificate: %v", err)
	}
	foundExtension := false
	for _, extension := range challengeLeaf.Extensions {
		if extension.Id.String() == "1.3.6.1.5.5.7.1.31" {
			foundExtension = true
		}
	}
	if !foundExtension {
		t.Fatal("TLS-ALPN challenge certificate has no acmeIdentifier extension")
	}
	if err := provider.CleanUp(context.Background(), "EXAMPLE.TEST", "token", ""); err != nil {
		t.Fatal(err)
	}
	ordinaryCert, err := tlsConfig.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.test", SupportedProtos: []string{tlsALPNProtocol}})
	if err != nil {
		t.Fatalf("regular certificate callback after cleanup error = %v", err)
	}
	if ordinaryCert.Leaf == nil || ordinaryCert.Leaf.DNSNames[0] != "example.test" {
		t.Fatal("cleanup did not restore the configured host certificate")
	}
}

func TestLoadOrCreateAccountAndPrivateFilePermissions(t *testing.T) {
	root := testutil.TempDir(t)
	manager := NewManager(Config{CertPath: root, LetsEncrypt: LetsEncryptConfig{Staging: true}})
	directory, err := manager.accountDirectory()
	if err != nil {
		t.Fatalf("accountDirectory() error = %v", err)
	}
	if !strings.HasSuffix(directory, filepath.Join("accounts", "staging")) {
		t.Fatalf("staging account directory = %q", directory)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("stat staging account directory: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("staging account directory mode = %v, want 0700", info.Mode().Perm())
	}
	account, err := loadOrCreateAccount(directory, "ops@example.test")
	if err != nil {
		t.Fatalf("loadOrCreateAccount() error = %v", err)
	}
	if account.GetEmail() != "ops@example.test" || account.GetPrivateKey() == nil {
		t.Fatalf("new account = %+v, expected email and signing key", account)
	}
	keyPath := filepath.Join(directory, "account.key")
	info, err = os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat account key: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("account key mode = %v, want 0600", info.Mode().Perm())
	}
	if err := saveAccount(directory, account); err != nil {
		t.Fatalf("saveAccount() error = %v", err)
	}
	reloaded, err := loadOrCreateAccount(directory, "new@example.test")
	if err != nil {
		t.Fatalf("loadOrCreateAccount() reload error = %v", err)
	}
	if reloaded.GetEmail() != "new@example.test" || reloaded.GetPrivateKey() == nil {
		t.Fatalf("reloaded account = %+v, want configured email and persisted key", reloaded)
	}
	accountPath := filepath.Join(directory, "account.json")
	info, err = os.Stat(accountPath)
	if err != nil {
		t.Fatalf("stat account metadata: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("account metadata mode = %v, want 0600", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(directory, "account.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateAccount(directory, "ops@example.test"); err == nil {
		t.Fatal("loadOrCreateAccount() accepted invalid account metadata")
	}
}

func TestSaveManagedCertificateAndRenewalSelection(t *testing.T) {
	root := testutil.TempDir(t)
	certPath := filepath.Join(root, "example.test.crt")
	keyPath := filepath.Join(root, "example.test.key")
	writeTLSCertificate(t, certPath, keyPath, []string{"example.test"}, time.Now().Add(-time.Hour), time.Now().Add(14*24*time.Hour))
	certificatePEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveManagedCertificate(root, "example.test", &certificate.Resource{Certificate: certificatePEM, PrivateKey: privateKeyPEM}); err != nil {
		t.Fatalf("saveManagedCertificate() error = %v", err)
	}
	managedDirectory := filepath.Join(root, "letsencrypt", "example.test")
	for _, name := range []string{"fullchain.pem", "privkey.pem"} {
		info, err := os.Stat(filepath.Join(managedDirectory, name))
		if err != nil {
			t.Errorf("stat managed %s: %v", name, err)
			continue
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("managed %s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}
	info, err := os.Stat(managedDirectory)
	if err != nil {
		t.Fatalf("stat managed certificate directory: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("managed certificate directory mode = %v, want 0700", info.Mode().Perm())
	}
	manager := NewManager(Config{Enabled: true, CertPath: root, LetsEncrypt: LetsEncryptConfig{Enabled: true, Email: "ops@example.test"}})
	if _, err := manager.GetTLSConfig([]string{"example.test"}); err != nil {
		t.Fatalf("GetTLSConfig() managed certificate error = %v", err)
	}
	if err := manager.RenewManagedCertificate(context.Background(), []string{"example.test"}); err != nil {
		t.Fatalf("RenewManagedCertificate() renewed a certificate with more than seven days remaining: %v", err)
	}
}

func writeTLSCertificate(t *testing.T, certPath, keyPath string, dnsNames []string, notBefore, notAfter time.Time) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: notBefore, NotAfter: notAfter,
		DNSNames: dnsNames, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
}
