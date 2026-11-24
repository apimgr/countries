package metrics

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHandlerExportsCoreMetrics(t *testing.T) {
	metrics := New(Config{ProjectName: "countries", Version: "test"})
	metrics.RecordHTTP(http.MethodGet, "/api/countries", http.StatusOK, 12, 34, 25*time.Millisecond)
	metrics.ObserveDB("select", "countries", time.Millisecond, "")

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, want := range []string{
		"countries_app_info{",
		"countries_app_uptime_seconds",
		"countries_http_requests_total{method=\"GET\",path=\"/api/countries\",status=\"200\"} 1",
		"countries_http_request_duration_seconds_bucket",
		"countries_http_request_size_bytes_bucket",
		"countries_http_response_size_bytes_bucket",
		"countries_db_queries_total{operation=\"select\",table=\"countries\"} 1",
		"countries_owner_tokens_active{kind=\"primary\"} 0",
		"countries_owner_tokens_active{kind=\"linked\"} 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics exposition missing %q", want)
		}
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("content type = %q, want Prometheus text", got)
	}
}

func TestPathLabelsAreSanitizedAndBounded(t *testing.T) {
	metrics := New(Config{ProjectName: "countries", MaxPaths: 2})
	for _, path := range []string{
		"/api/countries/12345?token=secret",
		"/api/stats",
		"/api/currencies",
	} {
		metrics.RecordHTTP("GET", path, 200, 0, 0, time.Second)
	}

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "secret") || strings.Contains(body, "23456") || strings.Contains(body, "34567") {
		t.Fatalf("sensitive or dynamic URL value leaked into exposition: %s", body)
	}
	if !strings.Contains(body, `path="/api/countries/:id"`) || !strings.Contains(body, `path="/other"`) {
		t.Fatalf("expected normalized and overflow path labels in exposition: %s", body)
	}
}

func TestMiddlewareRecordsSizesAndStatus(t *testing.T) {
	metrics := New(Config{ProjectName: "countries"})
	handler := metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(append([]byte("reply:"), body...))
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/items/1234", strings.NewReader("payload"))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if recorder.Body.String() != "reply:payload" {
		t.Fatalf("body = %q", recorder.Body.String())
	}

	metricsHandler := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsHandler, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsHandler.Body.String()
	if !strings.Contains(body, `path="/items/:id",status="201"`) {
		t.Fatalf("missing request counter: %s", body)
	}
	if !strings.Contains(body, `countries_http_active_requests 0`) {
		t.Fatalf("active request gauge was not decremented: %s", body)
	}
}

func TestHighCardinalityLabelsCollapseToOther(t *testing.T) {
	metrics := New(Config{ProjectName: "countries"})
	metrics.ObserveDB("select", "users_987654", time.Second, "database error including secret")
	metrics.RecordAuth("owner_token", "success")

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	if strings.Contains(body, "987654") || strings.Contains(body, "database error including secret") {
		t.Fatalf("unbounded or sensitive label leaked into exposition: %s", body)
	}
	if !strings.Contains(body, `table="other"`) || !strings.Contains(body, `error_type="other"`) {
		t.Fatalf("expected collapsed database labels: %s", body)
	}
}

func TestRecordsOperationalMetrics(t *testing.T) {
	metrics := New(Config{ProjectName: "countries"})
	metrics.SetDatabaseConnections(4, 2)
	metrics.RecordCache("items", true)
	metrics.RecordCache("items", false)
	metrics.RecordCacheEviction("items")
	metrics.SetCacheSize("items", 3, 128)
	metrics.RecordSchedulerStart("cleanup")
	metrics.RecordSchedulerEnd("cleanup", 250*time.Millisecond, nil, time.Unix(123, 0))
	metrics.RecordSchedulerStart("sync")
	metrics.RecordSchedulerEnd("sync", -time.Second, io.ErrUnexpectedEOF, time.Unix(124, 0))
	metrics.RecordAuth("session", "success")
	metrics.RecordAuth("unknown", "invalid")
	metrics.SetActiveOwnerTokens(2, 3)
	metrics.RecordRateLimit("global", false)
	metrics.RecordRateLimit("global", true)
	metrics.SetCountryRecords(250)
	metrics.SetTorState(true, true, false)
	metrics.RecordTorRequest()
	metrics.SetTorBandwidthUsage(1024)

	body := scrape(t, metrics)
	for _, want := range []string{
		`countries_db_connections_open 4`,
		`countries_db_connections_in_use 2`,
		`countries_cache_hits_total{cache="items"} 1`,
		`countries_cache_misses_total{cache="items"} 1`,
		`countries_cache_evictions_total{cache="items"} 1`,
		`countries_cache_size{cache="items"} 3`,
		`countries_cache_bytes{cache="items"} 128`,
		`countries_scheduler_tasks_total{status="success",task="cleanup"} 1`,
		`countries_scheduler_tasks_total{status="error",task="sync"} 1`,
		`countries_scheduler_tasks_running{task="cleanup"} 0`,
		`countries_scheduler_last_run_timestamp{task="sync"} 124`,
		`countries_auth_attempts_total{method="session",status="success"} 1`,
		`countries_auth_attempts_total{method="other",status="other"} 1`,
		`countries_owner_tokens_active{kind="primary"} 2`,
		`countries_owner_tokens_active{kind="linked"} 3`,
		`countries_ratelimit_requests_total{limit="global",status="allowed"} 1`,
		`countries_ratelimit_requests_total{limit="global",status="limited"} 1`,
		`countries_ratelimit_blocked_total{limit="global"} 1`,
		`countries_country_records 250`,
		`countries_tor_enabled 1`,
		`countries_tor_running 1`,
		`countries_tor_circuit_established 0`,
		`countries_tor_requests_total 1`,
		`countries_tor_bandwidth_usage_bytes 1024`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics exposition missing %q", want)
		}
	}
}

func TestMetricNormalizationAndLimits(t *testing.T) {
	metrics := New(Config{ProjectName: "Invalid Project!", MaxPaths: 9999})
	if metrics.project != "countries" {
		t.Fatalf("invalid project name = %q, want fallback", metrics.project)
	}
	if metrics.maxPaths != maxConfiguredPaths {
		t.Fatalf("max paths = %d, want configured limit %d", metrics.maxPaths, maxConfiguredPaths)
	}
	if got := metrics.normalizePath(""); got != "/" {
		t.Errorf("empty path normalized to %q", got)
	}
	if got := metrics.normalizePath("not-absolute"); got != "/other" {
		t.Errorf("relative path normalized to %q", got)
	}
	if got := metrics.normalizePath("/" + strings.Repeat("x", maxPathBytes)); got != "/other" {
		t.Errorf("oversized path normalized to %q", got)
	}
	if got := metrics.normalizePath("/" + strings.Repeat("x/", maxPathSegments)); got != "/other" {
		t.Errorf("path with too many segments normalized to %q", got)
	}
	if got := metrics.normalizeTask("bad task name"); got != "other" {
		t.Errorf("invalid task normalized to %q", got)
	}
	for i := 0; i < maxTaskLabels; i++ {
		metrics.normalizeTask("task_" + strconv.Itoa(i))
	}
	if got := metrics.normalizeTask("overflow"); got != "other" {
		t.Errorf("overflow task normalized to %q", got)
	}
}

func TestCollectorLifecycleAndHelpers(t *testing.T) {
	metrics := New(Config{ProjectName: "countries"})
	metrics.StartSystemCollector(context.Background(), 0)
	metrics.StopSystemCollector()
	metrics.SetDataDir(t.TempDir())
	metrics.SetDatabaseConnections(-1, -1)
	metrics.SetCacheSize("unknown", -1, -1)
	metrics.SetActiveOwnerTokens(-1, -1)
	metrics.SetCountryRecords(-1)
	metrics.RecordHTTP("TRACE", "/invalid", 99, -1, -1, -time.Second)
	metrics.ObserveDB("drop", "unknown", -time.Second, "secret")

	if got := normalizeStatus(777); got != "unknown" {
		t.Errorf("invalid status normalized to %q", got)
	}
	if got := normalizeStatus(418); got != "other" {
		t.Errorf("unlisted status normalized to %q", got)
	}
	if got := normalizeLabel("Upper", 10); got != "upper" {
		t.Errorf("valid label normalized to %q", got)
	}
	if got := metricName("bad project"); got != "countries" {
		t.Errorf("invalid metric namespace normalized to %q", got)
	}
	buckets := positiveBuckets([]float64{2, 1, 2, 0, math.NaN(), math.Inf(1)})
	if !reflect.DeepEqual(buckets, []float64{1, 2}) {
		t.Errorf("normalized buckets = %v", buckets)
	}
	if got := positiveBuckets([]float64{0, math.NaN()}); len(got) == 0 {
		t.Error("empty valid bucket set did not use defaults")
	}
	if boolFloat(true) != 1 || boolFloat(false) != 0 {
		t.Error("boolean gauge conversion returned unexpected values")
	}
}

func scrape(t *testing.T, metrics *Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder.Body.String()
}
