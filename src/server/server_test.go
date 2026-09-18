package server

import (
	"bytes"
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
	{"name": "Testland", "country_code": "TL", "capital": "Testville", "latlng": [10, 20]},
	{"name": "Otherland", "country_code": "OL", "capital": "Othertown", "latlng": [30, 40]}
]`

// newTestServer builds a fully wired Server backed by fixture data and the
// default config, exactly as production does, but without touching the
// network — requests are dispatched in-process via httptest.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	svc, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService() unexpected error: %v", err)
	}
	cfg := config.DefaultConfig()
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
	for _, path := range []string{"/healthz", "/health", "/status"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		var got map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: invalid JSON body: %v", path, err)
		}
		if got["status"] != "ok" {
			t.Errorf("%s: status field = %v, want ok", path, got["status"])
		}
	}
}

func TestHandleGetCountries(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/v1/countries", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Success bool `json:"success"`
		Count   int  `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if !got.Success || got.Count != 2 {
		t.Errorf("got %+v, want success=true count=2", got)
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

func TestHandleSearchCountriesPath(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/countries/search/Other", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Otherland") {
		t.Errorf("body = %s, want it to contain Otherland", rec.Body.String())
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
		Count int `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if got.Count != 2 {
		t.Errorf("count = %d, want 2", got.Count)
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

func TestHandleRawData(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/api/data", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Testland") {
		t.Errorf("body = %s, want raw fixture JSON", rec.Body.String())
	}
}

func TestHandleRandomCountry(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/random", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	recTxt := doRequest(s, http.MethodGet, "/random.txt", nil)
	if recTxt.Code != http.StatusOK {
		t.Fatalf("random.txt: status = %d, want 200", recTxt.Code)
	}
	if recTxt.Body.Len() == 0 {
		t.Error("random.txt: body is empty")
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
	for _, path := range []string{"/security.txt", "/.well-known/security.txt"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Contact:") {
			t.Errorf("%s: body = %q, want a Contact line", path, rec.Body.String())
		}
	}
}

// The Web UI pages render through the embedded templates; a status of 200
// confirms the templates parsed and executed without error.
//
// "/coordinates" is intentionally excluded: setupRoutes registers GET
// /coordinates twice (once for handleCoordinatesPage as a web UI page,
// once for handleFindNearestCountry as a shorthand API route), and chi
// keeps the later registration — so the page handler is unreachable at
// that path. Documented below in TestCoordinatesRouteShadowing.
func TestHandleWebUIPages(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/search", "/openapi"} {
		rec := doRequest(s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200, body: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// setupRoutes registers GET /coordinates twice: once for the HTML page
// (handleCoordinatesPage) and once, later, as a shorthand for the API's
// nearest-country lookup (handleFindNearestCountry). chi keeps the later
// registration, so the page is unreachable and GET /coordinates actually
// behaves like the API handler (400 without lat/lon query params). This
// test pins the current, buggy-looking behavior so a future route change
// is a deliberate decision, not a silent regression.
func TestCoordinatesRouteShadowing(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/coordinates", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET /coordinates = %d, want 400 (shadowed by handleFindNearestCountry); "+
			"if this now returns 200 the duplicate route registration in setupRoutes was fixed "+
			"and this test should be updated/removed", rec.Code)
	}
}

// corsMiddleware must short-circuit OPTIONS requests and set CORS headers
// on every response.
func TestCorsMiddleware(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(s, http.MethodOptions, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	recGet := doRequest(s, http.MethodGet, "/healthz", nil)
	if recGet.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("GET Access-Control-Allow-Origin = %q, want *", recGet.Header().Get("Access-Control-Allow-Origin"))
	}
}
