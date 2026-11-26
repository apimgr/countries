package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/logbuffer"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/server/metrics"
	"github.com/go-chi/chi/v5"
)

// RegisterMetricsRoutes mounts each protected metrics service under its
// canonical path and direct aliases. Metrics are omitted when disabled.
func RegisterMetricsRoutes(router chi.Router, cfg config.MetricsConfig, collector *metrics.Metrics) {
	if !cfg.Enabled || collector == nil {
		return
	}
	prometheusHandler := metricsBearerAuth(cfg, cfg.Auth.Tokens.Prometheus, collector.Handler())
	grafanaHandler := metricsBearerAuth(cfg, cfg.Auth.Tokens.Grafana, http.HandlerFunc(handleGrafanaDashboard))
	lokiHandler := metricsBearerAuth(cfg, cfg.Auth.Tokens.Loki, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleLokiStreams(w, r, cfg)
	}))

	mount := func(prefix string) {
		router.Method(http.MethodGet, prefix, prometheusHandler)
		router.Method(http.MethodGet, prefix+"/{service}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch chi.URLParam(r, "service") {
			case "prometheus":
				prometheusHandler.ServeHTTP(w, r)
			case "grafana":
				grafanaHandler.ServeHTTP(w, r)
			case "loki":
				lokiHandler.ServeHTTP(w, r)
			default:
				http.NotFound(w, r)
			}
		}))
		router.Method(http.MethodGet, prefix+"/prometheus", prometheusHandler)
		router.Method(http.MethodGet, prefix+"/grafana", grafanaHandler)
		router.Method(http.MethodGet, prefix+"/loki", lokiHandler)
	}
	mount("/server/metrics")
	mount(api.APIPath("server/metrics"))
	mount("/api/metrics")
	if cfg.Root.Enabled {
		mount("/metrics")
	}
}

func metricsBearerAuth(cfg config.MetricsConfig, token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.Auth.AllowUnauthenticated {
			next.ServeHTTP(w, r)
			return
		}
		if token == "" {
			writeAPIError(w, http.StatusForbidden, "FORBIDDEN", "Metrics service is unavailable")
			return
		}
		provided := r.Header.Get("Authorization")
		want := "Bearer " + token
		providedHash := sha256.Sum256([]byte(provided))
		wantHash := sha256.Sum256([]byte(want))
		if subtle.ConstantTimeCompare(providedHash[:], wantHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "A valid metrics bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleGrafanaDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	if err := json.NewEncoder(w).Encode(grafanaDashboard()); err != nil {
		log.Printf("encode metrics Grafana dashboard: %v", err)
	}
}

func grafanaDashboard() map[string]interface{} {
	datasource := map[string]interface{}{"type": "prometheus", "uid": "${DS_PROMETHEUS}"}
	panel := func(id, x, y int, title, query, legend, unit string) map[string]interface{} {
		return map[string]interface{}{
			"datasource": datasource,
			"fieldConfig": map[string]interface{}{"defaults": map[string]interface{}{
				"color":  map[string]interface{}{"mode": "palette-classic"},
				"custom": map[string]interface{}{"drawStyle": "line", "fillOpacity": 10, "lineInterpolation": "linear", "lineWidth": 1, "showPoints": "never", "spanNulls": false},
				"min":    0, "unit": unit,
			}, "overrides": []interface{}{}},
			"gridPos": map[string]interface{}{"h": 8, "w": 12, "x": x, "y": y},
			"id":      id,
			"options": map[string]interface{}{
				"legend":  map[string]interface{}{"calcs": []string{}, "displayMode": "list", "placement": "bottom", "showLegend": true},
				"tooltip": map[string]interface{}{"mode": "single", "sort": "none"},
			},
			"targets": []interface{}{map[string]interface{}{"datasource": datasource, "editorMode": "code", "expr": query, "legendFormat": legend, "refId": "A"}},
			"title":   title, "type": "timeseries",
		}
	}
	return map[string]interface{}{
		"__inputs":    []interface{}{map[string]interface{}{"name": "DS_PROMETHEUS", "label": "Prometheus", "description": "", "type": "datasource", "pluginId": "prometheus", "pluginName": "Prometheus"}},
		"__requires":  []interface{}{map[string]interface{}{"type": "grafana", "id": "grafana", "name": "Grafana", "version": "11.0.0"}, map[string]interface{}{"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"}},
		"annotations": map[string]interface{}{"list": []interface{}{map[string]interface{}{"builtIn": 1, "datasource": map[string]interface{}{"type": "grafana", "uid": "-- Grafana --"}, "enable": true, "hide": true, "iconColor": "rgba(0, 211, 255, 1)", "name": "Annotations & Alerts", "type": "dashboard"}}},
		"editable":    true, "fiscalYearStartMonth": 0, "graphTooltip": 0, "id": nil,
		"links": []interface{}{}, "liveNow": false,
		"panels": []interface{}{
			panel(1, 0, 0, "HTTP Requests", `sum by (status) (rate(countries_http_requests_total[$__rate_interval]))`, `{{status}}`, "reqps"),
			panel(2, 12, 0, "HTTP Request Latency (p95)", `histogram_quantile(0.95, sum by (le, path) (rate(countries_http_request_duration_seconds_bucket[$__rate_interval])))`, `{{path}} p95`, "s"),
			panel(3, 0, 8, "HTTP Active Requests", `countries_http_active_requests`, "active", "short"),
			panel(4, 12, 8, "Database Query Latency (p95)", `histogram_quantile(0.95, sum by (le, operation) (rate(countries_db_query_duration_seconds_bucket[$__rate_interval])))`, `{{operation}} p95`, "s"),
			panel(5, 0, 16, "Database Connections", `countries_db_connections_open`, "open", "short"),
			panel(6, 12, 16, "Database Errors", `sum by (error_type) (rate(countries_db_errors_total[$__rate_interval]))`, `{{error_type}}`, "reqps"),
			panel(7, 0, 24, "SQLite Busy Retries", `rate(countries_db_busy_total[$__rate_interval])`, "retries/s", "reqps"),
			panel(8, 12, 24, "SQLite Write Wait (p95)", `histogram_quantile(0.95, rate(countries_db_write_wait_seconds_bucket[$__rate_interval]))`, "p95", "s"),
			panel(9, 0, 32, "Cache Hits and Misses", `sum by (cache) (rate(countries_cache_hits_total[$__rate_interval])) or sum by (cache) (rate(countries_cache_misses_total[$__rate_interval]))`, `{{cache}}`, "reqps"),
			panel(10, 12, 32, "Cache Evictions", `sum by (cache) (rate(countries_cache_evictions_total[$__rate_interval]))`, `{{cache}}`, "reqps"),
			panel(11, 0, 40, "Cache Items", `sum by (cache) (countries_cache_size)`, `{{cache}}`, "short"),
			panel(12, 12, 40, "Scheduler Runs", `sum by (task, status) (rate(countries_scheduler_tasks_total[$__rate_interval]))`, `{{task}} {{status}}`, "reqps"),
			panel(13, 0, 48, "Scheduler Task Duration (p95)", `histogram_quantile(0.95, sum by (task, le) (rate(countries_scheduler_task_duration_seconds_bucket[1h])))`, `{{task}}`, "s"),
			panel(14, 12, 48, "Scheduler Tasks Running", `sum(countries_scheduler_tasks_running)`, "running", "short"),
			panel(15, 0, 56, "Seconds Since Last Task Run", `time() - countries_scheduler_last_run_timestamp`, `{{task}}`, "s"),
			panel(16, 12, 56, "System CPU", `countries_system_cpu_usage_percent`, "CPU", "percent"),
			panel(17, 0, 64, "System Memory", `countries_system_memory_usage_percent`, "Memory", "percent"),
			panel(18, 12, 64, "System Disk", `countries_system_disk_usage_percent`, `{{path}}`, "percent"),
			panel(19, 0, 72, "Country Records", `countries_country_records`, "Countries", "short"),
			panel(20, 12, 72, "Authentication Attempts", `sum by (status) (rate(countries_auth_attempts_total[$__rate_interval]))`, `{{status}}`, "reqps"),
			panel(21, 0, 80, "Rate Limit Blocks", `sum by (limit) (rate(countries_ratelimit_blocked_total[$__rate_interval]))`, `{{limit}}`, "reqps"),
			panel(22, 12, 80, "Go Runtime Goroutines", `countries_go_goroutines`, "Goroutines", "short"),
			panel(23, 0, 88, "Application Uptime", `countries_app_uptime_seconds`, "Uptime", "s"),
		},
		"refresh": "30s", "schemaVersion": 39, "tags": []string{"countries", "metrics"},
		"templating": map[string]interface{}{"list": []interface{}{map[string]interface{}{
			"current": map[string]interface{}{"selected": true, "text": "Prometheus", "value": "prometheus"},
			"hide":    0, "includeAll": false, "label": "Datasource", "multi": false, "name": "DS_PROMETHEUS", "options": []interface{}{},
			"query": "prometheus", "refresh": 1, "regex": "", "skipUrlSync": false, "type": "datasource",
		}}},
		"time": map[string]interface{}{"from": "now-6h", "to": "now"}, "timepicker": map[string]interface{}{},
		"timezone": "browser", "title": "Countries Metrics", "uid": "countries-metrics", "version": 1, "weekStart": "",
	}
}

func handleLokiStreams(w http.ResponseWriter, _ *http.Request, cfg config.MetricsConfig) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	entries := logbuffer.Default().Recent(cfg.Loki.MaxEntries, cfg.Loki.MaxAge)
	type lokiStream struct {
		Stream map[string]string `json:"stream"`
		Values [][2]string       `json:"values"`
	}
	streamsByLevel := make(map[string]*lokiStream)
	for _, entry := range entries {
		stream := streamsByLevel[entry.Level]
		if stream == nil {
			stream = &lokiStream{Stream: map[string]string{"app": "countries", "level": entry.Level}}
			streamsByLevel[entry.Level] = stream
		}
		line, err := json.Marshal(map[string]string{"time": entry.Time.UTC().Format(time.RFC3339Nano), "level": entry.Level, "msg": entry.Message})
		if err != nil {
			continue
		}
		stream.Values = append(stream.Values, [2]string{strconv.FormatInt(entry.Time.UnixNano(), 10), string(line)})
	}
	levels := make([]string, 0, len(streamsByLevel))
	for level := range streamsByLevel {
		levels = append(levels, level)
	}
	sort.Strings(levels)
	streams := make([]lokiStream, 0, len(levels))
	for _, level := range levels {
		streams = append(streams, *streamsByLevel[level])
	}
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"streams": streams}); err != nil {
		log.Printf("encode metrics Loki streams: %v", err)
	}
}
