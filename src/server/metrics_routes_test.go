package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/common/logbuffer"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/server/metrics"
	"github.com/go-chi/chi/v5"
)

func metricsTestRouter(cfg config.MetricsConfig) http.Handler {
	router := chi.NewRouter()
	collector := metrics.New(metrics.Config{ProjectName: "countries", Version: "test", Commit: "abc", BuildDate: "today", IncludeRuntime: true})
	collector.SetCountryRecords(249)
	RegisterMetricsRoutes(router, cfg, collector)
	return router
}

func TestMetricsRoutesRequireIndependentBearerTokens(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.Auth.Tokens = config.MetricsAuthTokens{Prometheus: "prom-secret", Grafana: "graf-secret", Loki: "loki-secret"}
	handler := metricsTestRouter(cfg)
	for _, test := range []struct {
		path   string
		token  string
		method string
		want   int
	}{
		{path: "/server/metrics", want: http.StatusUnauthorized},
		{path: "/server/metrics", token: "graf-secret", want: http.StatusUnauthorized},
		{path: "/server/metrics", token: "prom-secret", want: http.StatusOK},
		{path: "/server/metrics", token: "prom-secret", method: http.MethodPost, want: http.StatusMethodNotAllowed},
		{path: "/server/metrics/prometheus", token: "prom-secret", want: http.StatusOK},
		{path: "/api/v1/server/metrics", token: "prom-secret", want: http.StatusOK},
		{path: "/api/metrics/prometheus", token: "prom-secret", want: http.StatusOK},
		{path: "/metrics", token: "prom-secret", want: http.StatusOK},
		{path: "/server/metrics/grafana", token: "graf-secret", want: http.StatusOK},
		{path: "/server/metrics/loki", token: "loki-secret", want: http.StatusOK},
		{path: "/server/metrics?token=prom-secret", want: http.StatusUnauthorized},
	} {
		t.Run(test.path+"/"+test.token, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, test.path, nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusUnauthorized && response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", response.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestMetricsGrafanaDashboardIsImportableAndCoversCategories(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.Auth.Tokens.Grafana = "graf-secret"
	request := httptest.NewRequest(http.MethodGet, "/server/metrics/grafana", nil)
	request.Header.Set("Authorization", "Bearer graf-secret")
	response := httptest.NewRecorder()
	metricsTestRouter(cfg).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var dashboard struct {
		Title         string `json:"title"`
		SchemaVersion int    `json:"schemaVersion"`
		Templating    struct {
			List []map[string]interface{} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			Title string `json:"title"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &dashboard); err != nil {
		t.Fatalf("dashboard JSON invalid: %v", err)
	}
	if dashboard.Title == "" || dashboard.SchemaVersion < 1 || len(dashboard.Templating.List) == 0 {
		t.Fatalf("dashboard is missing import metadata: %+v", dashboard)
	}
	panelTitles := make(map[string]bool, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		panelTitles[panel.Title] = true
	}
	for _, title := range []string{"HTTP Requests", "HTTP Request Latency (p95)", "Database Query Latency (p95)", "Database Errors", "SQLite Busy Retries", "SQLite Write Wait (p95)", "Cache Hits and Misses", "Cache Evictions", "Cache Items", "Scheduler Runs", "Scheduler Task Duration (p95)", "Scheduler Tasks Running", "Seconds Since Last Task Run", "System CPU", "System Memory", "System Disk", "Country Records", "Authentication Attempts", "Rate Limit Blocks", "Go Runtime Goroutines", "Application Uptime"} {
		if !panelTitles[title] {
			t.Errorf("dashboard missing panel %q", title)
		}
	}
}

func TestMetricsLokiReturnsBoundedStructuredRedactedLogs(t *testing.T) {
	logbuffer.Default().Add("error", `security report rejected password="not-a-secret" token=mtr_0123456789abcdefghijklmnopqrstuv`)
	cfg := config.DefaultConfig().Server.Metrics
	cfg.Auth.Tokens.Loki = "loki-secret"
	request := httptest.NewRequest(http.MethodGet, "/api/metrics/loki", nil)
	request.Header.Set("Authorization", "Bearer loki-secret")
	response := httptest.NewRecorder()
	metricsTestRouter(cfg).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("response status/content type = %d/%q, want 200 JSON", response.Code, response.Header().Get("Content-Type"))
	}
	if strings.Contains(response.Body.String(), "not-a-secret") || strings.Contains(response.Body.String(), "mtr_0123456789abcdefghijklmnopqrstuv") {
		t.Fatalf("Loki response leaked a credential: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"streams":[`) || !strings.Contains(response.Body.String(), `"level":"error"`) || !strings.Contains(response.Body.String(), "[redacted]") {
		t.Fatalf("Loki response lacks a structured sanitized log stream: %s", response.Body.String())
	}
}

func TestMetricsRoutesRespectEnabledAndRootAliasConfig(t *testing.T) {
	cfg := config.DefaultConfig().Server.Metrics
	cfg.Auth.Tokens.Prometheus = "prom-secret"
	cfg.Root.Enabled = false
	handler := metricsTestRouter(cfg)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer prom-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled root alias status = %d, want 404", response.Code)
	}
	cfg.Enabled = false
	response = httptest.NewRecorder()
	metricsTestRouter(cfg).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/server/metrics", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled metrics status = %d, want 404", response.Code)
	}
}
