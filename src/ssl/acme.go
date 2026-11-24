package ssl

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns"
	"github.com/go-acme/lego/v5/registration"
)

const (
	productionDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	stagingDirectory    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

var (
	dnsEnvironmentMu sync.Mutex
	unsignedRFC2136  sync.Once
	nonEnvName       = regexp.MustCompile(`[^A-Za-z0-9_]`)
)

type acmeAccount struct {
	Email        string                `json:"email"`
	Registration *acme.ExtendedAccount `json:"registration,omitempty"`
	key          crypto.Signer
}

func (a *acmeAccount) GetEmail() string { return a.Email }

func (a *acmeAccount) GetRegistration() *acme.ExtendedAccount { return a.Registration }

func (a *acmeAccount) GetPrivateKey() crypto.Signer { return a.key }

func (m *Manager) obtainCertificate(ctx context.Context, domains []string) error {
	if ctx == nil {
		return fmt.Errorf("certificate request context is required")
	}
	if err := m.validateChallengeConfiguration(); err != nil {
		return err
	}
	stateDir, err := m.accountDirectory()
	if err != nil {
		return err
	}
	account, err := loadOrCreateAccount(stateDir, m.config.LetsEncrypt.Email)
	if err != nil {
		return err
	}
	legoConfig := lego.NewConfig(account)
	if m.config.LetsEncrypt.Staging {
		legoConfig.CADirURL = stagingDirectory
	} else {
		legoConfig.CADirURL = productionDirectory
	}
	legoConfig.UserAgent = "countries"
	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return fmt.Errorf("initialize ACME client: %w", err)
	}
	if account.Registration == nil {
		registered, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("register ACME account: %w", err)
		}
		account.Registration = registered
		if err := saveAccount(stateDir, account); err != nil {
			return fmt.Errorf("persist ACME account: %w", err)
		}
	}
	challengeType := ParseChallenge(m.config.LetsEncrypt.Challenge)
	switch challengeType {
	case "http-01":
		if err := client.Challenge.SetHTTP01Provider(httpChallengeProvider{manager: m}); err != nil {
			return fmt.Errorf("configure HTTP-01 challenge: %w", err)
		}
	case "tls-alpn-01":
		if err := client.Challenge.SetTLSALPN01Provider(tlsALPNChallengeProvider{manager: m}); err != nil {
			return fmt.Errorf("configure TLS-ALPN-01 challenge: %w", err)
		}
	case "dns-01":
		provider, err := m.newDNSChallengeProvider()
		if err != nil {
			return err
		}
		if err := client.Challenge.SetDNS01Provider(provider); err != nil {
			return fmt.Errorf("configure DNS-01 challenge: %w", err)
		}
	default:
		return fmt.Errorf("unsupported ACME challenge type")
	}
	issued, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{
		Domains: domains,
		KeyType: certcrypto.EC256,
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("obtain Let's Encrypt certificate: %w", sanitizeProviderError(err, m.config.LetsEncrypt.DNSCredentials))
	}
	if len(issued.Certificate) == 0 || len(issued.PrivateKey) == 0 {
		return fmt.Errorf("ACME server returned an incomplete certificate")
	}
	if err := saveManagedCertificate(m.config.CertPath, domains[0], issued); err != nil {
		return fmt.Errorf("save managed certificate: %w", err)
	}
	files := certificateFiles{
		certificate: filepath.Join(m.config.CertPath, "letsencrypt", domains[0], "fullchain.pem"),
		privateKey:  filepath.Join(m.config.CertPath, "letsencrypt", domains[0], "privkey.pem"),
		managed:     true,
	}
	if _, err := m.loadCertificate(files, domains); err != nil {
		return fmt.Errorf("validate issued certificate: %w", err)
	}
	m.mu.Lock()
	m.selected = files
	callback := m.issued
	m.mu.Unlock()
	if strings.EqualFold(strings.TrimSpace(m.config.LetsEncrypt.Challenge), "dns-01") && callback != nil {
		callback()
	}
	return nil
}

func (m *Manager) accountDirectory() (string, error) {
	environment := "production"
	if m.config.LetsEncrypt.Staging {
		environment = "staging"
	}
	directory := filepath.Join(m.config.CertPath, "letsencrypt", "accounts", environment)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", fmt.Errorf("create ACME account directory: %w", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return "", fmt.Errorf("secure ACME account directory: %w", err)
	}
	return directory, nil
}

func loadOrCreateAccount(directory, email string) (*acmeAccount, error) {
	keyPath := filepath.Join(directory, "account.key")
	keyData, err := os.ReadFile(keyPath)
	var signer crypto.Signer
	if err == nil {
		block, _ := pem.Decode(keyData)
		if block == nil {
			return nil, fmt.Errorf("ACME account key is not valid PEM")
		}
		privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse ACME account key: %w", err)
		}
		var ok bool
		signer, ok = privateKey.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("ACME account key does not implement crypto.Signer")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ACME account key: %w", err)
		}
		signer = key
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("encode ACME account key: %w", err)
		}
		if err := writeAtomicPrivateFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
			return nil, fmt.Errorf("save ACME account key: %w", err)
		}
	} else {
		return nil, fmt.Errorf("read ACME account key: %w", err)
	}
	account := &acmeAccount{Email: email, key: signer}
	data, err := os.ReadFile(filepath.Join(directory, "account.json"))
	if err == nil {
		if err := json.Unmarshal(data, account); err != nil {
			return nil, fmt.Errorf("read ACME account registration: %w", err)
		}
		account.Email = email
		account.key = signer
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read ACME account registration: %w", err)
	}
	return account, nil
}

func saveAccount(directory string, account *acmeAccount) error {
	data, err := json.Marshal(account)
	if err != nil {
		return err
	}
	return writeAtomicPrivateFile(filepath.Join(directory, "account.json"), data)
}

func saveManagedCertificate(root, domain string, cert *certificate.Resource) error {
	directory := filepath.Join(root, "letsencrypt", domain)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	if err := writeAtomicPrivateFile(filepath.Join(directory, "fullchain.pem"), cert.Certificate); err != nil {
		return err
	}
	return writeAtomicPrivateFile(filepath.Join(directory, "privkey.pem"), cert.PrivateKey)
}

func writeAtomicPrivateFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".ssl-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	parent, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	return errors.Join(syncErr, closeErr)
}

func newDNSProvider(name string, credentials map[string]string, sslRoot string) (challenge.Provider, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "rfc2136" {
		name = "dnsupdate"
	}
	if name == "" {
		return nil, fmt.Errorf("DNS provider name is required")
	}
	providerCredentials := make(map[string]string, len(credentials))
	for key, value := range credentials {
		key = strings.TrimSpace(key)
		if key == "" || nonEnvName.MatchString(key) {
			return nil, fmt.Errorf("DNS credential names must be environment variable names")
		}
		if strings.HasPrefix(key, "RFC2136_") {
			legoKey := "DNSUPDATE_" + strings.TrimPrefix(key, "RFC2136_")
			if prior, ok := providerCredentials[legoKey]; ok && prior != value {
				return nil, fmt.Errorf("conflicting DNSUPDATE and RFC2136 credential values")
			}
			key = legoKey
		}
		providerCredentials[key] = value
	}
	if name == "dnsupdate" && providerCredentials["DNSUPDATE_NAMESERVER"] == "" {
		return nil, fmt.Errorf("DNS provider credentials missing variable DNSUPDATE_NAMESERVER")
	}
	if name == "dnsupdate" {
		nameserver, err := normalizeRFC2136Nameserver(providerCredentials["DNSUPDATE_NAMESERVER"])
		if err != nil {
			return nil, fmt.Errorf("DNS provider credentials contain invalid variable DNSUPDATE_NAMESERVER")
		}
		providerCredentials["DNSUPDATE_NAMESERVER"] = nameserver
		if keyFile := providerCredentials["DNSUPDATE_TSIG_FILE"]; keyFile != "" {
			resolved, err := resolveTSIGFile(sslRoot, keyFile)
			if err != nil {
				return nil, fmt.Errorf("DNS provider credentials contain invalid variable DNSUPDATE_TSIG_FILE")
			}
			providerCredentials["DNSUPDATE_TSIG_FILE"] = resolved
		}
	}
	if name == "dnsupdate" && providerCredentials["DNSUPDATE_TSIG_KEY"] == "" && providerCredentials["DNSUPDATE_TSIG_SECRET"] == "" && providerCredentials["DNSUPDATE_TSIG_FILE"] == "" {
		unsignedRFC2136.Do(func() {
			fmt.Fprintln(os.Stderr, "WARN: rfc2136: unsigned update")
		})
	}
	dnsEnvironmentMu.Lock()
	defer dnsEnvironmentMu.Unlock()
	previous := make(map[string]string)
	for _, item := range os.Environ() {
		key, _, found := strings.Cut(item, "=")
		if found {
			previous[key] = item[len(key)+1:]
			_ = os.Unsetenv(key)
		}
	}
	for key, value := range providerCredentials {
		if err := os.Setenv(key, value); err != nil {
			restoreEnvironment(previous)
			return nil, fmt.Errorf("set DNS provider variable %s: %w", key, err)
		}
	}
	defer restoreEnvironment(previous)
	provider, err := dns.NewDNSChallengeProviderByName(name)
	if err != nil {
		return nil, fmt.Errorf("DNS provider %q configuration invalid: %w", name, sanitizeProviderError(err, providerCredentials))
	}
	return &sanitizingDNSChallengeProvider{provider: provider, credentials: providerCredentials}, nil
}

func normalizeRFC2136Nameserver(nameserver string) (string, error) {
	nameserver = strings.TrimSpace(nameserver)
	if host, port, err := net.SplitHostPort(nameserver); err == nil {
		portNumber, parseErr := strconv.Atoi(port)
		if host == "" || parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("invalid nameserver")
		}
		return net.JoinHostPort(host, port), nil
	}
	host := nameserver
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n") || strings.HasSuffix(host, ":") || strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", fmt.Errorf("invalid nameserver")
	}
	return net.JoinHostPort(host, "53"), nil
}

func restoreEnvironment(previous map[string]string) {
	for _, item := range os.Environ() {
		key, _, found := strings.Cut(item, "=")
		if found {
			_ = os.Unsetenv(key)
		}
	}
	for key, value := range previous {
		_ = os.Setenv(key, value)
	}
}

func resolveTSIGFile(root, name string) (string, error) {
	if root == "" || filepath.IsAbs(name) || strings.TrimSpace(name) != name {
		return "", fmt.Errorf("invalid TSIG key path")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return "", err
	}
	filePath, err := filepath.EvalSymlinks(filepath.Join(rootPath, name))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootPath, filePath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("TSIG key path must remain inside the SSL directory")
	}
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("TSIG key path is not a regular file")
	}
	return filePath, nil
}

func sanitizeProviderError(err error, credentials map[string]string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	lowerMessage := strings.ToLower(message)
	switch {
	case strings.Contains(lowerMessage, "bad authentication"):
		message = "BADSIG"
	case strings.Contains(lowerMessage, "bad key"):
		message = "BADKEY"
	case strings.Contains(lowerMessage, "bad time"):
		message = "BADTIME"
	}
	for _, value := range credentials {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return errors.New(message)
}

type sanitizingDNSChallengeProvider struct {
	provider    challenge.Provider
	credentials map[string]string
}

func (p *sanitizingDNSChallengeProvider) Present(ctx context.Context, domain, token, keyAuthorization string) error {
	return sanitizeProviderError(p.provider.Present(ctx, domain, token, keyAuthorization), p.credentials)
}

func (p *sanitizingDNSChallengeProvider) CleanUp(ctx context.Context, domain, token, keyAuthorization string) error {
	return sanitizeProviderError(p.provider.CleanUp(ctx, domain, token, keyAuthorization), p.credentials)
}
