//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/chromedp/chromedp/remote"
)

var (
	baseURL       string
	artifactDir   string
	serverLog     *os.File
	serverCommand *exec.Cmd
	allocatorCtx  context.Context
	allocatorStop context.CancelFunc
	browserRoot   context.Context
	browserStop   context.CancelFunc
)

func TestMain(m *testing.M) {
	artifactDir = os.Getenv("COUNTRIES_E2E_TMPDIR")
	port := os.Getenv("COUNTRIES_E2E_PORT")
	binary := os.Getenv("COUNTRIES_E2E_BINARY")
	wsURL := os.Getenv("CHROMEDP_REMOTE_WS")
	configuredBaseURL := os.Getenv("COUNTRIES_E2E_BASE_URL")
	if artifactDir == "" || port == "" || binary == "" || wsURL == "" {
		fmt.Fprintln(os.Stderr, "COUNTRIES_E2E_TMPDIR, COUNTRIES_E2E_PORT, COUNTRIES_E2E_BINARY, and CHROMEDP_REMOTE_WS are required")
		os.Exit(2)
	}
	for _, dir := range []string{"config", "data", "logs", "cache", "backup", "failures"} {
		if err := os.MkdirAll(filepath.Join(artifactDir, dir), 0700); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	dependencyStub, caCertificate, stubErr := newDependencyStub(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"resultsPerPage":1,"startIndex":0,"totalResults":1,"vulnerabilities":[{"cve":{"id":"CVE-2000-0000"}}]}`)
	}))
	if stubErr != nil {
		fmt.Fprintln(os.Stderr, stubErr)
		os.Exit(2)
	}
	dependencyURL := strings.Replace(dependencyStub.URL, "127.0.0.1", "localhost", 1)
	certificatePath := filepath.Join(artifactDir, "config", "e2e-ca.pem")
	if err := os.WriteFile(certificatePath, caCertificate, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	databasePath := filepath.Join(artifactDir, "data", "db", "server.db")
	fixtureConfig := fmt.Sprintf("data:\n  cve:\n    source: %q\nserver:\n  port: %q\n  address: 0.0.0.0\n  database:\n    driver: sqlite\n    url: %q\n  geoip:\n    enabled: false\n  tor:\n    enabled: false\n  i2p:\n    enabled: false\n  security:\n    blocklists:\n      enabled: false\n  rate_limit:\n    global_burst: 10000\n    read:\n      requests: 10000\n      window: 60\n    write:\n      requests: 10000\n      window: 60\n    health:\n      requests: 10000\n      window: 60\n    strict:\n      requests: 10000\n      window: 60\n    auth_failures:\n      requests: 10000\n      window: 60\n", dependencyURL, port, databasePath)
	if err := os.WriteFile(filepath.Join(artifactDir, "config", "server.yml"), []byte(fixtureConfig), 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	baseURL = configuredBaseURL
	if baseURL == "" {
		baseURL = "http://127.0.0.1:" + port
	}
	logPath := filepath.Join(artifactDir, "logs", "server.log")
	var err error
	serverLog, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	serverCommand = exec.Command(binary, "--address=0.0.0.0", "--port="+port,
		"--config="+filepath.Join(artifactDir, "config"), "--data="+filepath.Join(artifactDir, "data"), "--log="+filepath.Join(artifactDir, "logs"), "--color=no")
	serverCommand.Env = make([]string, 0, len(os.Environ())+5)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "CONFIG_DIR=") || strings.HasPrefix(value, "DATA_DIR=") || strings.HasPrefix(value, "CACHE_DIR=") || strings.HasPrefix(value, "LOG_DIR=") || strings.HasPrefix(value, "LOGS_DIR=") || strings.HasPrefix(value, "BACKUP_DIR=") || strings.HasPrefix(value, "SSL_CERT_FILE=") {
			continue
		}
		serverCommand.Env = append(serverCommand.Env, value)
	}
	serverCommand.Env = append(serverCommand.Env,
		"CONFIG_DIR="+filepath.Join(artifactDir, "config"),
		"DATA_DIR="+filepath.Join(artifactDir, "data"),
		"CACHE_DIR="+filepath.Join(artifactDir, "cache"),
		"LOG_DIR="+filepath.Join(artifactDir, "logs"),
		"BACKUP_DIR="+filepath.Join(artifactDir, "backup"),
		"SSL_CERT_FILE="+certificatePath,
	)
	serverCommand.Stdout, serverCommand.Stderr = serverLog, serverLog
	if err := serverCommand.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	allocatorCtx, allocatorStop = remote.NewAllocator(context.Background(), wsURL)
	if err := waitForServer(30 * time.Second); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = serverCommand.Process.Kill()
		os.Exit(2)
	}
	browserRoot, browserStop = chromedp.NewContext(allocatorCtx)
	if _, err := chromedp.Run(browserRoot, chromedp.Navigate("about:blank")); err != nil {
		fmt.Fprintln(os.Stderr, "connect to the headless browser:", err)
		_ = serverCommand.Process.Kill()
		os.Exit(2)
	}
	code := m.Run()
	dependencyStub.Close()
	browserStop()
	allocatorStop()
	_ = serverCommand.Process.Kill()
	_, _ = serverCommand.Process.Wait()
	_ = serverLog.Close()
	os.Exit(code)
}

func newDependencyStub(handler http.Handler) (*httptest.Server, []byte, error) {
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate E2E test CA key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Countries E2E Test CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create E2E test CA certificate: %w", err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate E2E stub key: %w", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create E2E stub certificate: %w", err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode E2E stub private key: %w", err)
	}
	pair, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("load E2E stub TLS keypair: %w", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	return server, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}

func waitForServer(timeout time.Duration) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/api/v1/server/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		if serverCommand.ProcessState != nil {
			return fmt.Errorf("server exited before becoming healthy; see %s", filepath.Join(artifactDir, "logs", "server.log"))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("server did not become healthy within %s; see %s", timeout, filepath.Join(artifactDir, "logs", "server.log"))
}

type pageCase struct {
	path    string
	title   string
	content string
}

var pages = []pageCase{
	{path: "/", title: "Countries", content: "Countries"},
	{path: "/search", title: "Search Countries", content: "Search"},
	{path: "/search?q=Japan", title: "Search Countries", content: "Japan"},
	{path: "/coordinates", title: "Find Nearest Country", content: "Latitude"},
	{path: "/coordinates?lat=40.7128&lon=-74.0060", title: "Find Nearest Country", content: "Nearest Country"},
	{path: "/server/docs/swagger", title: "REST API", content: "OpenAPI"},
	{path: "/server/docs/graphql", title: "GraphQL Explorer", content: "GraphQL"},
	{path: "/server/about", title: "About", content: "Countries"},
	{path: "/server/help", title: "Help", content: "Countries"},
	{path: "/server/help/mcp", title: "MCP server help", content: "Endpoint URL"},
	{path: "/server/privacy", title: "Privacy", content: "Privacy"},
	{path: "/server/terms", title: "Terms", content: "Terms"},
	{path: "/server/security", title: "Security", content: "Security"},
	{path: "/server/security/policy", title: "Security disclosure policy", content: "Scope"},
	{path: "/server/security/thanks", title: "Security acknowledgments", content: "Acknowledged researchers"},
	{path: "/server/contact", title: "Contact", content: "Contact"},
	{path: "/server/dpo", title: "Data Protection", content: "Data"},
	{path: "/server/preferences", title: "Preferences", content: "Appearance"},
	{path: "/server/preferences/sync", title: "Preferences", content: "sync"},
	{path: "/server/preferences/tokens", title: "Owner tokens", content: "New token"},
	{path: "/server/healthz", title: "Service health", content: "Status"},
}

func getHTML(client *http.Client, path string) (*http.Response, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/html")
	return client.Do(request)
}

func TestTier1InternalLinksAndAssets(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	attribute := regexp.MustCompile(`(?:href|src)="([^"]+)"`)
	visited := make(map[string]bool)
	for _, page := range pages {
		response, err := getHTML(client, page.path)
		if err != nil {
			t.Fatalf("GET %s: %v", page.path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", page.path, readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s returned %d", page.path, response.StatusCode)
		}
		for _, match := range attribute.FindAllStringSubmatch(string(body), -1) {
			resource := strings.TrimSpace(match[1])
			if !strings.HasPrefix(resource, "/") || strings.HasPrefix(resource, "//") {
				continue
			}
			if visited[resource] {
				continue
			}
			visited[resource] = true
			linkedResponse, err := client.Get(baseURL + resource)
			if err != nil {
				t.Errorf("GET linked resource %s from %s: %v", resource, page.path, err)
				continue
			}
			_ = linkedResponse.Body.Close()
			if linkedResponse.StatusCode >= http.StatusBadRequest {
				t.Errorf("linked resource %s from %s returned %d", resource, page.path, linkedResponse.StatusCode)
			}
		}
	}
}

func TestTier1SSRPages(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, page := range pages {
		page := page
		t.Run(page.path, func(t *testing.T) {
			response, err := getHTML(client, page.path)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status=%d for %s", response.StatusCode, page.path)
			}
			if !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
				t.Fatalf("content type=%q", response.Header.Get("Content-Type"))
			}
			for _, want := range []string{"<title>", page.title, "<html", "<main", "name=\"viewport\"", "charset=", `lang="`, page.content} {
				if !strings.Contains(string(body), want) {
					t.Errorf("%s is missing %q", page.path, want)
				}
			}
			for _, want := range []string{`href="#main-content"`, `href="#site-navigation"`, `id="site-navigation"`, `class="footer"`} {
				if !strings.Contains(string(body), want) {
					t.Errorf("%s shared page shell is missing %q", page.path, want)
				}
			}
			if strings.Contains(string(body), `<div id="app"></div>`) {
				t.Fatal("page is an empty client-rendered shell")
			}
		})
	}
}

func TestTier1SSRAssetsNegotiationAndErrors(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/static/css/main.css", "/static/js/app.js", "/manifest.json", "/sw.js"} {
		response, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("%s status=%d", path, response.StatusCode)
		}
	}
	response, err := client.Get(baseURL + "/server")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMovedPermanently && response.StatusCode != http.StatusFound {
		t.Fatalf("/server status=%d", response.StatusCode)
	}
	if response.Header.Get("Location") != "/server/about" {
		t.Errorf("/server redirect=%q", response.Header.Get("Location"))
	}
	response, err = getHTML(client, "/not-a-real-page")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "<main") {
		t.Errorf("unknown route status=%d body=%q", response.StatusCode, body)
	}
	request, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/countries/search?q=Japan", nil)
	request.Header.Set("Accept", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "json") {
		t.Errorf("JSON negotiation status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
}

func TestTier1CountryAPIAndDiscoveryFeatures(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	cases := []struct {
		path string
		want string
	}{
		{path: "/api/v1/countries/JP", want: `"common": "Japan"`},
		{path: "/api/v1/countries/search?q=Japan", want: `"common": "Japan"`},
		{path: "/api/v1/countries?region=Asia&limit=1", want: `"region": "Asia"`},
		{path: "/api/v1/coordinates?lat=40.7128&lon=-74.0060", want: `"common": "Bermuda"`},
		{path: "/api/v1/nearby?lat=38&lon=-97&radius=250", want: `"common": "United States"`},
		{path: "/api/v1/stats", want: `"total_countries": 249`},
		{path: "/api/v1/count", want: `"count": 249`},
	}
	for _, test := range cases {
		request, err := http.NewRequest(http.MethodGet, baseURL+test.path, nil)
		if err != nil {
			t.Errorf("GET %s: %v", test.path, err)
			continue
		}
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Errorf("GET %s: %v", test.path, err)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Errorf("read %s: %v", test.path, readErr)
			continue
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "application/json") {
			t.Errorf("GET %s status=%d content-type=%q body=%q", test.path, response.StatusCode, response.Header.Get("Content-Type"), body)
			continue
		}
		if !strings.Contains(string(body), test.want) {
			t.Errorf("GET %s body does not contain %q: %s", test.path, test.want, body)
		}
	}

	for _, path := range []string{"/api/v1/server/swagger", "/.well-known/security.txt", "/llms.txt"} {
		request, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		if strings.HasPrefix(path, "/api/") {
			request.Header.Set("Accept", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s status=%d", path, response.StatusCode)
		}
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/server/graphql", strings.NewReader(`{"query":"{ countries { name { common } } }"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Japan") {
		t.Errorf("GraphQL country query status=%d body=%q", response.StatusCode, body)
	}
}

func TestAPIContentNegotiationFromOpenAPI(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	requestAPI := func(method, path, accept string) (*http.Response, error) {
		var body io.Reader
		if method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut {
			body = strings.NewReader(`{}`)
		}
		request, err := http.NewRequest(method, baseURL+path, body)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", accept)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		return client.Do(request)
	}

	response, err := requestAPI(http.MethodGet, "/api/v1/server/swagger", "application/json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&document)
	_ = response.Body.Close()
	if decodeErr != nil {
		t.Fatalf("decode OpenAPI route registry: %v", decodeErr)
	}
	if len(document.Paths) == 0 {
		t.Fatal("OpenAPI route registry contains no paths")
	}
	methods := map[string]string{
		"get": http.MethodGet, "post": http.MethodPost, "put": http.MethodPut,
		"patch": http.MethodPatch, "delete": http.MethodDelete,
	}
	for route, operations := range document.Paths {
		for method, operation := range operations {
			httpMethod, supported := methods[method]
			var operationData map[string]json.RawMessage
			if !supported || json.Unmarshal(operation, &operationData) != nil {
				continue
			}
			path := strings.ReplaceAll(route, "{code}", "US")
			path = strings.ReplaceAll(path, "{id}", "test-id")
			path = strings.ReplaceAll(path, "{report}", "csp")
			for _, accept := range []string{"application/json", "text/plain", ""} {
				if (path == "/mcp" || path == "/api/mcp" || strings.HasSuffix(path, "/server/mcp")) && accept != "application/json" {
					continue
				}
				response, err := requestAPI(httpMethod, path, accept)
				if err != nil {
					t.Errorf("%s %s Accept %s: %v", httpMethod, path, accept, err)
					continue
				}
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil {
					t.Errorf("read %s %s Accept %s: %v", httpMethod, path, accept, readErr)
					continue
				}
				wantType := "application/json"
				if accept == "text/plain" || accept == "" || strings.HasSuffix(path, ".txt") {
					wantType = "text/plain"
				}
				protocolMetrics := strings.Contains(path, "/metrics")
				if len(body) > 0 && !protocolMetrics && !strings.Contains(response.Header.Get("Content-Type"), wantType) {
					t.Errorf("%s %s Accept %s: content type %q, want %s (status %d, body %q)", httpMethod, path, accept, response.Header.Get("Content-Type"), wantType, response.StatusCode, body)
				}
				if len(body) > 0 && body[len(body)-1] != '\n' {
					t.Errorf("%s %s Accept %s: response must end with newline", httpMethod, path, accept)
				}
			}
			if httpMethod == http.MethodGet && !strings.Contains(path, "/metrics") && !strings.HasSuffix(path, ".txt") {
				response, err := requestAPI(http.MethodGet, path+".txt", "application/json")
				if err != nil {
					t.Errorf("GET %s.txt: %v", path, err)
					continue
				}
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil {
					t.Errorf("read GET %s.txt: %v", path, readErr)
					continue
				}
				if len(body) > 0 && !strings.Contains(path, "/metrics") && (!strings.Contains(response.Header.Get("Content-Type"), "text/plain") || body[len(body)-1] != '\n') {
					t.Errorf("GET %s.txt: content type=%q status=%d body=%q", path, response.Header.Get("Content-Type"), response.StatusCode, body)
				}
			}
		}
	}
}

func TestFrontendPageNegotiation(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, page := range pages {
		if strings.HasPrefix(page.path, "/server/docs/") || strings.Contains(page.path, "?") {
			continue
		}
		for _, accept := range []struct{ value, contentType string }{
			{value: "text/html", contentType: "text/html"},
			{value: "text/plain", contentType: "text/plain"},
		} {
			request, err := http.NewRequest(http.MethodGet, baseURL+page.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Accept", accept.value)
			response, err := client.Do(request)
			if err != nil {
				t.Errorf("GET %s Accept %s: %v", page.path, accept.value, err)
				continue
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Errorf("read GET %s Accept %s: %v", page.path, accept.value, readErr)
				continue
			}
			if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), accept.contentType) {
				t.Errorf("GET %s Accept %s: status=%d content-type=%q body=%q", page.path, accept.value, response.StatusCode, response.Header.Get("Content-Type"), body)
			}
		}
	}
}

func TestPreferenceSyncAPIUpdateWithoutCSRF(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/server/preferences/sync", strings.NewReader(`{"theme":"light","lang":"fr"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	var syncResult struct {
		Data struct {
			Applied []string `json:"applied"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &syncResult); err != nil {
		t.Fatalf("decode preference sync response: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "application/json") || len(syncResult.Data.Applied) != 2 || syncResult.Data.Applied[0] != "lang" || syncResult.Data.Applied[1] != "theme" {
		t.Fatalf("preference sync status=%d content-type=%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	if len(response.Cookies()) == 0 {
		t.Fatal("preference sync did not set preference cookies")
	}

	invalid, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/server/preferences/sync", strings.NewReader(`{"theme":"invalid"}`))
	if err != nil {
		t.Fatal(err)
	}
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("Accept", "application/json")
	response, err = client.Do(invalid)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), `"error": "VALIDATION_FAILED"`) {
		t.Fatalf("invalid preference sync status=%d body=%s", response.StatusCode, body)
	}
}

func TestExplicitAPITextRoutesKeepTheirFormats(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	cases := []struct {
		path string
		want string
	}{
		{path: "/api/v1/count.txt", want: "249\n"},
		{path: "/api/v1/stats.txt", want: "Total Countries: 249\n"},
		{path: "/api/v1/server/healthz.txt", want: "status: "},
		{path: "/api/healthz.txt", want: "status: "},
	}
	for _, test := range cases {
		request, err := http.NewRequest(http.MethodGet, baseURL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Errorf("GET %s: %v", test.path, err)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Errorf("read GET %s: %v", test.path, readErr)
			continue
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/plain") || !strings.Contains(string(body), test.want) || len(body) == 0 || body[len(body)-1] != '\n' {
			t.Errorf("GET %s status=%d content-type=%q body=%q", test.path, response.StatusCode, response.Header.Get("Content-Type"), body)
		}
	}
}

func TestOpenAPIRoutesMatchAIAndIDEAInventories(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/server/swagger", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&document)
	_ = response.Body.Close()
	if decodeErr != nil {
		t.Fatalf("decode OpenAPI registry: %v", decodeErr)
	}
	idea, err := os.ReadFile("../../IDEA.md")
	if err != nil {
		t.Fatalf("read IDEA API inventory: %v", err)
	}
	ai, err := os.ReadFile("../../AI.md")
	if err != nil {
		t.Fatalf("read AI template route inventory: %v", err)
	}
	contractRoutes := make(map[string]map[string]bool)
	for label, source := range map[string]string{"IDEA.md": string(idea), "AI.md": string(ai)} {
		for _, route := range documentedAPIRoutes(source, label) {
			method, path := route[0], route[1]
			if contractRoutes[path] == nil {
				contractRoutes[path] = make(map[string]bool)
			}
			contractRoutes[path][method] = true
			operations, exists := document.Paths[path]
			if !exists || len(operations[method]) == 0 {
				t.Errorf("%s inventory route %s %s is absent from generated OpenAPI", method, path, label)
				continue
			}
			textVariant, _ := strconv.ParseBool(route[2])
			if textVariant {
				textPath := path + ".txt"
				if !strings.HasSuffix(path, ".txt") {
					if _, exists := document.Paths[textPath]; !exists {
						t.Errorf("%s inventory route %s requires generated text route %s", label, path, textPath)
					}
				}
			}
			if method == "get" && !strings.Contains(path, "/metrics") && !strings.HasSuffix(path, ".txt") {
				textPath := path + ".txt"
				if contractRoutes[textPath] == nil {
					contractRoutes[textPath] = make(map[string]bool)
				}
				contractRoutes[textPath][method] = true
			}
		}
	}
	for path, operations := range document.Paths {
		if !strings.HasPrefix(path, "/api/") && path != "/mcp" {
			continue
		}
		for method := range operations {
			if contractRoutes[path][method] || isOptionalRuntimeRoute(path) {
				continue
			}
			t.Errorf("generated OpenAPI route %s %s is absent from AI.md and IDEA.md inventories", strings.ToUpper(method), path)
		}
	}
}

func isOptionalRuntimeRoute(path string) bool {
	return strings.Contains(path, "/metrics") || strings.Contains(path, "/debug/")
}

func documentedAPIRoutes(document, sourceName string) [][3]string {
	var table string
	if sourceName == "IDEA.md" {
		start := strings.Index(document, "### API routes")
		if start < 0 {
			return nil
		}
		document = document[start+len("### API routes"):]
		if end := strings.Index(document, "\n## "); end >= 0 {
			document = document[:end]
		}
		table = document
	} else {
		start := strings.Index(document, "## Root-Level Endpoints")
		if start < 0 {
			return nil
		}
		document = document[start:]
		if end := strings.Index(document, "**NOTE:** OpenAPI is JSON only"); end >= 0 {
			document = document[:end]
		}
		table = document
	}
	code := regexp.MustCompile("`([^`]+)`")
	rows := strings.Split(table, "\n")
	var routes [][3]string
	for _, row := range rows {
		cells := strings.Split(row, "|")
		if len(cells) < 4 {
			continue
		}
		methodCell, routeCell := strings.TrimSpace(cells[1]), cells[2]
		if sourceName == "AI.md" {
			routeCell, methodCell = cells[1], strings.TrimSpace(cells[2])
		}
		if !strings.Contains(strings.ToUpper(methodCell), "GET") && !strings.Contains(strings.ToUpper(methodCell), "POST") && !strings.Contains(strings.ToUpper(methodCell), "PATCH") && !strings.Contains(strings.ToUpper(methodCell), "DELETE") && !strings.Contains(strings.ToUpper(methodCell), "PUT") {
			continue
		}
		methods := strings.Split(strings.ToLower(methodCell), ",")
		paths := code.FindAllStringSubmatch(routeCell, -1)
		for _, entry := range paths {
			path := strings.ReplaceAll(entry[1], "{api_version}", "v1")
			if (!strings.HasPrefix(path, "/api/") && path != "/mcp") || strings.ContainsAny(path, "[]*") {
				continue
			}
			textVariant := strings.Contains(routeCell, ".txt")
			for _, method := range methods {
				method = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(method), "|"))
				if method == "all" || method == "get, post" || method == "get post" {
					continue
				}
				if method == "get" || method == "post" || method == "patch" || method == "delete" || method == "put" {
					routes = append(routes, [3]string{method, path, strconv.FormatBool(textVariant)})
				}
			}
		}
	}
	return routes
}

func TestTier1OwnerTokenLifecycleAPI(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	call := func(method, path, bearer string, body string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatalf("build %s %s request: %v", method, path, err)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("Accept", "application/json")
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read %s %s response: %v", method, path, err)
		}
		return response.StatusCode, content
	}
	type tokenData struct {
		Data struct {
			OwnerToken string `json:"owner_token"`
			Token      string `json:"token"`
			ID         int64  `json:"id"`
		} `json:"data"`
	}
	decodeToken := func(status int, body []byte, wantStatus int, field string) (string, int64) {
		t.Helper()
		if status != wantStatus {
			t.Fatalf("token API status=%d, want %d", status, wantStatus)
		}
		var result tokenData
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("decode token API response: %v", err)
		}
		value := result.Data.Token
		if field == "owner_token" {
			value = result.Data.OwnerToken
		}
		if value == "" {
			t.Fatalf("token API omitted %s", field)
		}
		return value, result.Data.ID
	}
	listPath := "/api/v1/server/preferences/tokens"
	if status, _ := call(http.MethodGet, listPath, "", ""); status != http.StatusUnauthorized {
		t.Fatalf("token list without bearer status=%d, want 401", status)
	}
	status, body := call(http.MethodPost, listPath+"/primary", "", `{}`)
	primary, _ := decodeToken(status, body, http.StatusCreated, "owner_token")
	if !strings.HasPrefix(primary, "own_") {
		t.Fatal("issued owner token has an invalid prefix")
	}
	if status, _ := call(http.MethodPost, listPath+"/primary", primary, `{}`); status != http.StatusConflict {
		t.Fatalf("second primary issue status=%d, want 409", status)
	}
	status, body = call(http.MethodPost, listPath, primary, `{"name":"e2e-client","expiry":"7d"}`)
	linked, linkedID := decodeToken(status, body, http.StatusCreated, "token")
	if linkedID == 0 || linked == primary {
		t.Fatal("linked token was not independently issued")
	}
	status, body = call(http.MethodGet, listPath, primary, "")
	if status != http.StatusOK || !strings.Contains(string(body), "e2e-client") || strings.Contains(string(body), primary) || strings.Contains(string(body), linked) {
		t.Fatal("token list did not expose metadata only")
	}
	if status, _ := call(http.MethodGet, listPath, linked, ""); status != http.StatusForbidden {
		t.Fatalf("linked token management status=%d, want 403", status)
	}
	linkedPath := listPath + "/" + strconv.FormatInt(linkedID, 10)
	status, _ = call(http.MethodPatch, linkedPath, primary, `{"name":"renamed-client","expiry":"30d"}`)
	if status != http.StatusOK {
		t.Fatalf("linked token update status=%d, want 200", status)
	}
	status, body = call(http.MethodPost, linkedPath+"/rotate", primary, `{"confirm":true}`)
	rotatedLinked, _ := decodeToken(status, body, http.StatusOK, "token")
	if rotatedLinked == linked {
		t.Fatal("linked-token rotation did not replace its value")
	}
	if status, _ := call(http.MethodGet, listPath, linked, ""); status != http.StatusUnauthorized {
		t.Fatalf("old linked token status=%d after rotation, want 401", status)
	}
	if status, _ := call(http.MethodGet, listPath, rotatedLinked, ""); status != http.StatusForbidden {
		t.Fatalf("rotated linked token management status=%d, want 403", status)
	}
	status, body = call(http.MethodPost, listPath+"/primary/rotate", primary, `{"confirm":true}`)
	rotatedPrimary, _ := decodeToken(status, body, http.StatusOK, "owner_token")
	if rotatedPrimary == primary {
		t.Fatal("primary-token rotation did not replace its value")
	}
	if status, _ := call(http.MethodGet, listPath, primary, ""); status != http.StatusUnauthorized {
		t.Fatalf("old primary token status=%d after rotation, want 401", status)
	}
	if status, _ := call(http.MethodGet, listPath, rotatedPrimary, ""); status != http.StatusOK {
		t.Fatalf("rotated primary token list status=%d, want 200", status)
	}
	if status, _ := call(http.MethodGet, listPath, rotatedLinked, ""); status != http.StatusForbidden {
		t.Fatalf("linked token stopped working after primary rotation: status=%d", status)
	}
	status, _ = call(http.MethodDelete, linkedPath, rotatedPrimary, `{"confirm":true}`)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("linked token revocation status=%d", status)
	}
	if status, _ := call(http.MethodGet, listPath, rotatedLinked, ""); status != http.StatusUnauthorized {
		t.Fatalf("revoked linked token status=%d, want 401", status)
	}
}

func TestTier1SecurityDisclosurePagesAndPublicAPI(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, page := range []pageCase{
		{path: "/server/security/policy", title: "Security disclosure policy", content: "Scope"},
		{path: "/server/security/thanks", title: "Security acknowledgments", content: "Acknowledged researchers"},
	} {
		response, err := getHTML(client, page.path)
		if err != nil {
			t.Fatalf("GET %s: %v", page.path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", page.path, readErr)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/html") {
			t.Errorf("GET %s status=%d content-type=%q", page.path, response.StatusCode, response.Header.Get("Content-Type"))
		}
		for _, want := range []string{page.title, page.content, `href="#main-content"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("%s is missing %q", page.path, want)
			}
		}
	}

	for _, endpoint := range []struct {
		path string
		want string
	}{
		{path: "/api/v1/server/security/policy", want: `"policy": "## Scope`},
		{path: "/api/v1/server/security/thanks", want: `"entries": []`},
	} {
		request, err := http.NewRequest(http.MethodGet, baseURL+endpoint.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("GET %s without authentication: %v", endpoint.path, err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", endpoint.path, readErr)
		}
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "application/json") || !strings.Contains(string(body), endpoint.want) {
			t.Errorf("GET %s status=%d content-type=%q body=%s", endpoint.path, response.StatusCode, response.Header.Get("Content-Type"), body)
		}
	}

	securityRequest, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/server/security", nil)
	if err != nil {
		t.Fatal(err)
	}
	securityRequest.Header.Set("Accept", "application/json")
	response, err := client.Do(securityRequest)
	if err != nil {
		t.Fatal(err)
	}
	var securityResponse struct {
		OK   bool `json:"ok"`
		Data struct {
			PolicyURL string `json:"policy_url"`
			ThanksURL string `json:"thanks_url"`
			PGP       struct {
				Available   bool            `json:"available"`
				URL         json.RawMessage `json:"url"`
				Fingerprint json.RawMessage `json:"fingerprint"`
			} `json:"pgp"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&securityResponse)
	_ = response.Body.Close()
	if decodeErr != nil {
		t.Fatalf("decode security API response: %v", decodeErr)
	}
	if response.StatusCode != http.StatusOK || !securityResponse.OK || !strings.HasSuffix(securityResponse.Data.PolicyURL, "/server/security/policy") || !strings.HasSuffix(securityResponse.Data.ThanksURL, "/server/security/thanks") {
		t.Errorf("security API URLs/status do not match public pages: status=%d data=%+v", response.StatusCode, securityResponse.Data)
	}
	if securityResponse.Data.PGP.Available || string(securityResponse.Data.PGP.URL) != "null" || string(securityResponse.Data.PGP.Fingerprint) != "null" {
		t.Errorf("security API reported a PGP key although the fresh E2E config has none: %+v", securityResponse.Data.PGP)
	}

	response, err = client.Get(baseURL + "/.well-known/security.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "/server/security/policy\n") || !strings.Contains(string(body), "Expires:") {
		t.Errorf("security.txt policy or expiry missing: status=%d body=%q", response.StatusCode, body)
	}
	headRequest, err := http.NewRequest(http.MethodHead, baseURL+"/.well-known/security.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	headResponse, err := client.Do(headRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = headResponse.Body.Close()
	if headResponse.StatusCode != http.StatusOK {
		t.Errorf("HEAD /.well-known/security.txt status=%d, want 200", headResponse.StatusCode)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request, err := http.NewRequest(method, baseURL+"/.well-known/pgp-key.asc", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s PGP public key status without generated keypair=%d, want 404", method, response.StatusCode)
		}
	}
}

func TestTier2NoJSNativeFlows(t *testing.T) {
	ctx := browserContext(t)
	if _, err := chromedp.Run(ctx, disableScripts()); err != nil {
		t.Fatal(err)
	}
	if err := declineOptionalCookies(ctx); err != nil {
		t.Fatalf("dismiss consent banner without JavaScript: %v", err)
	}
	if err := chromedp.Do(ctx,
		chromedp.Navigate(baseURL+"/search"),
		chromedp.SendKeys("#query", "Japan"),
		chromedp.Click(`#searchForm button[type="submit"]`),
		chromedp.WaitVisible("#server-results-title"),
	); err != nil {
		t.Fatalf("SSR search failed without JavaScript: %v", err)
	}
	html, err := chromedp.Run(ctx, chromedp.OuterHTML("#main-content"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Japan") {
		t.Fatalf("search result missing Japan: %q", html)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/coordinates"), chromedp.SendKeys("#lat", "40.7128"), chromedp.SendKeys("#lon", "-74.0060"), chromedp.Click(`#coordForm button[type="submit"]`), chromedp.WaitVisible("#server-nearest-title")); err != nil {
		t.Fatalf("coordinate lookup failed without JavaScript: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/docs/graphql")); err != nil {
		t.Fatal(err)
	}
	query := `{ countries { name { common } cca2 } }`
	if err := chromedp.Do(ctx, chromedp.Click("#graphql-query"), chromedp.KeyEvent("a", chromedp.KeyModifiers(chromedp.ModifierCtrl)), chromedp.SendKeys("#graphql-query", query), chromedp.Submit("#graphql-form")); err != nil {
		t.Fatalf("GraphQL native POST failed: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.WaitVisible("#graphql-result")); err != nil {
		t.Fatalf("wait for GraphQL response page: %v", err)
	}
	html, err = chromedp.Run(ctx, chromedp.OuterHTML("html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Japan") {
		t.Fatalf("GraphQL result missing Japan: %q", html)
	}
	preferencesCtx, cancelPreferences := context.WithTimeout(ctx, 20*time.Second)
	defer cancelPreferences()
	if err := chromedp.Do(preferencesCtx, chromedp.Navigate(baseURL+"/server/preferences")); err != nil {
		t.Fatalf("open preferences without JavaScript: %v", err)
	}
	if err := chromedp.Do(preferencesCtx, chromedp.Click("#theme"), chromedp.KeyEvent("l"), chromedp.KeyEvent(kb.Enter)); err != nil {
		t.Fatalf("select a theme without JavaScript: %v", err)
	}
	if err := chromedp.Do(preferencesCtx, chromedp.Click("#preference-language"), chromedp.KeyEvent("f"), chromedp.KeyEvent(kb.Enter), chromedp.KeyEvent(kb.Escape)); err != nil {
		t.Fatalf("select a language without JavaScript: %v", err)
	}
	if err := chromedp.Do(preferencesCtx,
		chromedp.KeyEvent(kb.Tab),
		chromedp.KeyEvent(kb.Tab),
		chromedp.KeyEvent(kb.Tab),
		chromedp.KeyEvent(kb.Tab),
		chromedp.KeyEvent(kb.Tab),
		chromedp.KeyEvent(kb.Enter),
		chromedp.WaitVisible(".hero-title"),
	); err != nil {
		t.Fatalf("save preferences without JavaScript: %v", err)
	}
	html, err = chromedp.Run(preferencesCtx, chromedp.OuterHTML("html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `class="theme-light"`) || !strings.Contains(html, `lang="fr"`) {
		t.Fatalf("preferences were not applied: %s", html)
	}
	if err := chromedp.Do(preferencesCtx, chromedp.Navigate(baseURL+"/server/preferences")); err != nil {
		t.Fatalf("reopen preferences without JavaScript: %v", err)
	}
	html, err = chromedp.Run(preferencesCtx, chromedp.OuterHTML("html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `option value="auto"`) {
		t.Fatal("automatic theme preference is unavailable")
	}
}

func TestTier2OwnerTokenCreateAndRestoreWithoutJavaScript(t *testing.T) {
	ctx := browserContext(t)
	if _, err := chromedp.Run(ctx, disableScripts()); err != nil {
		t.Fatal(err)
	}
	if err := declineOptionalCookies(ctx); err != nil {
		t.Fatalf("dismiss consent banner without JavaScript: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/preferences/tokens"), chromedp.WaitVisible(`form[action="/server/preferences/token/create"]`), chromedp.Click(`form[action="/server/preferences/token/create"] button[type="submit"]`), chromedp.WaitVisible(`[data-token-secret]`)); err != nil {
		t.Fatalf("create owner token without JavaScript: %v", err)
	}
	primary, err := chromedp.Run(ctx, chromedp.TextContent(`[data-token-secret]`))
	if err != nil {
		t.Fatal(err)
	}
	primary = strings.TrimSpace(primary)
	if !strings.HasPrefix(primary, "own_") {
		t.Fatal("new-token form did not reveal the issued owner token")
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/preferences/tokens")); err != nil {
		t.Fatal(err)
	}
	maskedPage, err := chromedp.Run(ctx, chromedp.OuterHTML("html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(maskedPage, primary) {
		t.Fatal("owner token remained visible after its one-time display")
	}
	secondContext := browserContext(t)
	if _, err := chromedp.Run(secondContext, disableScripts()); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(secondContext, chromedp.Navigate(baseURL+"/server/preferences/tokens"), chromedp.SendKeys("#primary-token", primary), chromedp.Click(`form[action="/server/preferences/token/restore"] button[type="submit"]`)); err != nil {
		t.Fatalf("restore owner token without JavaScript: %v", err)
	}
	if _, err := chromedp.Run(secondContext, chromedp.WaitVisible(`#primary`)); err != nil {
		t.Fatalf("restored token did not unlock the token page: %v", err)
	}
}

func TestTier2SecurityDisclosurePagesWithoutJavaScript(t *testing.T) {
	ctx := browserContext(t)
	if _, err := chromedp.Run(ctx, disableScripts()); err != nil {
		t.Fatal(err)
	}
	for _, page := range []struct {
		path string
		want string
	}{
		{path: "/server/security", want: "Disclosure policy"},
		{path: "/server/security/policy", want: "Scope"},
		{path: "/server/security/thanks", want: "Acknowledged researchers"},
	} {
		if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+page.path), chromedp.WaitVisible("#main-content")); err != nil {
			t.Fatalf("navigate %s without JavaScript: %v", page.path, err)
		}
		text, err := chromedp.Run(ctx, chromedp.Text("#main-content"))
		if err != nil {
			t.Fatalf("read %s without JavaScript: %v", page.path, err)
		}
		if !strings.Contains(text, page.want) {
			t.Errorf("%s does not render %q without JavaScript: %q", page.path, page.want, text)
		}
	}
}

func TestTier3SecurityDisclosurePagesWithJavaScript(t *testing.T) {
	ctx := browserContext(t)
	for _, page := range []struct {
		path string
		want string
	}{
		{path: "/server/security", want: "Disclosure policy"},
		{path: "/server/security/policy", want: "Scope"},
		{path: "/server/security/thanks", want: "Acknowledged researchers"},
	} {
		if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+page.path), chromedp.WaitVisible("#main-content")); err != nil {
			t.Fatalf("navigate %s: %v", page.path, err)
		}
		text, err := chromedp.Run(ctx, chromedp.Text("#main-content"))
		if err != nil {
			t.Fatalf("read %s: %v", page.path, err)
		}
		if !strings.Contains(text, page.want) {
			t.Errorf("%s does not render %q: %q", page.path, page.want, text)
		}
	}
}

func TestTier3FullJSFlowsAndResponsiveShell(t *testing.T) {
	ctx := browserContext(t)
	var browserErrors []string
	var browserErrorsMu sync.Mutex
	requestURLs := make(map[network.RequestID]string)
	if err := chromedp.Do(ctx,
		protocolAction(log.Enable, cdp.Empty{}),
		protocolAction(network.Enable, network.EnableParams{}),
		protocolAction(runtime.Enable, cdp.Empty{}),
	); err != nil {
		t.Fatal(err)
	}
	listenTargetEvent(ctx, runtime.ExceptionThrown, func(entry runtime.EventExceptionThrown) {
		appendBrowserError(&browserErrorsMu, &browserErrors, entry.ExceptionDetails.Text)
	})
	listenTargetEvent(ctx, log.EntryAdded, func(entry log.EventEntryAdded) {
		if entry.Entry != nil && entry.Entry.Level == log.EntryLevelError {
			appendBrowserError(&browserErrorsMu, &browserErrors, entry.Entry.Text)
		}
	})
	listenTargetEvent(ctx, network.RequestWillBeSent, func(entry network.EventRequestWillBeSent) {
		browserErrorsMu.Lock()
		requestURLs[entry.RequestID] = entry.Request.URL
		browserErrorsMu.Unlock()
	})
	listenTargetEvent(ctx, network.LoadingFailed, func(entry network.EventLoadingFailed) {
		browserErrorsMu.Lock()
		requestURL := requestURLs[entry.RequestID]
		browserErrorsMu.Unlock()
		if strings.Contains(requestURL, "/static/") || strings.Contains(requestURL, "/api/") {
			appendBrowserError(&browserErrorsMu, &browserErrors, entry.ErrorText)
		}
	})
	listenTargetEvent(ctx, network.ResponseReceived, func(entry network.EventResponseReceived) {
		if entry.Response != nil && entry.Response.Status >= 400 && (strings.Contains(entry.Response.URL, "/static/") || strings.Contains(entry.Response.URL, "/api/")) {
			appendBrowserError(&browserErrorsMu, &browserErrors, fmt.Sprintf("%s returned HTTP %d", entry.Response.URL, entry.Response.Status))
		}
	})
	if err := declineOptionalCookies(ctx); err != nil {
		t.Fatalf("dismiss consent banner before browser flows: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/search"), chromedp.WaitVisible("#query"), chromedp.SetValue("#query", "Japan"), chromedp.Click(`#searchForm button[type="submit"]`), chromedp.WaitVisible("#results"), chromedp.WaitVisible("#resultsList .result-card")); err != nil {
		t.Fatalf("JavaScript search failed: %v", err)
	}
	text, err := chromedp.Run(ctx, chromedp.Text("#resultsList"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Japan") {
		t.Fatalf("JavaScript search result missing Japan: %q", text)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/coordinates"), chromedp.SetValue("#lat", "40.7128"), chromedp.SetValue("#lon", "-74.0060"), chromedp.Click(`#coordForm button[type="submit"]`), chromedp.WaitVisible("#resultCard .result-card")); err != nil {
		t.Fatalf("JavaScript coordinate lookup failed: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/docs/graphql"), chromedp.SetValue("#graphql-query", `{ countries { name { common } cca2 } }`), chromedp.Submit("#graphql-form")); err != nil {
		t.Fatalf("JavaScript GraphQL explorer failed: %v", err)
	}
	text, err = chromedp.Run(ctx, chromedp.Text("#graphql-result"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Japan") {
		t.Fatalf("GraphQL explorer result missing Japan: %q", text)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/"), setViewport(375, 812), chromedp.Click(".nav-toggle")); err != nil {
		t.Fatalf("responsive navigation failed: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.WaitVisible(".nav-panel a")); err != nil {
		t.Fatalf("mobile navigation links are not visible: %v", err)
	}
	menuOpen, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.querySelector('#nav-toggle').checked`))
	if err != nil {
		t.Fatal(err)
	}
	if !menuOpen {
		t.Fatal("responsive navigation did not open")
	}
	overflow, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.documentElement.scrollWidth > document.documentElement.clientWidth && getComputedStyle(document.documentElement).overflowX !== 'clip'`))
	if err != nil {
		t.Fatal(err)
	}
	if overflow {
		overflowing, err := chromedp.Run(ctx, chromedp.Evaluate[string](`JSON.stringify(Array.from(document.querySelectorAll('body *')).filter(element => (element.getBoundingClientRect().right > document.documentElement.clientWidth + 1 || element.getBoundingClientRect().left < -1)).slice(0, 5).map(element => { const chain = []; let current = element; while (current && chain.length < 5) { const rect = current.getBoundingClientRect(); chain.push({tag: current.tagName, className: typeof current.className === 'string' ? current.className : '', left: Math.round(rect.left), right: Math.round(rect.right), width: Math.round(rect.width), scrollWidth: current.scrollWidth, clientWidth: current.clientWidth, overflowX: getComputedStyle(current).overflowX}); current = current.parentElement; } return chain; }))`))
		if err != nil {
			t.Fatalf("mobile page has horizontal overflow at 375px; inspect elements failed: %v", err)
		}
		t.Fatalf("mobile page has horizontal overflow at 375px: %s", overflowing)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/preferences"), chromedp.SetValue("#theme", "light"), chromedp.SetValue("#preference-language", "ja"), chromedp.Submit(`main form[action="/server/preferences"]`), chromedp.WaitReady("html")); err != nil {
		t.Fatalf("JavaScript light theme preference failed: %v", err)
	}
	lightBackground, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.documentElement).getPropertyValue('--color-bg-primary').trim()`))
	if err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/preferences"), chromedp.SetValue("#theme", "dark"), chromedp.SetValue("#preference-language", "ja"), chromedp.Submit(`main form[action="/server/preferences"]`), chromedp.WaitReady("html")); err != nil {
		t.Fatalf("JavaScript dark theme preference failed: %v", err)
	}
	darkBackground, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.documentElement).getPropertyValue('--color-bg-primary').trim()`))
	if err != nil {
		t.Fatal(err)
	}
	if lightBackground == darkBackground {
		t.Fatalf("light and dark themes have the same computed background: %q", lightBackground)
	}
	if err := chromedp.Do(ctx, emulateColorScheme("dark"), chromedp.Navigate(baseURL+"/server/preferences"), chromedp.SetValue("#theme", "auto"), chromedp.SetValue("#preference-language", "ja"), chromedp.Submit(`main form[action="/server/preferences"]`), chromedp.WaitReady("html")); err != nil {
		t.Fatalf("JavaScript automatic theme preference failed: %v", err)
	}
	theme, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.className`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(theme, "theme-auto") {
		t.Fatalf("automatic theme was not applied: %q", theme)
	}
	autoDarkBackground, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.documentElement).getPropertyValue('--color-bg-primary').trim()`))
	if err != nil {
		t.Fatal(err)
	}
	if autoDarkBackground != darkBackground {
		t.Fatalf("automatic dark scheme background=%q, want %q", autoDarkBackground, darkBackground)
	}
	if _, err := chromedp.Run(ctx, emulateColorScheme("light")); err != nil {
		t.Fatal(err)
	}
	autoLightBackground, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.documentElement).getPropertyValue('--color-bg-primary').trim()`))
	if err != nil {
		t.Fatal(err)
	}
	if autoLightBackground != lightBackground {
		t.Fatalf("automatic light scheme background=%q, want %q", autoLightBackground, lightBackground)
	}
	lang, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.lang`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(theme, "theme-auto") || lang != "ja" {
		t.Fatalf("JavaScript preferences did not apply: theme=%q lang=%q", theme, lang)
	}
	consentCtx := browserContext(t)
	if err := chromedp.Do(consentCtx, chromedp.Navigate(baseURL+"/"), chromedp.WaitVisible(`form[action="/server/consent"]`), chromedp.Click(`.cookie-buttons form:nth-of-type(2) button[type="submit"]`), chromedp.WaitNotPresent(".cookie-banner")); err != nil {
		t.Fatalf("JavaScript consent acceptance failed: %v", err)
	}
	browserErrorsMu.Lock()
	defer browserErrorsMu.Unlock()
	if len(browserErrors) != 0 {
		t.Fatalf("browser console or request errors: %s", strings.Join(browserErrors, "; "))
	}
}

func TestTier3OwnerTokenIssueAndOneTimeDisplay(t *testing.T) {
	ctx := browserContext(t)
	if err := declineOptionalCookies(ctx); err != nil {
		t.Fatalf("dismiss consent banner before token flow: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/preferences/tokens"), chromedp.WaitVisible(`form[action="/server/preferences/token/create"]`), chromedp.Click(`form[action="/server/preferences/token/create"] button[type="submit"]`), chromedp.WaitVisible(`[data-token-secret]`)); err != nil {
		t.Fatalf("issue an owner token in the browser: %v", err)
	}
	primary, err := chromedp.Run(ctx, chromedp.TextContent(`[data-token-secret]`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(primary), "own_") {
		t.Fatal("the token page did not show the newly issued token")
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/server/preferences/tokens")); err != nil {
		t.Fatal(err)
	}
	maskedPage, err := chromedp.Run(ctx, chromedp.OuterHTML("html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(maskedPage, strings.TrimSpace(primary)) {
		t.Fatal("the token page exposed the full token after its one-time display")
	}
}

func TestTier2ConsentAndCSRF(t *testing.T) {
	ctx := browserContext(t)
	if _, err := chromedp.Run(ctx, disableScripts()); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/"), chromedp.WaitVisible(`form[action="/server/consent"]`), chromedp.Click(`form[action="/server/consent"] button[type="submit"]`), chromedp.WaitNotPresent(".cookie-banner")); err != nil {
		t.Fatalf("no-JS consent decline failed: %v", err)
	}
	html, err := chromedp.Run(ctx, chromedp.OuterHTML("html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `class="cookie-banner"`) {
		t.Fatal("consent banner remains after decline")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/server/preferences", strings.NewReader(url.Values{"theme": {"light"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("preference POST without CSRF token status=%d", response.StatusCode)
	}
}

func TestTier2ContactReportReviewAndSecurityEntry(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	chooser, err := client.Get(baseURL + "/server/contact")
	if err != nil {
		t.Fatal(err)
	}
	chooserBody, _ := io.ReadAll(chooser.Body)
	_ = chooser.Body.Close()
	chooserHTML := string(chooserBody)
	mainStart, mainEnd := strings.Index(chooserHTML, "<main"), strings.Index(chooserHTML, "</main>")
	chooserMain := ""
	if mainStart >= 0 && mainEnd >= mainStart {
		chooserMain = chooserHTML[mainStart:mainEnd]
	}
	if chooser.StatusCode != http.StatusOK || chooserMain == "" || strings.Contains(chooserMain, "<input") || strings.Contains(chooserMain, "<form") || !strings.Contains(chooserMain, "Report a security vulnerability") || !strings.Contains(chooserMain, "Abuse and copyright reports") {
		t.Fatalf("contact chooser request=%s response=%s status=%d body=%q", chooser.Request.URL, chooser.Header.Get("Location"), chooser.StatusCode, chooserBody)
	}

	formResponse, err := client.Get(baseURL + "/server/contact?type=privacy")
	if err != nil {
		t.Fatal(err)
	}
	formBody, _ := io.ReadAll(formResponse.Body)
	_ = formResponse.Body.Close()
	formHTML := string(formBody)
	if formResponse.StatusCode != http.StatusOK || !strings.Contains(formHTML, `name="request_kind"`) || strings.Contains(formHTML, `name="reported_url"`) || formResponse.Header.Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("privacy form status=%d headers=%v body=%q", formResponse.StatusCode, formResponse.Header, formHTML)
	}
	inputValue := func(name string) string {
		expression := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`)
		found := expression.FindStringSubmatch(formHTML)
		if len(found) != 2 {
			t.Fatalf("form missing %s", name)
		}
		return found[1]
	}
	challenge := regexp.MustCompile(`Solve this arithmetic question: ([0-9]+) ([+-]) ([0-9]+)`).FindStringSubmatch(html.UnescapeString(formHTML))
	if len(challenge) != 4 {
		t.Fatalf("form simple challenge missing: %s", formHTML)
	}
	a, _ := strconv.Atoi(challenge[1])
	b, _ := strconv.Atoi(challenge[3])
	answer := a + b
	if challenge[2] == "-" {
		answer = a - b
	}
	csrf, formToken, captchaID := inputValue("csrf_token"), inputValue("form_token"), inputValue("captcha_id")
	form := url.Values{"csrf_token": {csrf}, "type": {"privacy"}, "form_token": {formToken}, "captcha_id": {captchaID}, "captcha_answer": {strconv.Itoa(answer)}, "name": {"E2E Researcher"}, "email": {"researcher@example.test"}, "request_kind": {"access"}, "subject": {"Privacy request"}, "message": {"Please review this test submission."}, "website": {"automated trap check"}, "step": {"submit"}}
	time.Sleep(4 * time.Second)
	request, err := http.NewRequest(http.MethodPost, baseURL+"/server/contact", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", baseURL)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, cookie := range formResponse.Cookies() {
		request.AddCookie(cookie)
	}
	review, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reviewBody, _ := io.ReadAll(review.Body)
	_ = review.Body.Close()
	if review.StatusCode != http.StatusOK || !strings.Contains(string(reviewBody), "Review your report") || !strings.Contains(string(reviewBody), `name="review_token"`) {
		t.Fatalf("contact review status=%d body=%q", review.StatusCode, reviewBody)
	}
	reviewToken := regexp.MustCompile(`name="review_token" value="([^"]+)"`).FindStringSubmatch(string(reviewBody))
	reviewFormToken := regexp.MustCompile(`name="form_token" value="([^"]+)"`).FindStringSubmatch(string(reviewBody))
	if len(reviewToken) != 2 || len(reviewFormToken) != 2 {
		t.Fatalf("review is missing confirmation tokens: %s", reviewBody)
	}
	time.Sleep(2 * time.Second)
	confirm := url.Values{"csrf_token": {csrf}, "type": {"privacy"}, "form_token": {reviewFormToken[1]}, "review_token": {reviewToken[1]}, "honeypot_flag": {"d"}, "name": {"E2E Researcher"}, "email": {"researcher@example.test"}, "request_kind": {"access"}, "subject": {"Privacy request"}, "message": {"Please review this test submission."}, "confirmed": {"on"}, "step": {"confirm"}}
	confirmRequest, err := http.NewRequest(http.MethodPost, baseURL+"/server/contact", strings.NewReader(confirm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	confirmRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirmRequest.Header.Set("Origin", baseURL)
	confirmRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, cookie := range formResponse.Cookies() {
		confirmRequest.AddCookie(cookie)
	}
	for _, cookie := range review.Cookies() {
		confirmRequest.AddCookie(cookie)
	}
	confirmed, err := client.Do(confirmRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = confirmed.Body.Close()
	if confirmed.StatusCode != http.StatusSeeOther || confirmed.Header.Get("Location") != "/server/contact?sent=1" {
		t.Fatalf("honeypot confirmation status=%d location=%q", confirmed.StatusCode, confirmed.Header.Get("Location"))
	}

	securityText, err := client.Get(baseURL + "/.well-known/security.txt")
	if err != nil {
		t.Fatal(err)
	}
	secBody, _ := io.ReadAll(securityText.Body)
	_ = securityText.Body.Close()
	securityLink := regexp.MustCompile(`Contact: (https?://[^\s]+/server/contact\?security_id=[0-9a-f]{16})`).FindStringSubmatch(string(secBody))
	if securityText.StatusCode != http.StatusOK || len(securityLink) != 2 {
		t.Fatalf("security.txt report link missing: status=%d body=%q", securityText.StatusCode, secBody)
	}
	securityForm, err := client.Get(securityLink[1])
	if err != nil {
		t.Fatal(err)
	}
	securityBody, _ := io.ReadAll(securityForm.Body)
	_ = securityForm.Body.Close()
	if securityForm.StatusCode != http.StatusOK || !strings.Contains(string(securityBody), `name="component"`) || !strings.Contains(string(securityBody), `name="coordinated_disclosure"`) {
		t.Fatalf("security form status=%d body=%q", securityForm.StatusCode, securityBody)
	}
}

func disableScripts() chromedp.Action[cdp.Empty] {
	return func(ctx context.Context, target *chromedp.Target) (cdp.Empty, error) {
		return cdp.Call(ctx, target, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: true})
	}
}

func declineOptionalCookies(ctx context.Context) error {
	return chromedp.Do(ctx,
		chromedp.Navigate(baseURL+"/"),
		chromedp.WaitVisible(`form[action="/server/consent"]`),
		chromedp.Click(`.cookie-buttons form:first-of-type button[type="submit"]`),
		chromedp.WaitNotPresent(".cookie-banner"),
	)
}

func listenTargetEvent[E any](ctx context.Context, event cdp.Event[E], handler func(E)) {
	go func() {
		for value, err := range chromedp.Events(ctx, event) {
			if err != nil {
				return
			}
			handler(value)
		}
	}()
}

func appendBrowserError(mu *sync.Mutex, errors *[]string, message string) {
	if message == "" {
		return
	}
	mu.Lock()
	*errors = append(*errors, message)
	mu.Unlock()
}

func protocolAction[P any](command cdp.Command[P, cdp.Empty], params P) chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, target *chromedp.Target) (chromedp.Void, error) {
		_, err := cdp.Call(ctx, target, command, params)
		return chromedp.Void{}, err
	}
}

func emulateColorScheme(scheme string) chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, target *chromedp.Target) (chromedp.Void, error) {
		params := emulation.SetEmulatedMediaParams{
			Features: []*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: scheme}},
		}
		_, err := cdp.Call(ctx, target, emulation.SetEmulatedMedia, params)
		return chromedp.Void{}, err
	}
}

func setViewport(width, height int64) chromedp.Action[chromedp.Void] {
	return func(ctx context.Context, target *chromedp.Target) (chromedp.Void, error) {
		params := emulation.SetDeviceMetricsOverrideParams{Width: width, Height: height, DeviceScaleFactor: 1, Mobile: false}
		_, err := cdp.Call(ctx, target, emulation.SetDeviceMetricsOverride, params)
		return chromedp.Void{}, err
	}
}

func browserContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(browserRoot, 90*time.Second)
	ctx, closeBrowser := chromedp.NewContext(ctx, chromedp.WithNewBrowserContext())
	t.Cleanup(func() {
		closeBrowser()
		cancel()
	})
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		name := strings.NewReplacer("/", "-", " ", "_").Replace(t.Name())
		var html string
		pageCtx, pageCancel := context.WithTimeout(ctx, 5*time.Second)
		defer pageCancel()
		html, _ = chromedp.Run(pageCtx, chromedp.OuterHTML("html"))
		if html != "" {
			_ = os.WriteFile(filepath.Join(artifactDir, "failures", name+".html"), []byte(html), 0600)
		}
		if screenshot, err := chromedp.Run(pageCtx, chromedp.CaptureScreenshot()); err == nil {
			_ = os.WriteFile(filepath.Join(artifactDir, "failures", name+".png"), screenshot, 0600)
		}
	})
	return ctx
}
