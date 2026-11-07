package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
)

// fixtureJSON is a minimal, deterministic country dataset so handler tests
// do not depend on the real embedded countries.json.
const fixtureJSON = `[
	{"name": {"common": "Testland", "official": "Republic of Testland"}, "cca2": "TL", "cca3": "TST", "capital": ["Testville"], "region": "Europe", "latlng": [10, 20]},
	{"name": {"common": "Otherland", "official": "Republic of Otherland"}, "cca2": "OL", "cca3": "OTH", "capital": ["Othertown"], "region": "Asia", "latlng": [30, 40]}
]`

// newTestServer builds a fully wired Server backed by fixture data and the
// default config, exactly as production does, but without touching the
// network — requests are dispatched in-process via httptest.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWithConfig(t, config.DefaultConfig())
}

func newTestServerWithConfig(t *testing.T, cfg *config.Config) *Server {
	t.Helper()
	svc, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService() unexpected error: %v", err)
	}
	return New(svc, cfg, "0.0.0.0", "8080", "test-version", "2024-01-01", "abc123")
}

func doRequest(s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func TestHandleHealthz(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/server/healthz", "/api/v1/server/healthz", "/api/healthz"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		var got map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: invalid JSON body: %v", path, err)
		}
		data, _ := got["data"].(map[string]interface{})
		if got["ok"] != true || data["status"] != "healthy" {
			t.Errorf("%s: response = %v, want ok=true and data.status=healthy", path, got)
		}
	}
}

func TestSecurityHeadersAndReportEndpoint(t *testing.T) {
	s := newTestServer(t)
	record := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	s.router.ServeHTTP(record, req)
	for name, want := range map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "SAMEORIGIN",
		"Referrer-Policy":                   "strict-origin-when-cross-origin",
		"X-Permitted-Cross-Domain-Policies": "none",
	} {
		if got := record.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !strings.Contains(record.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Errorf("CSP does not disallow inline scripts: %q", record.Header().Get("Content-Security-Policy"))
	}
	if record.Header().Get("X-Request-ID") == "" {
		t.Error("response is missing X-Request-ID")
	}

	report := httptest.NewRecorder()
	reportRequest := httptest.NewRequest(http.MethodPost, "/api/v1/server/reports/csp", strings.NewReader(`{"csp-report":{}}`))
	reportRequest.Header.Set("Content-Type", "application/csp-report")
	s.router.ServeHTTP(report, reportRequest)
	if report.Code != http.StatusNoContent {
		t.Errorf("CSP report status = %d, want 204", report.Code)
	}
}

func TestRateLimitMiddlewareOnPublicAPI(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.RateLimit.Read.Requests = 1
	cfg.Server.RateLimit.Read.Window = 60
	cfg.Server.RateLimit.GlobalBurst = 10
	s := newTestServerWithConfig(t, cfg)
	first := httptest.NewRequest(http.MethodGet, "/api/v1/countries", nil)
	first.RemoteAddr = "203.0.113.8:1234"
	firstResult := httptest.NewRecorder()
	s.router.ServeHTTP(firstResult, first)
	if firstResult.Code != http.StatusOK {
		t.Fatalf("first API request status = %d, want 200", firstResult.Code)
	}
	second := httptest.NewRequest(http.MethodGet, "/api/v1/countries", nil)
	second.RemoteAddr = first.RemoteAddr
	secondResult := httptest.NewRecorder()
	s.router.ServeHTTP(secondResult, second)
	if secondResult.Code != http.StatusTooManyRequests || secondResult.Header().Get("Retry-After") == "" {
		t.Fatalf("limited request returned status %d and Retry-After %q", secondResult.Code, secondResult.Header().Get("Retry-After"))
	}
}

func TestHandleGetCountries(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/v1/countries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		OK   bool `json:"ok"`
		Data []struct {
			CCA2 string `json:"cca2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if !got.OK || len(got.Data) != 2 {
		t.Errorf("got %+v, want ok=true and two country results", got)
	}
	page := doRequest(s, http.MethodGet, "/api/v1/countries?page=2&limit=1", nil)
	var pageBody struct {
		OK   bool `json:"ok"`
		Data []struct {
			CCA2 string `json:"cca2"`
		} `json:"data"`
		Pagination struct {
			Page  int `json:"page"`
			Limit int `json:"limit"`
			Total int `json:"total"`
			Pages int `json:"pages"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &pageBody); err != nil {
		t.Fatalf("invalid paginated JSON body: %v", err)
	}
	if page.Code != http.StatusOK || !pageBody.OK || pageBody.Pagination.Total != 2 || pageBody.Pagination.Page != 2 || pageBody.Pagination.Limit != 1 || pageBody.Pagination.Pages != 2 || len(pageBody.Data) != 1 {
		t.Errorf("paginated response = status %d, %+v", page.Code, pageBody)
	}
	filtered := doRequest(s, http.MethodGet, "/api/v1/countries?region=Europe", nil)
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), "Testland") || strings.Contains(filtered.Body.String(), "Otherland") {
		t.Errorf("region-filtered response = status %d, body %s", filtered.Code, filtered.Body.String())
	}
	invalidPage := doRequest(s, http.MethodGet, "/api/v1/countries?page=0", nil)
	if invalidPage.Code != http.StatusBadRequest {
		t.Errorf("invalid page: status = %d, want 400", invalidPage.Code)
	}
}

func TestHandleSearchCountries(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(s, http.MethodGet, "/api/v1/countries/search?q=Test", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Testland") {
		t.Errorf("body = %s, want it to contain Testland", rec.Body.String())
	}

	// Missing query parameter must 400.
	recMissing := doRequest(s, http.MethodGet, "/api/v1/countries/search", nil)
	if recMissing.Code != http.StatusBadRequest {
		t.Errorf("missing q: status = %d, want 400", recMissing.Code)
	}
}

func TestHandleGetCountryByCode(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(s, http.MethodGet, "/api/v1/countries/tl", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Testland") {
		t.Errorf("body = %s, want it to contain Testland", rec.Body.String())
	}

	recNotFound := doRequest(s, http.MethodGet, "/api/v1/countries/zz", nil)
	if recNotFound.Code != http.StatusNotFound {
		t.Errorf("unknown code: status = %d, want 404", recNotFound.Code)
	}
}

func TestHandleFindNearestCountry(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(s, http.MethodGet, "/api/v1/coordinates?lat=10&lon=20", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Testland") {
		t.Errorf("body = %s, want nearest to be Testland", rec.Body.String())
	}

	recMissing := doRequest(s, http.MethodGet, "/api/v1/coordinates", nil)
	if recMissing.Code != http.StatusBadRequest {
		t.Errorf("missing lat/lon: status = %d, want 400", recMissing.Code)
	}

	recBad := doRequest(s, http.MethodGet, "/api/v1/coordinates?lat=x&lon=20", nil)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("invalid lat: status = %d, want 400", recBad.Code)
	}

	recBadLon := doRequest(s, http.MethodGet, "/api/v1/coordinates?lat=10&lon=x", nil)
	if recBadLon.Code != http.StatusBadRequest {
		t.Errorf("invalid lon: status = %d, want 400", recBadLon.Code)
	}
}

func TestHandleFindNearestCountryPost(t *testing.T) {
	s := newTestServer(t)

	body, _ := json.Marshal(map[string]float64{"lat": 30, "lon": 40})
	rec := doRequest(s, http.MethodPost, "/api/v1/coordinates", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Otherland") {
		t.Errorf("body = %s, want nearest to be Otherland", rec.Body.String())
	}

	recBadJSON := doRequest(s, http.MethodPost, "/api/v1/coordinates", []byte("not json"))
	if recBadJSON.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON body: status = %d, want 400", recBadJSON.Code)
	}
}

func TestHandleFindNearby(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(s, http.MethodGet, "/api/v1/nearby?lat=10&lon=20&radius=5000", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	recDefaultRadius := doRequest(s, http.MethodGet, "/api/v1/nearby?lat=10&lon=20", nil)
	if recDefaultRadius.Code != http.StatusOK {
		t.Errorf("default radius: status = %d, want 200", recDefaultRadius.Code)
	}

	recMissing := doRequest(s, http.MethodGet, "/api/v1/nearby", nil)
	if recMissing.Code != http.StatusBadRequest {
		t.Errorf("missing lat/lon: status = %d, want 400", recMissing.Code)
	}

	recBadRadius := doRequest(s, http.MethodGet, "/api/v1/nearby?lat=10&lon=20&radius=x", nil)
	if recBadRadius.Code != http.StatusBadRequest {
		t.Errorf("invalid radius: status = %d, want 400", recBadRadius.Code)
	}
}

func TestHandleStats(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/v1/stats", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "test-version") {
		t.Errorf("body = %s, want it to contain the version", rec.Body.String())
	}
}

func TestHandleStatsTxt(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/v1/stats.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Total Countries: 2") {
		t.Errorf("body = %q, want it to contain the count", rec.Body.String())
	}
}

func TestHandleCount(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/v1/count", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		OK   bool `json:"ok"`
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if !got.OK || got.Data.Count != 2 {
		t.Errorf("response = %+v, want ok=true and data.count=2", got)
	}
}

func TestHandleCountTxt(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/v1/count.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "2" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "2")
	}
}

func TestHandleManifest(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/manifest.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Errorf("Content-Type = %q, want application/manifest+json", rec.Header().Get("Content-Type"))
	}
}

func TestHandleServiceWorker(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/sw.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CACHE_NAME") {
		t.Errorf("body = %s, want service worker script", rec.Body.String())
	}
}

// robots.txt must reflect the configured Allow/Deny lists.
func TestHandleRobots(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/robots.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Allow: /") || !strings.Contains(body, "Disallow: /debug") {
		t.Errorf("body = %q, want configured allow/deny entries", body)
	}
}

func TestHandleSecurity(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/.well-known/security.txt"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Contact:") {
			t.Errorf("%s: body = %q, want a Contact line", path, rec.Body.String())
		}
		for _, required := range []string{"Policy:", "Expires:"} {
			if !strings.Contains(rec.Body.String(), required) {
				t.Errorf("%s: body = %q, want %s line", path, rec.Body.String(), required)
			}
		}
	}
	if rec := doRequest(s, http.MethodGet, "/security.txt", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/security.txt status = %d, want 404", rec.Code)
	}
}

func TestHandleLLMS(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/llms.txt", "/.well-known/llms.txt"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Base URL:") || !strings.Contains(rec.Body.String(), "GET /countries") {
			t.Errorf("%s: status=%d body=%q", path, rec.Code, rec.Body.String())
		}
	}
}

// The Web UI pages render through the embedded templates; a status of 200
// confirms the templates parsed and executed without error.
func TestHandleWebUIPages(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/search", "/coordinates", "/server/docs/swagger", "/server/about", "/server/help", "/server/privacy", "/server/terms", "/server/security", "/server/contact", "/server/docs/graphql"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200, body: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// The coordinates path serves the server-rendered location finder. API lookup
// requests use the versioned `/api/v1/coordinates` endpoint.
func TestCoordinatesRouteServesWebPage(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/coordinates", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Find Nearest Country") {
		t.Errorf("GET /coordinates = %d, want the location finder page", rec.Code)
	}
}

// corsMiddleware must short-circuit OPTIONS requests and set CORS headers
// on every response.
func TestCorsMiddleware(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(s, http.MethodOptions, "/api/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	recGet := doRequest(s, http.MethodGet, "/api/healthz", nil)
	if recGet.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("GET Access-Control-Allow-Origin = %q, want *", recGet.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCoordinatesWebPageHandler(t *testing.T) {
	s := newTestServer(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/coordinates", nil)
	s.handleCoordinatesPage(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("coordinates page status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Find Nearest Country") {
		t.Errorf("coordinates page body lacks its heading: %q", recorder.Body.String())
	}
}

func TestRunReturnsListenError(t *testing.T) {
	s := newTestServer(t)
	if err := s.Run(context.Background(), "invalid-address"); err == nil {
		t.Fatal("Run() with an invalid listen address returned nil")
	}
}
