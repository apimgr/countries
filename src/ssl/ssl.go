// Package ssl configures TLS and automatic certificate management.
package ssl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/tlsalpn01"
)

const tlsALPNProtocol = "acme-tls/1"

// Config contains listener and certificate-management settings.
type Config struct {
	Enabled     bool
	CertPath    string
	Cert        string
	Key         string
	MinVersion  string
	LetsEncrypt LetsEncryptConfig
}

// LetsEncryptConfig contains ACME registration and challenge settings.
type LetsEncryptConfig struct {
	Enabled         bool
	Email           string
	Challenge       string
	Staging         bool
	DNSProviderType string
	DNSCredentials  map[string]string
}

// Manager handles certificate selection, ACME challenges, issuance, and renewal.
type Manager struct {
	config      Config
	mu          sync.RWMutex
	issueMu     sync.Mutex
	httpTokens  map[string]string
	tlsTokens   map[string]*tls.Certificate
	allowedHost map[string]struct{}
	selected    certificateFiles
	issued      func()
}

type certificateFiles struct {
	certificate string
	privateKey  string
	managed     bool
}

// NewManager creates a certificate manager.
func NewManager(cfg Config) *Manager {
	if cfg.CertPath != "" {
		cfg.CertPath = filepath.Clean(cfg.CertPath)
	}
	return &Manager{
		config:      cfg,
		httpTokens:  make(map[string]string),
		tlsTokens:   make(map[string]*tls.Certificate),
		allowedHost: make(map[string]struct{}),
	}
}

// Validate checks the enabled Let's Encrypt settings without contacting the
// certificate authority or changing DNS records.
func (m *Manager) Validate() error {
	if !m.config.LetsEncrypt.Enabled {
		return nil
	}
	return m.validateChallengeConfiguration()
}

// SetCertificateIssuedCallback registers a callback invoked after a DNS-01
// issuance or renewal succeeds.
func (m *Manager) SetCertificateIssuedCallback(callback func()) {
	m.mu.Lock()
	m.issued = callback
	m.mu.Unlock()
}

// GetTLSConfig returns a TLS configuration suitable for an HTTPS listener.
// The certificate callback re-reads the selected keypair, so an ACME renewal
// is available to new connections without restarting the listener.
func (m *Manager) GetTLSConfig(domains []string) (*tls.Config, error) {
	if !m.config.Enabled {
		return nil, nil
	}
	hosts, err := normalizeDomains(domains)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.allowedHost = make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		m.allowedHost[strings.ToLower(host)] = struct{}{}
	}
	m.mu.Unlock()

	if err := m.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.selected = certificateFiles{}
	m.mu.Unlock()
	for _, files := range m.certificateCandidates(hosts) {
		if _, err := m.loadCertificate(files, hosts); err == nil {
			m.mu.Lock()
			m.selected = files
			m.mu.Unlock()
			break
		}
	}
	m.mu.RLock()
	selected := m.selected
	m.mu.RUnlock()
	if selected.certificate == "" && !m.config.LetsEncrypt.Enabled {
		return nil, fmt.Errorf("no valid certificate is available and Let's Encrypt is disabled")
	}

	return &tls.Config{
		MinVersion: m.minimumTLSVersion(),
		NextProtos: []string{"h2", "http/1.1", tlsALPNProtocol},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if hello.ServerName != "" {
				m.mu.RLock()
				challengeCert := m.tlsTokens[strings.ToLower(hello.ServerName)]
				_, allowed := m.allowedHost[strings.ToLower(hello.ServerName)]
				m.mu.RUnlock()
				if challengeCert != nil && hasProtocol(hello.SupportedProtos, tlsALPNProtocol) {
					return challengeCert, nil
				}
				if !allowed {
					return nil, fmt.Errorf("TLS host is not configured")
				}
			}
			m.mu.RLock()
			files := m.selected
			m.mu.RUnlock()
			if files.certificate == "" {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				if err := m.EnsureCertificate(ctx, hosts); err != nil {
					return nil, err
				}
			}
			m.mu.RLock()
			files = m.selected
			m.mu.RUnlock()
			cert, err := m.loadCertificate(files, hosts)
			if err != nil {
				return nil, err
			}
			return cert, nil
		},
	}, nil
}

// GetHTTPHandler serves active ACME HTTP-01 tokens before invoking fallback.
func (m *Manager) GetHTTPHandler(fallback http.Handler) http.Handler {
	if fallback == nil {
		fallback = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/.well-known/acme-challenge/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			fallback.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, prefix)
		if token == "" || strings.Contains(token, "/") {
			http.NotFound(w, r)
			return
		}
		m.mu.RLock()
		value, ok := m.httpTokens[token]
		m.mu.RUnlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(value))
		}
	})
}

// EnsureCertificate obtains a certificate when no valid certificate exists.
// Call this after the HTTP challenge handler and HTTPS listener are accepting
// traffic when using HTTP-01 or TLS-ALPN-01.
func (m *Manager) EnsureCertificate(ctx context.Context, domains []string) error {
	if !m.config.Enabled || !m.config.LetsEncrypt.Enabled {
		return fmt.Errorf("certificate management is disabled")
	}
	hosts, err := normalizeDomains(domains)
	if err != nil {
		return err
	}
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	for _, files := range m.certificateCandidates(hosts) {
		if _, err := m.loadCertificate(files, hosts); err == nil {
			m.mu.Lock()
			m.selected = files
			m.mu.Unlock()
			return nil
		}
	}
	return m.obtainCertificate(ctx, hosts)
}

// RenewManagedCertificate renews the app-managed certificate once it is
// within 30 days of expiration. System and user-managed certificates are
// deliberately left untouched.
func (m *Manager) RenewManagedCertificate(ctx context.Context, domains []string) error {
	if !m.config.Enabled || !m.config.LetsEncrypt.Enabled {
		return nil
	}
	hosts, err := normalizeDomains(domains)
	if err != nil {
		return err
	}
	m.mu.RLock()
	files := m.selected
	m.mu.RUnlock()
	if files.certificate == "" {
		for _, candidate := range m.certificateCandidates(hosts) {
			if _, err := m.loadCertificate(candidate, hosts); err == nil {
				files = candidate
				break
			}
		}
	}
	if !files.managed || files.certificate == "" {
		return nil
	}
	cert, err := m.loadCertificate(files, hosts)
	if err != nil {
		return err
	}
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("managed certificate has no certificate chain")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("parse managed certificate: %w", err)
	}
	if time.Until(leaf.NotAfter) > 30*24*time.Hour {
		return nil
	}
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	return m.obtainCertificate(ctx, hosts)
}

func (m *Manager) minimumTLSVersion() uint16 {
	switch strings.ToUpper(strings.TrimSpace(m.config.MinVersion)) {
	case "TLS1.3", "TLS 1.3", "1.3":
		return tls.VersionTLS13
	default:
		return tls.VersionTLS12
	}
}

func (m *Manager) certificateCandidates(domains []string) []certificateFiles {
	var candidates []certificateFiles
	add := func(cert, key string, managed bool) {
		if fileExists(cert) && fileExists(key) {
			candidates = append(candidates, certificateFiles{certificate: cert, privateKey: key, managed: managed})
		}
	}
	for _, domain := range domains {
		add(filepath.Join("/etc/letsencrypt/live/domain", "fullchain.pem"), filepath.Join("/etc/letsencrypt/live/domain", "privkey.pem"), false)
		add(filepath.Join("/etc/letsencrypt/live", domain, "fullchain.pem"), filepath.Join("/etc/letsencrypt/live", domain, "privkey.pem"), false)
		if m.config.CertPath != "" {
			managedDir := filepath.Join(m.config.CertPath, "letsencrypt", domain)
			add(filepath.Join(managedDir, "fullchain.pem"), filepath.Join(managedDir, "privkey.pem"), true)
			localDir := filepath.Join(m.config.CertPath, "local", domain)
			add(filepath.Join(localDir, "fullchain.pem"), filepath.Join(localDir, "privkey.pem"), false)
			add(filepath.Join(localDir, "cert.pem"), filepath.Join(localDir, "key.pem"), false)
			add(filepath.Join(m.config.CertPath, domain+".crt"), filepath.Join(m.config.CertPath, domain+".key"), false)
			add(filepath.Join(m.config.CertPath, domain, "fullchain.pem"), filepath.Join(m.config.CertPath, domain, "privkey.pem"), false)
		}
	}
	if m.config.Cert != "" && m.config.Key != "" {
		add(m.config.Cert, m.config.Key, false)
	}
	return candidates
}

func (m *Manager) findManualCerts(domains []string) (string, string) {
	if m.config.CertPath == "" {
		return "", ""
	}
	for _, domain := range domains {
		for _, pair := range [][2]string{
			{filepath.Join(m.config.CertPath, domain+".crt"), filepath.Join(m.config.CertPath, domain+".key")},
			{filepath.Join(m.config.CertPath, domain, "fullchain.pem"), filepath.Join(m.config.CertPath, domain, "privkey.pem")},
		} {
			if fileExists(pair[0]) && fileExists(pair[1]) {
				return pair[0], pair[1]
			}
		}
	}
	return "", ""
}

func (m *Manager) loadCertificate(files certificateFiles, domains []string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(files.certificate, files.privateKey)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 0 {
		return nil, fmt.Errorf("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	if time.Now().After(leaf.NotAfter) || time.Now().Before(leaf.NotBefore) {
		return nil, fmt.Errorf("certificate is outside its validity period")
	}
	for _, domain := range domains {
		if !certificateMatchesDomain(leaf, domain) {
			return nil, fmt.Errorf("certificate does not match configured host %q", domain)
		}
	}
	cert.Leaf = leaf
	return &cert, nil
}

func certificateMatchesDomain(certificate *x509.Certificate, domain string) bool {
	if strings.HasPrefix(domain, "*.") {
		for _, name := range certificate.DNSNames {
			if strings.EqualFold(name, domain) {
				return true
			}
		}
		return strings.EqualFold(certificate.Subject.CommonName, domain)
	}
	if err := certificate.VerifyHostname(domain); err == nil {
		return true
	}
	return strings.EqualFold(certificate.Subject.CommonName, domain)
}

func normalizeDomains(domains []string) ([]string, error) {
	seen := make(map[string]struct{}, len(domains))
	result := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" || strings.ContainsAny(domain, "/\\\x00") {
			return nil, fmt.Errorf("invalid TLS domain")
		}
		if _, ok := seen[domain]; !ok {
			seen[domain] = struct{}{}
			result = append(result, domain)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one TLS domain is required")
	}
	return result, nil
}

func hasProtocol(protocols []string, expected string) bool {
	for _, protocol := range protocols {
		if protocol == expected {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ParseChallenge canonicalizes supported ACME challenge names.
func ParseChallenge(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tls-alpn-01", "tlsalpn01", "tls-alpn", "tls":
		return "tls-alpn-01"
	case "dns-01", "dns01", "dns":
		return "dns-01"
	default:
		return "http-01"
	}
}

// ChallengeServer provides a standalone HTTP-01 handler for compatibility
// with callers that manage challenge tokens outside Manager.
type ChallengeServer struct {
	mu     sync.RWMutex
	tokens map[string]string
}

func NewChallengeServer() *ChallengeServer {
	return &ChallengeServer{tokens: make(map[string]string)}
}

func (s *ChallengeServer) SetToken(token, keyAuthorization string) {
	s.mu.Lock()
	s.tokens[token] = keyAuthorization
	s.mu.Unlock()
}

func (s *ChallengeServer) ClearToken(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

func (s *ChallengeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/.well-known/acme-challenge/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return true
	}
	token := strings.TrimPrefix(r.URL.Path, prefix)
	if token == "" || strings.Contains(token, "/") || path.Clean(r.URL.Path) != r.URL.Path {
		http.NotFound(w, r)
		return true
	}
	s.mu.RLock()
	value, ok := s.tokens[token]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(value))
	}
	return true
}

type httpChallengeProvider struct{ manager *Manager }

func (p httpChallengeProvider) Present(_ context.Context, _, token, keyAuth string) error {
	p.manager.mu.Lock()
	p.manager.httpTokens[token] = keyAuth
	p.manager.mu.Unlock()
	return nil
}

func (p httpChallengeProvider) CleanUp(_ context.Context, _, token, _ string) error {
	p.manager.mu.Lock()
	delete(p.manager.httpTokens, token)
	p.manager.mu.Unlock()
	return nil
}

type tlsALPNChallengeProvider struct{ manager *Manager }

func (p tlsALPNChallengeProvider) Present(_ context.Context, domain, _, keyAuth string) error {
	cert, err := tlsalpn01.ChallengeCert(domain, keyAuth)
	if err != nil {
		return err
	}
	p.manager.mu.Lock()
	p.manager.tlsTokens[strings.ToLower(domain)] = cert
	p.manager.mu.Unlock()
	return nil
}

func (p tlsALPNChallengeProvider) CleanUp(_ context.Context, domain, _, _ string) error {
	p.manager.mu.Lock()
	delete(p.manager.tlsTokens, strings.ToLower(domain))
	p.manager.mu.Unlock()
	return nil
}

var _ challenge.Provider = httpChallengeProvider{}
var _ challenge.Provider = tlsALPNChallengeProvider{}

func (m *Manager) validateChallengeConfiguration() error {
	if m.config.CertPath == "" {
		return fmt.Errorf("SSL certificate directory is required when Let's Encrypt is enabled")
	}
	email := strings.TrimSpace(m.config.LetsEncrypt.Email)
	parsedEmail, err := mail.ParseAddress(email)
	if err != nil || parsedEmail.Address != email {
		return fmt.Errorf("server.ssl.letsencrypt.email must be a valid email address")
	}
	challengeValue := strings.ToLower(strings.TrimSpace(m.config.LetsEncrypt.Challenge))
	if challengeValue != "" {
		switch challengeValue {
		case "http-01", "http01", "http", "tls-alpn-01", "tlsalpn01", "tls-alpn", "tls", "dns-01", "dns01", "dns":
		default:
			return fmt.Errorf("unsupported ACME challenge type %q", challengeValue)
		}
	}
	challengeType := ParseChallenge(challengeValue)
	if challengeType == "dns-01" {
		if strings.TrimSpace(m.config.LetsEncrypt.DNSProviderType) == "" {
			return fmt.Errorf("server.ssl.letsencrypt.dns_provider is required for dns-01")
		}
		_, err := m.newDNSChallengeProvider()
		return err
	}
	return nil
}

func (m *Manager) newDNSChallengeProvider() (challenge.Provider, error) {
	provider, err := newDNSProvider(m.config.LetsEncrypt.DNSProviderType, m.config.LetsEncrypt.DNSCredentials, m.config.CertPath)
	if err != nil {
		return nil, err
	}
	return &sanitizingDNSChallengeProvider{provider: provider, credentials: m.config.LetsEncrypt.DNSCredentials}, nil
}
