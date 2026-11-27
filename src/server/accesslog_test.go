package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/testutil"
)

func TestFormatAccessLogCustomExpandsDocumentedVariables(t *testing.T) {
	started := time.Date(2026, time.November, 26, 15, 4, 5, 120000000, time.FixedZone("test", -5*60*60))
	request := httptest.NewRequest("GET", "https://countries.example.test/api/v1/countries?limit=2", nil)
	request.RemoteAddr = "192.0.2.4:1234"
	request.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	request.Header.Set("User-Agent", "agent\r\nnoise")
	request.Header.Set("Referer", "https://example.test/source")
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, "req-123"))
	format := "{time}|{date}|{datetime}|{remote_ip}|{method}|{path}|{query}|{status}|{bytes}|{latency}|{latency_ms}|{user_agent}|{referer}|{request_id}|{fqdn}|{protocol}|{tls_version}|{country}|{asn}"
	got := formatAccessLog("custom", format, request, started, 201, 42, 1500*time.Microsecond, "US", "15169")
	for _, expected := range []string{
		"15:04:05", "2026-11-26", "192.0.2.4", "GET", "/api/v1/countries", "limit=2", "201", "42", "1.5ms", "1.500",
		"agent  noise", "https://example.test/source", "req-123", "countries.example.test", "HTTP/1.1", "TLS1.3", "US", "15169",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("custom access log %q does not contain %q", got, expected)
		}
	}
	if strings.ContainsAny(got, "\r\n\x00") {
		t.Fatalf("custom access log contains a control character: %q", got)
	}
}

func TestFormatAccessLogFallsBackToApacheWhenCustomFormatIsEmpty(t *testing.T) {
	request := httptest.NewRequest("GET", "http://example.test/health", nil)
	request.RemoteAddr = "127.0.0.1:8080"
	got := formatAccessLog("custom", "", request, time.Date(2026, 11, 26, 15, 4, 5, 0, time.UTC), 200, 12, time.Millisecond)
	if !strings.Contains(got, `"GET /health HTTP/1.1" 200 12`) {
		t.Fatalf("empty custom format fallback = %q, want Apache style", got)
	}
}

func TestAccessLogRedactsSensitiveQueryParametersAcrossFormats(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://countries.example.test/report?Token=raw-token&SESSION=raw-session&Code=raw-code&KEY=raw-key&Password=raw-password&secret=raw-secret&auth=raw-auth&pwd=raw-pwd&API_KEY=raw-api-key&ApIkEy=raw-apikey&ACCESS_TOKEN=raw-access-token&refresh_token=raw-refresh-token&visible=keep-me", nil)
	request.RemoteAddr = "192.0.2.5:1234"
	request.Header.Set("Referer", "https://origin.example.test/link?password=raw-referer-secret&visible=referrer")
	secretValues := []string{
		"raw-token", "raw-session", "raw-code", "raw-key", "raw-password", "raw-secret", "raw-auth", "raw-pwd",
		"raw-api-key", "raw-apikey", "raw-access-token", "raw-refresh-token", "raw-referer-secret",
	}
	for _, test := range []struct {
		format string
		custom string
	}{
		{format: "apache"},
		{format: "nginx"},
		{format: "json"},
		{format: "custom", custom: "{path}?{query} {referer}"},
	} {
		got := formatAccessLog(test.format, test.custom, request, time.Now(), 200, 12, time.Millisecond)
		for _, secret := range secretValues {
			if strings.Contains(got, secret) {
				t.Errorf("%s access format leaked %q: %s", test.format, secret, got)
			}
		}
		if test.format != "json" && (!strings.Contains(got, "keep-me") || !strings.Contains(got, "%5Bredacted%5D")) {
			t.Errorf("%s access format did not preserve safe values and visibly redact sensitive values: %s", test.format, got)
		}
	}
}

func TestRedactSensitiveQueryFailsClosedOnMalformedQuery(t *testing.T) {
	if got := redactSensitiveQuery("token=raw-secret&broken=%zz"); got != "[redacted]" {
		t.Fatalf("malformed query redaction = %q, want entire query redacted", got)
	}
}

func TestWriteRotatingLogUsesPrivateFileAndRetention(t *testing.T) {
	directory := testutil.TempDir(t)
	firstTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := writeRotatingLog(directory, "access.log", "first", "daily", "none", firstTime); err != nil {
		t.Fatalf("write first access line: %v", err)
	}
	path := filepath.Join(directory, "access.log")
	if err := os.Chtimes(path, firstTime, firstTime); err != nil {
		t.Fatalf("set access log time: %v", err)
	}
	if err := writeRotatingLog(directory, "access.log", "second", "daily", "none", firstTime.Add(24*time.Hour)); err != nil {
		t.Fatalf("rotate access log: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "second\n" {
		t.Fatalf("active access log = %q, want only new entry", contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("access log mode = %04o, want 0600", info.Mode().Perm())
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "access.log" {
		t.Fatalf("retained logs = %v, want only access.log with keep=none", entries)
	}
}

func TestWriteRotatingLogRetainsNewestCount(t *testing.T) {
	directory := testutil.TempDir(t)
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(directory, "access.log")
	if err := writeRotatingLog(directory, "access.log", "one", "daily", "1", start); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, start, start); err != nil {
		t.Fatal(err)
	}
	second := start.Add(24 * time.Hour)
	if err := writeRotatingLog(directory, "access.log", "two", "daily", "1", second); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, second, second); err != nil {
		t.Fatal(err)
	}
	if err := writeRotatingLog(directory, "access.log", "three", "daily", "1", second.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	archives := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "access-") {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("rotated archive count = %d, want 1; entries=%v", archives, entries)
	}
}

func TestAccessLogRotationRecognizesSizePolicies(t *testing.T) {
	if size, ok := parseLogSize("1MB"); !ok || size != 1<<20 {
		t.Fatalf("parseLogSize(1MB) = (%d, %t), want (%d, true)", size, ok, 1<<20)
	}
	if size, ok := parseLogSize("2GB"); !ok || size != 2<<30 {
		t.Fatalf("parseLogSize(2GB) = (%d, %t), want (%d, true)", size, ok, 2<<30)
	}
	directory := testutil.TempDir(t)
	path := filepath.Join(directory, "access.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !shouldRotateLog(info, 1, "weekly,1MB", time.Now().Add(-time.Hour)) {
		t.Fatal("size policy did not rotate at the configured limit")
	}
	if shouldRotateLog(info, 1, "never", time.Now().AddDate(5, 0, 0)) {
		t.Fatal("never policy rotated despite a large time difference")
	}
}

func TestAccessLogConcurrentWritesRemainComplete(t *testing.T) {
	directory := testutil.TempDir(t)
	cfg := config.DefaultConfig()
	cfg.Server.Logging.Defaults.Rotate = "never"
	server := &Server{config: cfg}
	server.SetAuditLogDir(directory)
	const workers, perWorker = 8, 20
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for entry := 0; entry < perWorker; entry++ {
				if err := server.writeAccessLog("access", fmt.Sprintf("%d-%d", worker, entry), time.Now()); err != nil {
					t.Errorf("write access log line: %v", err)
				}
			}
		}(worker)
	}
	wait.Wait()
	contents, err := os.ReadFile(filepath.Join(directory, "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := bytes.Count(contents, []byte("\n")); lines != workers*perWorker {
		t.Fatalf("access log contains %d complete lines, want %d", lines, workers*perWorker)
	}
}

func TestFailedHealthRequestWritesAccessAndErrorLogs(t *testing.T) {
	directory := testutil.TempDir(t)
	server := &Server{config: config.DefaultConfig()}
	server.SetAuditLogDir(directory)
	var console bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&console)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	handler := requestLogMiddleware(server)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	request := httptest.NewRequest("GET", "/server/healthz", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	for _, name := range []string{"access.log", "error.log"} {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(contents) == 0 || !bytes.HasSuffix(contents, []byte("\n")) {
			t.Errorf("%s is empty or missing its final newline: %q", name, contents)
		}
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", name, info.Mode().Perm())
		}
	}
	if !strings.Contains(console.String(), "ERROR:") {
		t.Fatalf("failed health request console output = %q, want error severity", console.String())
	}
}

func TestSuccessfulHealthRequestIsSuppressedFromLogFiles(t *testing.T) {
	directory := testutil.TempDir(t)
	server := &Server{config: config.DefaultConfig()}
	server.SetAuditLogDir(directory)
	var console bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&console)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	handler := requestLogMiddleware(server)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest("GET", "/server/healthz", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if _, err := os.Stat(filepath.Join(directory, "access.log")); !os.IsNotExist(err) {
		t.Fatalf("successful health request access.log stat error = %v, want file absent", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "error.log")); !os.IsNotExist(err) {
		t.Fatalf("successful health request error.log stat error = %v, want file absent", err)
	}
	if console.Len() != 0 {
		t.Fatalf("successful health request console output = %q, want empty", console.String())
	}
}
