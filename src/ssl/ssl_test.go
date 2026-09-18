package ssl

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	dir := t.TempDir()
	m := NewManager(Config{Enabled: true, CertPath: dir})

	_, err := m.GetTLSConfig([]string{"example.invalid"})
	if err == nil {
		t.Error("GetTLSConfig() expected error when no certificates are available, got nil")
	}
}

// findManualCerts must locate certs in the "<domain>.crt/.key" naming
// convention.
func TestFindManualCerts_CrtKeyPair(t *testing.T) {
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
