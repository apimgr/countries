package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
)

func TestHealthJSONFieldOrderAndRequiredKeys(t *testing.T) {
	response := HealthResponse{
		Project: HealthProjectInfo{Name: "Countries"}, Status: "healthy", Version: "1.2.3", GoVersion: "go1.test",
		Build: HealthBuildInfo{Commit: "abc1234", Date: "2026-11-22T12:00:00Z"}, Uptime: "45s", Mode: "production",
		Timestamp: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC), Cluster: HealthClusterInfo{},
		Features: HealthFeatures{Tor: HealthOverlayInfo{Status: "disabled"}, I2P: HealthI2PInfo{Status: "disabled", State: "disabled", Provider: "none"}},
		Checks:   HealthChecks{Database: "ok", Cache: "ok", Disk: "ok", Scheduler: "ok", Datasets: []HealthCheck{{Key: "data_countries", Status: "ok"}}},
		Stats:    HealthStats{RequestsTotal: 1, Requests24h: 1, ActiveConns: 1, Project: []HealthStatValue{{Key: "searches_total", Value: 2}}},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(body)
	keys := []string{`"project"`, `"status"`, `"version"`, `"go_version"`, `"build"`, `"uptime"`, `"mode"`, `"timestamp"`, `"cluster"`, `"features"`, `"checks"`, `"stats"`}
	previous := -1
	for _, key := range keys {
		index := strings.Index(text, key)
		if index <= previous {
			t.Fatalf("JSON key %s is missing or out of order in %s", key, text)
		}
		previous = index
	}
	for _, key := range []string{`"requests_total"`, `"requests_24h"`, `"active_connections"`, `"searches_total"`} {
		if !strings.Contains(text, key) {
			t.Errorf("JSON is missing required stats key %s: %s", key, text)
		}
	}
	checksStart := strings.Index(text, `"checks":{`)
	statsStart := strings.Index(text, `"stats":{`)
	checksJSON := text[checksStart:statsStart]
	checkKeys := []string{`"database"`, `"cache"`, `"disk"`, `"scheduler"`, `"data_countries"`}
	previous = -1
	for _, key := range checkKeys {
		index := strings.Index(checksJSON, key)
		if index <= previous {
			t.Fatalf("check key %s is missing or out of order in %s", key, checksJSON)
		}
		previous = index
	}
}

func TestHealthTextFormatsKeepMachineAndFrontendLayoutsSeparate(t *testing.T) {
	response := HealthResponse{
		Project: HealthProjectInfo{Name: "Countries", Tagline: "Reference data"}, Status: "healthy", Version: "1.2.3", GoVersion: "go1.test",
		Build: HealthBuildInfo{Commit: "abc1234", Date: "2026-11-22T12:00:00Z"}, Uptime: "1d 1h", Mode: "production",
		Timestamp: time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC),
		Features:  HealthFeatures{Tor: HealthOverlayInfo{Status: "disabled"}, I2P: HealthI2PInfo{Status: "disabled", State: "disabled", Provider: "none"}},
		Checks:    HealthChecks{Database: "ok", Cache: "ok", Disk: "ok", Scheduler: "ok"},
		Stats:     HealthStats{RequestsTotal: 1234, Requests24h: 10, ActiveConns: 1},
	}
	apiText := response.apiText()
	apiFields := []string{"project.name:", "status:", "version:", "go_version:", "build.commit:", "build.date:", "uptime:", "mode:", "timestamp:", "cluster.enabled:", "features.tor:", "features.i2p:", "features.geoip:", "checks.database:", "checks.cache:", "checks.disk:", "checks.scheduler:", "stats.requests_total:", "stats.requests_24h:", "stats.active_connections:"}
	previous := -1
	for _, field := range apiFields {
		index := strings.Index(apiText, field)
		if index <= previous {
			t.Fatalf("API text field %q is missing or out of order in %q", field, apiText)
		}
		previous = index
	}
	frontendText := response.frontendText("en")
	if strings.Contains(frontendText, "project.name:") || strings.Contains(frontendText, "uptime: 1d 1h") || !strings.Contains(frontendText, "1 day 1 hour") {
		t.Fatalf("frontend text is not the localized human layout: %q", frontendText)
	}
}

func TestHealthUptimeAndTimestampFormats(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		compact  string
		human    string
	}{
		{45 * time.Second, "45s", "45 seconds"},
		{24 * time.Minute, "24m", "24 minutes"},
		{2*time.Hour + 16*time.Minute, "2h 16m", "2 hours 16 minutes"},
		{time.Hour*24 + time.Hour + time.Minute*4, "1d 1h 4m", "1 day 1 hour"},
	} {
		if got := compactHealthUptime(test.duration); got != test.compact {
			t.Errorf("compactHealthUptime(%s) = %q, want %q", test.duration, got, test.compact)
		}
		if got := humanHealthUptime(test.compact); got != test.human {
			t.Errorf("humanHealthUptime(%q) = %q, want %q", test.compact, got, test.human)
		}
	}
	timestamp := time.Date(2026, time.November, 22, 12, 34, 56, 987654321, time.FixedZone("offset", -5*60*60))
	if got := healthBuildTimestamp(timestamp.Format(time.RFC3339Nano)); got != "2026-11-22T17:34:56Z" {
		t.Errorf("healthBuildTimestamp() = %q, want whole-second UTC timestamp", got)
	}
	if got := humanHealthTimestamp(timestamp); got != "November 22, 2026 at 17:34:56 UTC" {
		t.Errorf("humanHealthTimestamp() = %q", got)
	}
}

func TestHealthFormatNegotiationAndStatusCodes(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		accept      string
		userAgent   string
		apiFormat   string
		frontFormat string
	}{
		{"default api browser", "/api/v1/server/healthz", "", "Mozilla/5.0", "json", "html"},
		{"empty user agent", "/api/v1/server/healthz", "", "", "text", "text"},
		{"api json beats curl", "/api/v1/server/healthz", "application/json", "curl/8", "json", "json"},
		{"api curl", "/api/v1/server/healthz", "", "curl/8", "text", "text"},
		{"api text file", "/api/v1/server/healthz.txt", "application/json", "", "text", "json"},
		{"frontend browser", "/server/healthz", "text/html", "Mozilla/5.0", "json", "html"},
		{"frontend default browser", "/server/healthz", "", "Mozilla/5.0", "json", "html"},
		{"frontend curl", "/server/healthz", "", "curl/8", "text", "text"},
		{"frontend explicit json", "/server/healthz", "application/json", "curl/8", "json", "json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, test.path, nil)
			r.Header.Set("Accept", test.accept)
			r.Header.Set("User-Agent", test.userAgent)
			if got := healthAPIFormat(r); got != test.apiFormat {
				t.Errorf("healthAPIFormat() = %q, want %q", got, test.apiFormat)
			}
			if got := healthFrontendFormat(r); got != test.frontFormat {
				t.Errorf("healthFrontendFormat() = %q, want %q", got, test.frontFormat)
			}
		})
	}
	for _, test := range []struct {
		status string
		code   int
	}{
		{"healthy", http.StatusOK}, {"degraded", http.StatusOK}, {"restart_required", http.StatusOK},
		{"unhealthy", http.StatusServiceUnavailable}, {"maintenance", http.StatusServiceUnavailable}, {"shutting_down", http.StatusServiceUnavailable},
	} {
		if got := healthResponseStatusCode(test.status); got != test.code {
			t.Errorf("healthResponseStatusCode(%q) = %d, want %d", test.status, got, test.code)
		}
	}
}

func TestHealthStatusPrecedence(t *testing.T) {
	datasetError := []HealthCheck{{Key: "data_countries", Status: "error", Critical: true}}
	for _, test := range []struct {
		name         string
		shuttingDown bool
		maintenance  bool
		database     string
		cache        string
		disk         string
		scheduler    string
		datasets     []HealthCheck
		pending      bool
		want         string
	}{
		{name: "healthy", database: "ok", cache: "ok", disk: "ok", scheduler: "ok", want: "healthy"},
		{name: "restart required", database: "ok", cache: "ok", disk: "ok", scheduler: "ok", pending: true, want: "restart_required"},
		{name: "degraded beats restart", database: "ok", cache: "ok", disk: "ok", scheduler: "error", pending: true, want: "degraded"},
		{name: "unhealthy beats degraded", database: "ok", cache: "ok", disk: "error", scheduler: "error", pending: true, want: "unhealthy"},
		{name: "maintenance beats unhealthy", maintenance: true, database: "error", disk: "error", want: "maintenance"},
		{name: "shutdown beats maintenance", shuttingDown: true, maintenance: true, database: "error", disk: "error", want: "shutting_down"},
		{name: "critical dataset is unhealthy", database: "ok", cache: "ok", disk: "ok", scheduler: "ok", datasets: datasetError, want: "unhealthy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := overallHealthStatus(test.shuttingDown, test.maintenance, test.database, test.cache, test.disk, test.scheduler, test.datasets, test.pending)
			if got != test.want {
				t.Errorf("overallHealthStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFrontendCurlHealthUsesHumanLayout(t *testing.T) {
	service, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService() error = %v", err)
	}
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.DefaultConfig()
	cfg.Server.GeoIP.Enabled = false
	s := New(service, cfg, "0.0.0.0", "8080", "1.2.3", "2026-11-22", "abc1234", db)
	s.SetMetricsDataDir(testutil.TempDir(t))
	s.SetSchedulerRunning(true)
	r := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	r.Header.Set("User-Agent", "curl/8.0")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("curl health response = status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if strings.Contains(w.Body.String(), "project.name:") || !strings.Contains(w.Body.String(), "Status:") || !strings.Contains(w.Body.String(), "Features") {
		t.Fatalf("curl response is not the frontend human layout: %q", w.Body.String())
	}
}

func TestOptionalRootHealthAliasUsesFrontendHandler(t *testing.T) {
	service, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService() error = %v", err)
	}
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.DefaultConfig()
	cfg.Server.Healthz.Root.Enabled = true
	cfg.Server.GeoIP.Enabled = false
	s := New(service, cfg, "0.0.0.0", "8080", "1.2.3", "2026-11-22", "abc1234", db)
	s.SetMetricsDataDir(testutil.TempDir(t))
	s.SetSchedulerRunning(true)
	for _, path := range []string{"/server/healthz", "/healthz"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("User-Agent", "curl/8.0")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Location") != "" {
			t.Errorf("GET %s status=%d Location=%q, want direct health response", path, w.Code, w.Header().Get("Location"))
		}
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || !strings.Contains(w.Body.String(), "Status:") {
			t.Errorf("GET %s did not use frontend text health handler: content-type=%q body=%q", path, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
}

func TestHealthOverlayDisabledStateContract(t *testing.T) {
	response := (&Server{config: config.DefaultConfig()}).buildHealthResponse()
	if response.Features.Tor.Enabled || response.Features.Tor.Running || response.Features.Tor.Status != "disabled" || response.Features.Tor.Hostname != "" || response.Checks.Tor != "" {
		t.Errorf("disabled Tor state violates contract: %+v checks=%+v", response.Features.Tor, response.Checks)
	}
	if response.Features.I2P.Enabled || response.Features.I2P.Running || response.Features.I2P.Status != "disabled" || response.Features.I2P.State != "disabled" || response.Features.I2P.Hostname != "" || response.Features.I2P.Provider != "none" || response.Checks.I2P != "" {
		t.Errorf("disabled I2P state violates contract: %+v checks=%+v", response.Features.I2P, response.Checks)
	}
}
