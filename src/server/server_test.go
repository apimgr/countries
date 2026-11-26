package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	commontheme "github.com/apimgr/countries/src/common/theme"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
	graphqlapi "github.com/apimgr/countries/src/graphql"
	appmode "github.com/apimgr/countries/src/mode"
	"github.com/apimgr/countries/src/swagger"
	"github.com/apimgr/countries/src/testutil"
)

// fixtureJSON is a minimal, deterministic country dataset so handler tests
// do not depend on the real embedded countries.json.
const fixtureJSON = `[
	{"name": {"common": "Testland", "official": "Republic of Testland"}, "cca2": "TL", "cca3": "TST", "ccn3": "001", "capital": ["Testville"], "region": "Europe", "latlng": [10, 20]},
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
	if strings.HasPrefix(path, "/api/") {
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "text/html")
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

func TestGraphQLUnversionedAliasGETMatchesCanonicalRoute(t *testing.T) {
	server := newTestServer(t)
	query := url.QueryEscape("{ countries { name { common } } }")
	canonical := doRequest(server, http.MethodGet, "/api/v1/server/graphql?query="+query, nil)
	alias := doRequest(server, http.MethodGet, "/api/graphql?query="+query, nil)
	if canonical.Code != alias.Code || canonical.Header().Get("Content-Type") != alias.Header().Get("Content-Type") || canonical.Body.String() != alias.Body.String() {
		t.Fatalf("GraphQL alias differs from canonical route: canonical=(%d, %q, %q) alias=(%d, %q, %q)", canonical.Code, canonical.Header().Get("Content-Type"), canonical.Body.String(), alias.Code, alias.Header().Get("Content-Type"), alias.Body.String())
	}
}

func TestHandleHealthz(t *testing.T) {
	svc, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatalf("countries.NewService() unexpected error: %v", err)
	}
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "health.db"))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.DefaultConfig()
	cfg.Server.GeoIP.Enabled = false
	s := New(svc, cfg, "0.0.0.0", "8080", "test-version", "2024-01-01", "abc123", db)
	s.SetMetricsDataDir(testutil.TempDir(t))
	s.SetSchedulerRunning(true)
	for _, path := range []string{"/server/healthz", "/api/v1/server/healthz", "/api/healthz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		var got HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: invalid JSON body: %v", path, err)
		}
		if got.Status != "healthy" || got.Project.Name == "" {
			t.Errorf("%s: health response = %+v, want healthy single-instance response", path, got)
		}
		if strings.Contains(rec.Body.String(), `"cluster"`) {
			t.Errorf("%s: health response exposes unsupported cluster state", path)
		}
	}
}

func TestDebugEndpointsRequireDebugFlag(t *testing.T) {
	previous := appmode.DebugEnabled()
	t.Cleanup(func() { appmode.SetDebugEnabled(previous) })
	appmode.SetDebugEnabled(false)
	withoutDebug := newTestServer(t)
	for _, path := range []string{"/debug/vars", "/debug/config", "/api/v1/debug/config", "/api/debug/config"} {
		if got := doRequest(withoutDebug, http.MethodGet, path, nil).Code; got != http.StatusNotFound {
			t.Fatalf("debug endpoint %s without debug flag status = %d, want 404", path, got)
		}
	}
	appmode.SetDebugEnabled(true)
	cfg := config.DefaultConfig()
	cfg.Server.Token = "adm_debug_test_token"
	cfg.Server.Database.URL = "libsql://db.example.test?authToken=database-url-secret"
	cfg.Server.Cache.URL = "rediss://cache-user:cache-url-secret@cache.example.test:6379/0"
	withDebug := newTestServerWithConfig(t, cfg)
	paths := []string{
		"/debug/vars", "/debug/config", "/debug/routes", "/debug/cache", "/debug/db",
		"/debug/scheduler", "/debug/memory", "/debug/goroutines", "/debug/pprof",
	}
	for _, path := range paths {
		if got := doRequest(withDebug, http.MethodGet, path, nil).Code; got != http.StatusUnauthorized {
			t.Errorf("debug endpoint %s without operator token status = %d, want 401", path, got)
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
		response := httptest.NewRecorder()
		withDebug.router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("debug endpoint %s with operator token status = %d, want 200: %s", path, response.Code, response.Body.String())
		}
		if path == "/debug/config" && strings.Contains(response.Body.String(), cfg.Server.Token) {
			t.Error("debug configuration exposed the operator token")
		}
		if path == "/debug/config" && (strings.Contains(response.Body.String(), "database-url-secret") || strings.Contains(response.Body.String(), "cache-url-secret")) {
			t.Error("debug configuration exposed database or cache URL credentials")
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/debug/error500", nil)
	request.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
	response := httptest.NewRecorder()
	withDebug.router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Errorf("debug error renderer status = %d, want 500", response.Code)
	}
	for _, path := range []string{"/api/v1/debug/config", "/api/debug/config"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
		response := httptest.NewRecorder()
		withDebug.router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("non-canonical debug route %s status = %d, want 404", path, response.Code)
		}
	}
}

func TestDebugDiagnosticSettingsAreGatedByDebugFlag(t *testing.T) {
	previous := appmode.DebugEnabled()
	t.Cleanup(func() { appmode.SetDebugEnabled(previous) })
	appmode.SetDebugEnabled(true)
	cfg := config.DefaultConfig()
	cfg.Server.Token = "adm_debug_settings_test"
	cfg.Server.Debug.Pprof = false
	cfg.Server.Debug.RuntimeEndpoints = false
	s := newTestServerWithConfig(t, cfg)
	for _, path := range []string{"/debug/pprof", "/debug/vars", "/debug/memory", "/debug/goroutines"} {
		if got := doRequest(s, http.MethodGet, path, nil).Code; got != http.StatusNotFound {
			t.Errorf("disabled diagnostic endpoint %s status = %d, want 404", path, got)
		}
	}
}

func TestDebugLogRedactsSensitiveRequestFields(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
		want        string
		secret      string
	}{
		{
			name:        "JSON",
			contentType: "application/json",
			body:        `{"name":"Test","access_token":"hidden","nested":{"apiKey":"also-hidden"}}`,
			want:        `"name":"Test"`,
			secret:      "hidden",
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        "name=Test&password=hidden",
			want:        "name=Test",
			secret:      "hidden",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeDebugBody([]byte(test.body), test.contentType)
			if !strings.Contains(got, test.want) {
				t.Errorf("sanitized body %q does not contain %q", got, test.want)
			}
			decoded, _ := url.QueryUnescape(got)
			if strings.Contains(decoded, test.secret) || !strings.Contains(decoded, "[redacted]") {
				t.Errorf("sanitized body leaked sensitive value: %q", got)
			}
		})
	}
	got := sanitizeDebugURL("https://example.test/report?security_id=visible&access_token=hidden")
	decoded, _ := url.QueryUnescape(got)
	if strings.Contains(decoded, "hidden") || !strings.Contains(decoded, "[redacted]") {
		t.Errorf("sanitized URL = %q, sensitive value was not redacted", got)
	}
}

func TestServerRootRedirectsToAbout(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/server", nil)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /server status = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/server/about" {
		t.Errorf("GET /server Location = %q, want /server/about", got)
	}
}

func TestBuildStampCookieAndVersionPurge(t *testing.T) {
	s := newTestServer(t)
	firstRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	firstRequest.Header.Set("Accept", "text/html")
	firstResponse := httptest.NewRecorder()
	s.router.ServeHTTP(firstResponse, firstRequest)
	if firstResponse.Code != http.StatusOK || firstResponse.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("first HTML response status=%d Clear-Site-Data=%q", firstResponse.Code, firstResponse.Header().Get("Clear-Site-Data"))
	}
	if firstResponse.Header().Get("Cache-Control") != "no-store" || firstResponse.Header().Get("ETag") != `"test-version-abc123"` {
		t.Fatalf("HTML cache headers = %v", firstResponse.Header())
	}
	var buildCookie *http.Cookie
	for _, cookie := range firstResponse.Result().Cookies() {
		if cookie.Name == buildCookieName {
			buildCookie = cookie
		}
	}
	if buildCookie == nil || buildCookie.Value != "test-version-abc123" || buildCookie.Path != "/" || buildCookie.MaxAge != 31536000 || !buildCookie.Secure || buildCookie.HttpOnly || buildCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("build stamp cookie = %+v", buildCookie)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Sec-Fetch-Dest", "document")
	request.Header.Set("Sec-Fetch-Mode", "navigate")
	request.AddCookie(&http.Cookie{Name: buildCookieName, Value: "old-build"})
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if got := response.Header().Get("Clear-Site-Data"); got != `"cache", "storage"` {
		t.Fatalf("stale top-level navigation Clear-Site-Data = %q", got)
	}

	for _, headers := range []map[string]string{
		{"Sec-Fetch-Dest": "iframe", "Sec-Fetch-Mode": "navigate"},
		{"Sec-Fetch-Dest": "empty", "Sec-Fetch-Mode": "cors"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		request.AddCookie(&http.Cookie{Name: buildCookieName, Value: "old-build"})
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, request)
		if got := response.Header().Get("Clear-Site-Data"); got != "" {
			t.Errorf("non-document request %v Clear-Site-Data = %q", headers, got)
		}
	}
}

func TestStaticAssetCacheRequiresMatchingBuildStamp(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/static/css/public.css", nil)
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("ETag") == "" {
		t.Fatalf("unstamped static response status=%d headers=%v", response.Code, response.Header())
	}

	stampedURL := "/static/css/public.css?v=" + s.assetStamp()
	request = httptest.NewRequest(http.MethodGet, stampedURL, nil)
	response = httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("stamped static response status=%d cache-control=%q", response.Code, response.Header().Get("Cache-Control"))
	}

	request = httptest.NewRequest(http.MethodGet, stampedURL, nil)
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	s.router.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified {
		t.Fatalf("matching static ETag status=%d, want 304", conditional.Code)
	}
}

func TestSecFetchValidationRejectsCrossSiteFrontendWritesOnly(t *testing.T) {
	s := newTestServer(t)

	frontend := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader(""))
	frontend.Header.Set("Sec-Fetch-Site", "cross-site")
	frontendResponse := httptest.NewRecorder()
	s.router.ServeHTTP(frontendResponse, frontend)
	if frontendResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-site frontend write status = %d, want %d", frontendResponse.Code, http.StatusForbidden)
	}

	page := doRequest(s, http.MethodGet, "/", nil)
	var token string
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			token = cookie.Value
		}
	}
	if token == "" {
		t.Fatal("page did not set CSRF token for legacy browser")
	}
	legacy := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader("theme=light&csrf_token="+url.QueryEscape(token)))
	legacy.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	legacy.Header.Set("Accept", "text/html")
	legacy.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	legacy.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	legacyResponse := httptest.NewRecorder()
	s.router.ServeHTTP(legacyResponse, legacy)
	if legacyResponse.Code == http.StatusForbidden {
		t.Fatalf("missing Sec-Fetch headers were rejected: status=%d", legacyResponse.Code)
	}

	apiRequest := httptest.NewRequest(http.MethodPost, "/api/v1/coordinates", strings.NewReader(""))
	apiRequest.Header.Set("Sec-Fetch-Site", "cross-site")
	apiResponse := httptest.NewRecorder()
	s.router.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code == http.StatusForbidden {
		t.Fatalf("API request was rejected by frontend Sec-Fetch validation: status=%d", apiResponse.Code)
	}
}

func TestGPCPrivacyNoticeIsAvailableOnPreferencesPage(t *testing.T) {
	s := newTestServer(t)
	s.config.Web.Headers.HonorSecGPC = true
	request := httptest.NewRequest(http.MethodGet, "/server/preferences", nil)
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Sec-GPC", "1")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "privacy_signal") && !strings.Contains(response.Body.String(), "Global Privacy Control") {
		t.Fatalf("preferences page did not render the privacy signal notice: status=%d", response.Code)
	}
}

func TestPreferencesRequireDoubleSubmitCSRF(t *testing.T) {
	s := newTestServer(t)
	page := doRequest(s, http.MethodGet, "/", nil)
	var token string
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			token = cookie.Value
			if cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("csrf cookie attributes = %+v", cookie)
			}
		}
	}
	if token == "" {
		t.Fatal("page did not set csrf_token cookie")
	}
	bad := doRequest(s, http.MethodPost, "/server/preferences", []byte("theme=light"))
	if bad.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF token status = %d, want 403", bad.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader("theme=light&csrf_token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	req.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	accepted := httptest.NewRecorder()
	s.router.ServeHTTP(accepted, req)
	if accepted.Code != http.StatusSeeOther {
		t.Fatalf("POST with CSRF token status = %d, want 303", accepted.Code)
	}
	for _, cookie := range accepted.Result().Cookies() {
		if cookie.Name == "theme" && cookie.Value == "light" && !cookie.HttpOnly {
			return
		}
	}
	t.Fatal("successful preference write did not set a JavaScript-readable theme cookie")
}

func TestPreferencesSaveIgnoresSyncCode(t *testing.T) {
	s := newTestServer(t)
	token := "csrf-code-ignore-token-123456789012"
	form := url.Values{"csrf_token": {token}, "code": {base64.RawURLEncoding.EncodeToString([]byte("theme=light&lang=fr"))}}
	request := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("POST preferences with sync code status=%d, want 303", response.Code)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "theme" || cookie.Name == "lang" {
			t.Errorf("POST /server/preferences applied sync code by setting %q=%q", cookie.Name, cookie.Value)
		}
	}
}

func TestPreferencesAPIMirrorDoesNotRequireCSRF(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences", strings.NewReader(`{"theme":"light","lang":"fr"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Theme string `json:"theme"`
			Lang  string `json:"lang"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode API preferences response: %v", err)
	}
	if rec.Code != http.StatusOK || !response.OK || response.Data.Theme != "light" || response.Data.Lang != "fr" {
		t.Fatalf("POST API preferences status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestPreferencesAPIMirrorRequiresPreferenceConsent(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/server/preferences", strings.NewReader(`{"theme":"light","lang":"fr"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	var response struct {
		Data struct {
			Applied         []string `json:"applied"`
			Ignored         []string `json:"ignored"`
			ConsentRequired bool     `json:"consent_required"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode API preferences response (status %d, body %q): %v", rec.Code, rec.Body.String(), err)
	}
	if rec.Code != http.StatusOK || len(response.Data.Applied) != 0 || len(response.Data.Ignored) != 2 || !response.Data.ConsentRequired {
		t.Fatalf("preference consent response status=%d body=%q", rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "theme" || cookie.Name == "lang" {
			t.Errorf("preference cookie written without consent: %s", cookie.Name)
		}
	}
}

func TestPreferenceSyncExportsAndImports(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/sync", nil)
	request.Header.Set("Accept", "application/json")
	request.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
	request.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
	export := httptest.NewRecorder()
	s.router.ServeHTTP(export, request)
	var exported struct {
		Data struct {
			URL  string `json:"url"`
			Code string `json:"code"`
		} `json:"data"`
	}
	if export.Code != http.StatusOK || json.Unmarshal(export.Body.Bytes(), &exported) != nil {
		t.Fatalf("sync export status=%d body=%q", export.Code, export.Body.String())
	}
	if !strings.Contains(exported.Data.URL, "lang=fr") || string(mustBase64URLDecode(t, exported.Data.Code)) != "lang=fr&theme=light" {
		t.Fatalf("unexpected sync export: %+v", exported.Data)
	}
	padded := base64.URLEncoding.EncodeToString([]byte("lang=fr&theme=light"))
	importRequest := httptest.NewRequest(http.MethodGet, "/api/v1/server/preferences/sync/import?code="+padded+"&theme=dark&lang=de-DE", nil)
	importRequest.Header.Set("Accept", "application/json")
	importRequest.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	imported := httptest.NewRecorder()
	s.router.ServeHTTP(imported, importRequest)
	var result struct {
		Data struct {
			Theme   string   `json:"theme"`
			Lang    string   `json:"lang"`
			Applied []string `json:"applied"`
		} `json:"data"`
	}
	if imported.Code != http.StatusOK || json.Unmarshal(imported.Body.Bytes(), &result) != nil {
		t.Fatalf("sync import status=%d body=%q", imported.Code, imported.Body.String())
	}
	if result.Data.Theme != "dark" || result.Data.Lang != "de" || len(result.Data.Applied) != 2 {
		t.Fatalf("direct values did not override code values: %+v", result.Data)
	}
}

func TestPreferencesConsentAndReset(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Privacy.Data.Sold = true
	s := newTestServerWithConfig(t, cfg)
	token := "csrf-test-token-12345678901234567890"
	values := url.Values{"csrf_token": {token}, "consent_form": {"1"}, "consent_preferences": {"on"}, "ccpa_opt_out": {"on"}}
	req := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("save consent status=%d body=%q", rec.Code, rec.Body.String())
	}
	var consentOK, ccpaOK bool
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case "cookie_consent":
			var got map[string]interface{}
			decoded, err := url.QueryUnescape(cookie.Value)
			if err == nil && json.Unmarshal([]byte(decoded), &got) == nil && got["essential"] == true && got["preferences"] == true && got["analytics"] == false {
				consentOK = true
			}
		case "ccpa_opt_out":
			ccpaOK = cookie.Value == "true"
		}
	}
	if !consentOK || !ccpaOK {
		t.Fatalf("consent cookies missing or invalid: %+v", rec.Result().Cookies())
	}
	reset := url.Values{"csrf_token": {token}, "action": {"reset"}}
	resetRequest := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader(reset.Encode()))
	resetRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resetRequest.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	resetRequest.AddCookie(&http.Cookie{Name: "countries_pref_sort", Value: "name"})
	resetRequest.AddCookie(&http.Cookie{Name: "dismissed_announcements", Value: "maintenance"})
	resetRequest.AddCookie(&http.Cookie{Name: "session_token", Value: "preserve-session"})
	resetRequest.AddCookie(&http.Cookie{Name: ownerTokenCookieName, Value: "preserve-owner"})
	resetResponse := httptest.NewRecorder()
	s.router.ServeHTTP(resetResponse, resetRequest)
	if resetResponse.Code != http.StatusSeeOther || resetResponse.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("reset status=%d Clear-Site-Data=%q", resetResponse.Code, resetResponse.Header().Get("Clear-Site-Data"))
	}
	deleted := make(map[string]bool)
	for _, cookie := range resetResponse.Result().Cookies() {
		if cookie.MaxAge < 0 && cookie.Path == "/" {
			deleted[cookie.Name] = true
		}
		if cookie.Name == "csrf_token" || cookie.Name == ownerTokenCookieName || cookie.Name == "session_token" {
			t.Errorf("reset unexpectedly changed credential cookie %q", cookie.Name)
		}
	}
	for _, name := range []string{"theme", "lang", "cookie_consent", "ccpa_opt_out", "countries_build", "countries_pref_sort", "dismissed_announcements"} {
		if !deleted[name] {
			t.Errorf("reset did not expire %q with Max-Age=0", name)
		}
	}
}

func TestConsentBannerAndSubmission(t *testing.T) {
	s := newTestServer(t)
	pageRequest := httptest.NewRequest(http.MethodGet, "/server/about", nil)
	pageRequest.Header.Set("Accept", "text/html")
	page := httptest.NewRecorder()
	s.router.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/server/consent"`) || !strings.Contains(page.Body.String(), `name="choice" value="decline"`) {
		t.Fatalf("first visit did not render the consent banner: status=%d", page.Code)
	}
	var token string
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == "csrf_token" {
			token = cookie.Value
		}
	}
	if token == "" {
		t.Fatal("page did not issue a CSRF token")
	}
	form := url.Values{"csrf_token": {token}, "choice": {"decline"}, "redirect": {"/server/about"}}
	request := httptest.NewRequest(http.MethodPost, "/server/consent", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/server/about" {
		t.Fatalf("consent POST status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	var consent string
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "cookie_consent" {
			consent = cookie.Value
		}
	}
	if consent == "" {
		t.Fatal("consent POST did not set cookie")
	}
	pageRequest = httptest.NewRequest(http.MethodGet, "/server/about", nil)
	pageRequest.Header.Set("Accept", "text/html")
	pageRequest.AddCookie(&http.Cookie{Name: "cookie_consent", Value: consent})
	page = httptest.NewRecorder()
	s.router.ServeHTTP(page, pageRequest)
	if strings.Contains(page.Body.String(), `action="/server/consent"`) {
		t.Fatal("consent banner remained after a valid choice")
	}
}

func TestConsentWithdrawalClearSiteData(t *testing.T) {
	s := newTestServer(t)
	token := "csrf-consent-withdrawal-token-123456"
	consent := url.QueryEscape(`{"essential":true,"preferences":true,"analytics":true,"timestamp":1}`)
	form := url.Values{"csrf_token": {token}, "choice": {"decline"}, "redirect": {"/server/preferences"}}
	request := httptest.NewRequest(http.MethodPost, "/server/consent", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: consent})
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Clear-Site-Data") != `"cache", "storage"` {
		t.Fatalf("withdrawal status=%d Clear-Site-Data=%q", response.Code, response.Header().Get("Clear-Site-Data"))
	}

	cfg := config.DefaultConfig()
	cfg.Web.Headers.ClearSiteData.OnConsentWithdrawal = false
	disabled := newTestServerWithConfig(t, cfg)
	request = httptest.NewRequest(http.MethodPost, "/server/consent", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: consent})
	response = httptest.NewRecorder()
	disabled.router.ServeHTTP(response, request)
	if got := response.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("disabled consent withdrawal emitted Clear-Site-Data %q", got)
	}
}

func TestAutomaticLanguageSelectionClearsOverride(t *testing.T) {
	s := newTestServer(t)
	token := "csrf-language-token-12345678901234567890"
	form := url.Values{"csrf_token": {token}, "lang": {"auto"}, "redirect": {"/server/preferences"}}
	request := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: token})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("automatic language save status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "lang" && cookie.MaxAge < 0 {
			return
		}
	}
	t.Fatal("automatic language choice did not expire the language cookie")
}

func TestPreferenceRedirectValidation(t *testing.T) {
	s := newTestServer(t)
	for _, target := range []string{"//evil.example", "/\\evil", "https://evil.example/path", "/server/preferences/sync/import"} {
		if isLocalReturnPath(target) {
			t.Errorf("isLocalReturnPath(%q) = true, want false", target)
		}
	}
	for _, target := range []string{"/\r\nLocation: https://evil.example", "/server/preferences?x=1\r\n"} {
		if isLocalReturnPath(target) {
			t.Errorf("isLocalReturnPath accepted CR/LF target %q", target)
		}
	}
	if !isLocalReturnPath("/search?q=united") || !isLocalReturnPath("/server/preferences") || !isLocalReturnPath("/server/preferences/sync") {
		t.Error("isLocalReturnPath rejected a local relative target")
	}
	req := httptest.NewRequest(http.MethodPost, "http://countries.test/server/preferences", nil)
	req.Header.Set("Referer", "http://countries.test/search?q=united")
	if got := s.preferencesRedirect(req, "/"); got != "/search?q=united" {
		t.Errorf("same-origin Referer target = %q, want /search?q=united", got)
	}
	req.Header.Set("Referer", "http://evil.test/search")
	if got := s.preferencesRedirect(req, "/"); got != "/" {
		t.Errorf("cross-origin Referer target = %q, want /", got)
	}
}

func mustBase64URLDecode(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestPrivacyBuiltInContentIsLocalized(t *testing.T) {
	s := newTestServer(t)
	privacy := s.config.Server.Privacy.BuiltInContent("es")
	if !strings.Contains(privacy.DataCollection, "Recopilamos únicamente") || !strings.Contains(privacy.DataUsage, "Sus datos se utilizan") || !strings.Contains(privacy.DataSecurity, "Cómo protegemos") {
		t.Fatalf("Spanish privacy content was not selected: %+v", privacy)
	}
}

func TestPrivacyAPIMirrorsLocalizedLegalContent(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/privacy", nil)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "fr")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	var envelope struct {
		Data struct {
			Content map[string]string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode privacy API response: %v; body=%q", err, response.Body.String())
	}
	if response.Code != http.StatusOK || !strings.Contains(envelope.Data.Content["data_collection"], "Nous recueillons") || !strings.Contains(envelope.Data.Content["data_usage"], "Vos données sont utilisées") || !strings.Contains(envelope.Data.Content["data_security"], "Comment nous protégeons") {
		t.Fatalf("French privacy API content mismatch: status=%d content=%+v", response.Code, envelope.Data.Content)
	}
}

func TestAboutAndHelpPagesDescribeProjectFeatures(t *testing.T) {
	s := newTestServer(t)
	about := s.infoForPath("/server/about")
	help := s.infoForPath("/server/help")
	var aboutText, helpText strings.Builder
	for _, section := range about.Sections {
		aboutText.WriteString(section.Title + " " + section.Text + " " + strings.Join(section.Bullets, " ") + " ")
	}
	for _, section := range help.Sections {
		helpText.WriteString(section.Title + " " + section.Text + " " + strings.Join(section.Bullets, " ") + " ")
	}
	for _, term := range []string{"ODbL", "CC BY 4.0", "World Bank", "GraphQL", "countries-cli"} {
		if !strings.Contains(aboutText.String(), term) {
			t.Errorf("about page does not include %q", term)
		}
	}
	for _, term := range []string{"/countries/search?q=united", "/coordinates?lat=40.7&lon=-74.0", "countries-cli", "Retry-After"} {
		if !strings.Contains(helpText.String(), term) {
			t.Errorf("help page does not include %q", term)
		}
	}
	helpAPI := doRequest(s, http.MethodGet, "/api/v1/server/help", nil)
	var helpEnvelope struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(helpAPI.Body.Bytes(), &helpEnvelope); err != nil {
		t.Fatalf("decode built-in help API response: %v", err)
	}
	if helpAPI.Code != http.StatusOK || !strings.Contains(helpEnvelope.Data.Content, "Find countries") {
		t.Fatalf("built-in help API content is missing project guidance: status=%d content=%q", helpAPI.Code, helpEnvelope.Data.Content)
	}
}

func TestHelpPageUsesConfiguredMarkdownAndAPIContract(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Help.Content = "# Operator guidance\n\nRead the [MCP guide](/server/help/mcp)."
	s := newTestServerWithConfig(t, cfg)

	page := doRequest(s, http.MethodGet, "/server/help", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `id="help-configured-content"`) || !strings.Contains(page.Body.String(), "Operator guidance") || strings.Contains(page.Body.String(), `id="help-search"`) {
		t.Fatalf("configured help page did not replace built-in sections: status=%d body=%q", page.Code, page.Body.String())
	}

	apiResponse := doRequest(s, http.MethodGet, "/api/v1/server/help", nil)
	if apiResponse.Code != http.StatusOK {
		t.Fatalf("help API status = %d, want 200", apiResponse.Code)
	}
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(apiResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode help API response: %v; body=%q", err, apiResponse.Body.String())
	}
	if len(envelope.Data) != 4 {
		t.Fatalf("help API data has %d fields, want title, content, format and pages", len(envelope.Data))
	}
	var content, format string
	if err := json.Unmarshal(envelope.Data["content"], &content); err != nil {
		t.Fatalf("decode help API content: %v", err)
	}
	if err := json.Unmarshal(envelope.Data["format"], &format); err != nil {
		t.Fatalf("decode help API format: %v", err)
	}
	if content != cfg.Server.Pages.Help.Content || format != "markdown" {
		t.Fatalf("help API content/format = %q/%q, want configured Markdown/%q", content, format, "markdown")
	}
	var pages []map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data["pages"], &pages); err != nil {
		t.Fatalf("decode registered help pages: %v", err)
	}
	for index, registeredPage := range pages {
		if len(registeredPage) != 2 || registeredPage["title"] == nil || registeredPage["path"] == nil {
			t.Errorf("help page %d has fields %v, want exactly title and path", index, registeredPage)
		}
	}
}

func TestHelpContentMapUsesRequestLanguageAndFallbackOrder(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Pages.Help.Content = map[string]any{
		"fr":      "## Bonjour",
		"default": "## Default language content",
		"en":      "## English",
	}
	s := newTestServerWithConfig(t, cfg)
	for _, tc := range []struct {
		language string
		want     string
	}{
		{language: "fr", want: "## Bonjour"},
		{language: "es", want: "## Default language content"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/server/help?lang="+tc.language, nil)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, request)
		var envelope struct {
			Data struct {
				Content string `json:"content"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode %s help response: %v", tc.language, err)
		}
		if response.Code != http.StatusOK || envelope.Data.Content != tc.want {
			t.Errorf("%s help content = %q, status=%d; want %q", tc.language, envelope.Data.Content, response.Code, tc.want)
		}
	}

	cfg.Server.Pages.Help.Content = map[string]any{"zh": "## Chinese override", "de": "## German override"}
	s = newTestServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/help?lang=en", nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	var envelope struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode built-in fallback help response: %v", err)
	}
	if response.Code != http.StatusOK || !strings.Contains(envelope.Data.Content, "Find countries") || strings.Contains(envelope.Data.Content, "override") {
		t.Fatalf("help content without language/default fallback = %q, status=%d; want built-in English help", envelope.Data.Content, response.Code)
	}
}

func TestDPOPageAndAPIFallbackContact(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Contact.General.Email = "privacy@example.test"
	s := newTestServerWithConfig(t, cfg)
	page := doRequest(s, http.MethodGet, "/server/dpo", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "privacy@example.test") || !strings.Contains(page.Body.String(), "/server/privacy") {
		t.Fatalf("DPO page status=%d body=%q", page.Code, page.Body.String())
	}
	apiResponse := doRequest(s, http.MethodGet, "/api/v1/server/dpo", nil)
	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			Name       string `json:"name"`
			Email      string `json:"email"`
			PrivacyURL string `json:"privacy_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(apiResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("DPO API returned invalid JSON: %v", err)
	}
	if apiResponse.Code != http.StatusOK || !body.OK || body.Data.Email != "privacy@example.test" || !strings.HasSuffix(body.Data.PrivacyURL, "/server/privacy") {
		t.Errorf("DPO API response status=%d body=%+v", apiResponse.Code, body)
	}
}

func TestContactChooserAndTypeSpecificFields(t *testing.T) {
	s := newTestServer(t)
	chooser := doRequest(s, http.MethodGet, "/server/contact", nil)
	mainStart, mainEnd := strings.Index(chooser.Body.String(), "<main"), strings.Index(chooser.Body.String(), "</main>")
	if chooser.Code != http.StatusOK || mainStart < 0 || mainEnd < mainStart || strings.Contains(chooser.Body.String()[mainStart:mainEnd], `<input`) || strings.Contains(chooser.Body.String()[mainStart:mainEnd], `<form`) || !strings.Contains(chooser.Body.String(), `name="robots" content="noindex, nofollow"`) {
		t.Fatalf("contact chooser status=%d body=%q", chooser.Code, chooser.Body.String())
	}
	for _, typ := range []string{"abuse", "dmca", "privacy"} {
		page := doRequest(s, http.MethodGet, "/server/contact?type="+typ, nil)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `name="type" value="`+typ+`"`) {
			t.Errorf("contact type %s status=%d body=%q", typ, page.Code, page.Body.String())
		}
		for other, field := range map[string]string{"abuse": `name="reason"`, "dmca": `name="signature"`, "privacy": `name="request_kind"`} {
			if other != typ && strings.Contains(page.Body.String(), field) {
				t.Errorf("%s form unexpectedly contains %s", typ, field)
			}
		}
	}
}

func TestContactShapeAcceptsRequiredTypeField(t *testing.T) {
	form := url.Values{
		"type":         {"abuse"},
		"name":         {"Reporter"},
		"email":        {"reporter@example.test"},
		"reported_url": {"https://countries.example.test/country/US"},
		"reason":       {"terms_violation"},
		"subject":      {"Report"},
		"message":      {"Details"},
		"step":         {"submit"},
		"form_token":   {"issued.nonce.signature"},
	}
	req := httptest.NewRequest(http.MethodPost, "/server/contact", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}
	if !validContactShape(req, "abuse", false) {
		t.Fatal("valid abuse submission with required type field was rejected")
	}
}

func TestSecurityReportStatusTokenCanBeUsedOncePerDay(t *testing.T) {
	s := newTestServer(t)
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "security-report.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close security report database: %v", err)
		}
	})
	s.db = db
	token := "one-time-researcher-token"
	hash := sha256.Sum256([]byte(token))
	_, err = db.Exec(`INSERT INTO security_reports(tracking_id,id_window,severity,component,body_enc,body_alg,token_hash) VALUES(?,?,?,?,?,?,?)`, "sec_0123456789abcdef", time.Now().Unix()/172800, "high", "api", []byte("encrypted"), "aes-256-gcm", hex.EncodeToString(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	path := "/server/security/report/sec_0123456789abcdef?token=" + url.QueryEscape(token)
	first := doRequest(s, http.MethodGet, path, nil)
	if first.Code != http.StatusOK || first.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("first status visit status=%d headers=%v body=%q", first.Code, first.Header(), first.Body.String())
	}
	second := doRequest(s, http.MethodGet, path, nil)
	if second.Code != http.StatusNotFound {
		t.Fatalf("same-day token reuse status=%d, want 404", second.Code)
	}
	if _, err := db.Exec(`UPDATE security_reports SET token_last_used=? WHERE tracking_id=?`, time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), "sec_0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	third := doRequest(s, http.MethodGet, path, nil)
	if third.Code != http.StatusOK {
		t.Fatalf("next-day token use status=%d, want 200 body=%q", third.Code, third.Body.String())
	}
}

func TestSecurityIDWindowReturnsValidatedWindowWithoutStoringIdentifier(t *testing.T) {
	key := []byte("test contact signing key")
	now := time.Unix(3456789000, 0).UTC()
	current := now.Unix() / 172800
	for _, window := range []int64{current, current - 1} {
		got, ok := securityIDWindow(key, securityIDFor(key, window), now)
		if !ok || got != window {
			t.Errorf("securityIDWindow(current=%d) = (%d, %v), want (%d, true)", window, got, ok, window)
		}
	}
	if _, ok := securityIDWindow(key, securityIDFor(key, current-2), now); ok {
		t.Fatal("securityIDWindow accepted an expired identifier")
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
	if got := record.Header().Get("Reporting-Endpoints"); !strings.Contains(got, "default=") || !strings.Contains(got, "nel=") {
		t.Errorf("Reporting-Endpoints = %q, want default and NEL endpoints", got)
	}
	if got := record.Header().Get("Report-To"); !strings.Contains(got, `"group":"default"`) || !strings.Contains(got, `"group":"nel"`) {
		t.Errorf("Report-To = %q, want default and NEL groups", got)
	}
	if got := record.Header().Get("NEL"); !strings.Contains(got, `"report_to":"nel"`) {
		t.Errorf("NEL = %q, want report_to nel", got)
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
	for _, code := range []string{"TST", "001"} {
		result := doRequest(s, http.MethodGet, "/api/v1/countries/"+code, nil)
		if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "Testland") {
			t.Errorf("GET country by %q: status=%d body=%q", code, result.Code, result.Body.String())
		}
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
	if rec.Body.String() != "2\n" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "2\\n")
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
	var manifest struct {
		Icons []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	for _, want := range []struct{ name, size, purpose string }{
		{"icon-192.png", "192x192", "any"}, {"icon-512.png", "512x512", "any"},
		{"icon-maskable-192.png", "192x192", "maskable"}, {"icon-maskable-512.png", "512x512", "maskable"},
	} {
		found := false
		for _, icon := range manifest.Icons {
			if strings.Contains(icon.Src, want.name) && icon.Sizes == want.size && icon.Purpose == want.purpose {
				found = true
				asset := doRequest(s, http.MethodGet, strings.Split(icon.Src, "?")[0], nil)
				if asset.Code != http.StatusOK || !strings.HasPrefix(asset.Header().Get("Content-Type"), "image/png") {
					t.Errorf("manifest icon %s response status=%d content-type=%q", want.name, asset.Code, asset.Header().Get("Content-Type"))
				}
				break
			}
		}
		if !found {
			t.Errorf("manifest omitted icon %s with size %s and purpose %s", want.name, want.size, want.purpose)
		}
	}
}

func TestPagesAndPWAAssetsHonorTrustedProxyPrefix(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://example.com/server/about", nil)
	request.Header.Set("Accept", "text/html")
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-Prefix", "/countries")
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{`content="/countries"`, `href="/countries/static/css/common.css?v=test-version-abc123"`, `href="/countries/static/css/components.css?v=test-version-abc123"`, `href="/countries/static/css/public.css?v=test-version-abc123"`, `href="/countries/search"`, `action="/countries/server/preferences"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("page does not contain prefixed URL %q", want)
		}
	}
	manifestRequest := httptest.NewRequest(http.MethodGet, "http://example.com/manifest.json", nil)
	manifestRequest.Header.Set("Accept", "application/json")
	manifestRequest.RemoteAddr = "127.0.0.1:1234"
	manifestRequest.Header.Set("X-Forwarded-Prefix", "/countries")
	manifest := httptest.NewRecorder()
	s.router.ServeHTTP(manifest, manifestRequest)
	if !strings.Contains(manifest.Body.String(), `"start_url": "/countries/"`) {
		t.Errorf("manifest start_url omitted trusted proxy prefix: %s", manifest.Body.String())
	}
}

func TestSingleFrontendScriptIsServed(t *testing.T) {
	s := newTestServer(t)
	app := doRequest(s, http.MethodGet, "/static/js/app.js", nil)
	if app.Code != http.StatusOK || !strings.Contains(app.Body.String(), "searchForm") || !strings.Contains(app.Body.String(), "coordForm") {
		t.Fatalf("unified frontend script status=%d body=%q", app.Code, app.Body.String())
	}
	for _, path := range []string{"/static/js/main.js", "/static/js/home.js", "/static/js/search.js", "/static/js/coordinates.js", "/static/js/graphql.js"} {
		if response := doRequest(s, http.MethodGet, path, nil); response.Code != http.StatusNotFound {
			t.Errorf("retired script %s status=%d, want 404", path, response.Code)
		}
	}
}

func TestPWAActionsUseLocalizedAccessibleCopy(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/?lang=fr", nil)
	request.Header.Set("Accept", "text/html")
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.PathEscape(`{"essential":true,"preferences":true}`)})
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	for _, required := range []string{
		`<html lang="fr"`,
		`id="offline-indicator"`,
		`Vous êtes hors ligne. Certaines fonctions peuvent être indisponibles.`,
		`data-update-available="Une nouvelle version est disponible."`,
		`data-install-label="Installer l&#39;application"`,
		`data-ios-install-step1="Touchez le bouton Partager dans Safari."`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("French PWA page omitted %q", required)
		}
	}
	app := doRequest(s, http.MethodGet, "/static/js/app.js", nil).Body.String()
	for _, required := range []string{"navigator.onLine", "addEventListener('offline'", "beforeinstallprompt", "appinstalled", "showModal()", "aria-labelledby", "iosInstallStep3"} {
		if !strings.Contains(app, required) {
			t.Errorf("PWA frontend omitted %q", required)
		}
	}
}

func TestFallbackHTMLUsesServerBuildStampAndThemeAssets(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://example.com/missing-template", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-Prefix", "/countries")
	request.Header.Set("Accept-Language", "fr")
	request.Header.Set("Accept", "text/html")
	recorder := httptest.NewRecorder()
	s.renderTemplateResponse(recorder, request, "missing-template.html", nil, http.StatusOK)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("fallback status = %d, want 500", recorder.Code)
	}
	for _, required := range []string{`lang="fr"`, `dir="ltr"`, `class="theme-dark"`, `href="/countries/static/css/common.css?v=test-version-abc123"`, `href="/countries/static/css/components.css?v=test-version-abc123"`, `href="/countries/static/css/public.css?v=test-version-abc123"`, `id="main-content"`} {
		if !strings.Contains(recorder.Body.String(), required) {
			t.Errorf("fallback response omitted %q", required)
		}
	}
	if strings.Contains(recorder.Body.String(), "data-theme=") {
		t.Error("fallback response still uses data-theme instead of a theme class")
	}
	if recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("ETag") != `"test-version-abc123"` {
		t.Errorf("fallback cache headers = %q / %q", recorder.Header().Get("Cache-Control"), recorder.Header().Get("ETag"))
	}
	cookie := recorder.Result().Cookies()
	if len(cookie) == 0 || cookie[0].Name != buildCookieName || cookie[0].Value != "test-version-abc123" {
		t.Errorf("fallback build cookie = %#v", cookie)
	}
}

func TestHandleServiceWorker(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/sw.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, required := range []string{
		"const CACHE_VERSION = 'test-version-abc123'",
		"const CACHE_NAME = CACHE_PREFIX + CACHE_VERSION",
		"Countries API-cache-",
		"APP_BASE + '/static/css/common.css?v=' + CACHE_VERSION",
		"APP_BASE + '/static/css/components.css?v=' + CACHE_VERSION",
		"APP_BASE + '/static/css/public.css?v=' + CACHE_VERSION",
		"APP_BASE + '/static/js/app.js?v=' + CACHE_VERSION",
		"APP_BASE + '/static/icons/icon-192.png?v=' + CACHE_VERSION",
		"APP_BASE + '/static/icons/icon-512.png?v=' + CACHE_VERSION",
		"APP_BASE + '/static/icons/icon-maskable-192.png?v=' + CACHE_VERSION",
		"APP_BASE + '/static/icons/icon-maskable-512.png?v=' + CACHE_VERSION",
		"const NAV_TIMEOUT_MS = 8000",
		"self.clients.claim()",
		"SKIP_WAITING",
		"offlineFallbackResponse()",
		"request.mode === 'navigate'",
		"response.redirected",
		"response.headers.get('X-Offline-Cache') !== 'public'",
		"Countries API-offline-strings",
		"const stored = await cache.match(new URL(APP_BASE + '/offline-strings.json'",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("service worker omitted %q", required)
		}
	}
	if strings.Contains(body, `'<h1>`) {
		t.Error("offline fallback must not render an h1")
	}
	if strings.Contains(body, "APP_BASE + '/'") || strings.Contains(body, "skipWaiting();\n});\n\nself.addEventListener('activate'") {
		t.Errorf("service worker precaches HTML or activates updates without approval: %s", body)
	}
}

func TestAutodiscoverPublishesCurrentAPIVersionAndPublicOrigins(t *testing.T) {
	t.Setenv("DOMAIN", "request.example.test,api.example.test,hidden.onion,service.b32.i2p")
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "https://request.example.test/api/autodiscover", nil)
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	var response struct {
		Data struct {
			API struct {
				CurrentVersion string `json:"current_version"`
			} `json:"api"`
			URLs []string `json:"urls"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode autodiscover response (status %d, body %q): %v", recorder.Code, recorder.Body.String(), err)
	}
	if recorder.Code != http.StatusOK || response.Data.API.CurrentVersion == "" {
		t.Fatalf("autodiscover status/version = %d/%q", recorder.Code, response.Data.API.CurrentVersion)
	}
	if len(response.Data.URLs) < 2 || response.Data.URLs[0] != "https://request.example.test" {
		t.Fatalf("public URLs = %#v, want request origin first", response.Data.URLs)
	}
	for _, origin := range response.Data.URLs {
		if strings.Contains(origin, ".onion") || strings.Contains(origin, ".b32.i2p") || strings.Contains(strings.TrimPrefix(origin, "https://"), "/") {
			t.Errorf("non-public or non-origin autodiscover URL %q", origin)
		}
	}
}

func TestStaticCLIBinaryNameExcludesGUIArtifacts(t *testing.T) {
	for _, name := range []string{
		"countries-cli-linux-amd64",
		"countries-cli-windows-arm64.exe",
	} {
		if !isStaticCLIBinaryName(name) {
			t.Errorf("isStaticCLIBinaryName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"countries-cli-gui-linux-amd64",
		"countries-cli-gui-windows-arm64.exe",
		"../countries-cli-linux-amd64",
	} {
		if isStaticCLIBinaryName(name) {
			t.Errorf("isStaticCLIBinaryName(%q) = true, want false", name)
		}
	}
}

func TestAutodiscoverExposesOverlayAddressesOnlyWhenOptedIn(t *testing.T) {
	s := newTestServer(t)
	s.overlayMu.Lock()
	s.torHealth = HealthOverlayInfo{Enabled: true, Running: true, Hostname: "hiddenservice.onion"}
	s.i2pHealth = HealthI2PInfo{Enabled: true, Running: true, Hostname: "destination.b32.i2p"}
	s.overlayMu.Unlock()
	request := httptest.NewRequest(http.MethodGet, "https://example.test/api/autodiscover", nil)
	request.Header.Set("Accept", "application/json")
	withoutConsent := httptest.NewRecorder()
	s.router.ServeHTTP(withoutConsent, request)
	if strings.Contains(withoutConsent.Body.String(), "hiddenservice.onion") || strings.Contains(withoutConsent.Body.String(), "destination.b32.i2p") {
		t.Fatal("autodiscover exposed overlay addresses without operator opt-in")
	}
	s.config.Server.Tor.Expose = true
	s.config.Server.I2P.Expose = true
	withConsent := httptest.NewRecorder()
	withConsentRequest := httptest.NewRequest(http.MethodGet, "https://example.test/api/autodiscover", nil)
	withConsentRequest.Header.Set("Accept", "application/json")
	s.router.ServeHTTP(withConsent, withConsentRequest)
	if !strings.Contains(withConsent.Body.String(), "hiddenservice.onion") || !strings.Contains(withConsent.Body.String(), "destination.b32.i2p") {
		t.Fatalf("autodiscover omitted opted-in overlay addresses: %s", withConsent.Body.String())
	}
}

func TestMaintenanceModeBlocksWritesAndAppearsInHealth(t *testing.T) {
	s := newTestServer(t)
	s.SetSelfHealingState("database_connection", time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 2, false)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/coordinates", strings.NewReader("{}"))
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "30" || recorder.Header().Get("X-Maintenance-Reason") != "database_connection" {
		t.Fatalf("maintenance write response = %d headers=%v", recorder.Code, recorder.Header())
	}
	if !strings.Contains(recorder.Body.String(), `"error": "MAINTENANCE"`) || !strings.Contains(recorder.Body.String(), `"self_healing": true`) {
		t.Fatalf("maintenance error body = %s", recorder.Body.String())
	}
	health := httptest.NewRecorder()
	healthRequest := httptest.NewRequest(http.MethodGet, "/server/healthz", nil)
	healthRequest.Header.Set("Accept", "application/json")
	s.router.ServeHTTP(health, healthRequest)
	if health.Code != http.StatusServiceUnavailable || !strings.Contains(health.Body.String(), `"exhausted": false`) || !strings.Contains(health.Body.String(), `"attempts": 2`) {
		t.Fatalf("maintenance health response = %d %s", health.Code, health.Body.String())
	}
}

func TestServiceWorkerUsesTrustedProxyPrefix(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://example.com/sw.js", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-Prefix", "/countries")
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), "APP_BASE + '/static/js/app.js?v=' + CACHE_VERSION") {
		t.Errorf("service worker cache path omitted prefix: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "APP_BASE = '/countries'") {
		t.Errorf("service worker API/cache scope omitted trusted proxy prefix: %s", recorder.Body.String())
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
	if !strings.Contains(body, "Allow: /") || !strings.Contains(body, "Disallow: /debug") || !strings.Contains(body, "Sitemap: http://example.com/sitemap.xml") {
		t.Errorf("body = %q, want configured allow/deny entries", body)
	}
}

func TestAboutPageShowsRequiredGeoIPAttribution(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/server/about", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>`) {
		t.Fatalf("about page does not include the required visible DB-IP attribution")
	}
}

func TestHandleSitemapContainsOnlyAllowedPublicPages(t *testing.T) {
	s := newTestServer(t)
	s.config.WebRobots.Deny = append(s.config.WebRobots.Deny, "/server/security")
	rec := doRequest(s, http.MethodGet, "/sitemap.xml", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"http://example.com/", "http://example.com/search", "http://example.com/server/docs/swagger"} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap missing %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{"/api/", "/server/contact", "/server/preferences", "/server/security"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("sitemap contains forbidden path %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", rec.Header().Get("Content-Type"))
	}
}

func TestBuildURLUsesTrustedProxyAndRejectsUntrustedForwardedHost(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://public.example/page", nil)
	request.RemoteAddr = "203.0.113.8:1234"
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := s.BuildURL(request, "/server/about"); got != "http://public.example/server/about" {
		t.Errorf("untrusted proxy headers produced URL %q", got)
	}
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-Host", "public.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Prefix", "/countries")
	if got := s.BuildURL(request, "/server/about"); got != "https://public.example/countries/server/about" {
		t.Errorf("trusted proxy URL = %q", got)
	}
}

func TestBuildURLHonorsConfiguredDomainBoundary(t *testing.T) {
	previous, hadPrevious := os.LookupEnv("DOMAIN")
	if err := os.Setenv("DOMAIN", "example.com"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv("DOMAIN", previous)
		} else {
			_ = os.Unsetenv("DOMAIN")
		}
	})
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://attacker.com/page", nil)
	if got := s.BuildURL(request, "/server/about"); got == "" || strings.Contains(got, "attacker.com") || !strings.HasSuffix(got, "/server/about") {
		t.Errorf("unlisted host produced URL %q", got)
	}
	request = httptest.NewRequest(http.MethodGet, "http://sub.example.com/page", nil)
	if got := s.BuildURL(request, "/server/about"); got != "http://sub.example.com/server/about" {
		t.Errorf("same-base-domain host produced URL %q", got)
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
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Base URL:") || !strings.Contains(rec.Body.String(), "GET /api/v1/countries") || !strings.Contains(rec.Body.String(), "POST /api/v1/server/preferences/tokens/primary") {
			t.Errorf("%s: status=%d body=%q", path, rec.Code, rec.Body.String())
		}
	}
}

// The Web UI pages render through the embedded templates; a status of 200
// confirms the templates parsed and executed without error.
func TestHandleWebUIPages(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/", "/search", "/coordinates", "/server/docs/swagger", "/server/about", "/server/help", "/server/help/mcp", "/server/preferences", "/server/preferences/sync", "/server/privacy", "/server/terms", "/server/security", "/server/contact", "/server/dpo", "/server/docs/graphql"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, request)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200, body: %s", path, rec.Code, rec.Body.String())
			continue
		}
		body := rec.Body.String()
		if path == "/server/help/mcp" && (!strings.Contains(body, `id="project"`) || !strings.Contains(body, "getCountries")) {
			t.Errorf("%s: rendered page is missing project-specific tool guidance", path)
		}
		if path == "/server/preferences/sync" {
			for _, required := range []string{
				`class="code-content copy-value"`, `name="code"`, `action="/server/preferences/sync"`,
				`data-copy=`,
			} {
				if !strings.Contains(body, required) {
					t.Errorf("%s: sync page is missing %q", path, required)
				}
			}
			if strings.Count(body, `class="code-content copy-value"`) != 2 || strings.Count(body, `data-copy=`) != 2 {
				t.Errorf("%s: expected selectable URL and code, each with one Copy control", path)
			}
		}
		if strings.Count(body, "<main") != 1 || strings.Count(body, "<h1") != 1 {
			t.Errorf("%s: rendered page must contain exactly one main and one h1", path)
		}
		skipLinkIndex := strings.Index(body, `class="skip-link"`)
		if strings.Count(body, `class="skip-link"`) != 2 || !strings.Contains(body, `href="#site-navigation"`) || !strings.Contains(body, `Skip to navigation`) || skipLinkIndex < 0 || skipLinkIndex > strings.Index(body, `<header`) {
			t.Errorf("%s: main and navigation skip links must precede the header", path)
		}
		if !strings.Contains(body, `class="header-actions"`) || !strings.Contains(body, `class="nav-links" id="site-navigation"`) || !strings.Contains(body, `id="nav-toggle"`) {
			t.Errorf("%s: rendered page is missing the shared responsive header", path)
		}
		if strings.Contains(body, `<style`) || strings.Contains(body, ` style=`) || strings.Contains(body, ` onclick=`) || strings.Contains(body, `<script>`) {
			t.Errorf("%s: rendered page contains inline CSS, JavaScript, or event handlers", path)
		}
		for _, form := range strings.Split(body, "<form") {
			close := strings.Index(form, "</form>")
			if close < 0 {
				continue
			}
			openingAndBody := form[:close]
			if strings.Contains(openingAndBody, `method="post"`) && !strings.Contains(openingAndBody, `name="csrf_token"`) {
				t.Errorf("%s: POST form is missing a CSRF token", path)
			}
		}
	}
}

func TestSwaggerUIPageUsesLocalAssetsAndVersionedSpec(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/server/docs/swagger", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Swagger UI page status = %d, want 200", response.Code)
	}
	for _, expected := range []string{
		`id="swagger-ui"`,
		`data-spec-url="/api/v1/server/swagger"`,
		`/static/vendor/swagger-ui/swagger-ui.css`,
		`/static/vendor/swagger-ui/swagger-ui-bundle.js`,
		`/static/css/swagger-ui-theme.css`,
		`/static/js/docs-swagger.js`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("Swagger UI page is missing %q", expected)
		}
	}
	if strings.Contains(response.Body.String(), "cdn.") || strings.Contains(response.Body.String(), "validator.swagger.io") {
		t.Fatal("Swagger UI page contains an external runtime dependency")
	}

	for _, path := range []string{
		"/static/vendor/swagger-ui/swagger-ui.css",
		"/static/vendor/swagger-ui/swagger-ui-bundle.js",
		"/static/css/swagger-ui-theme.css",
		"/static/js/docs-swagger.js",
	} {
		assetRequest := httptest.NewRequest(http.MethodGet, path, nil)
		assetResponse := httptest.NewRecorder()
		s.router.ServeHTTP(assetResponse, assetRequest)
		if assetResponse.Code != http.StatusOK || assetResponse.Body.Len() == 0 {
			t.Errorf("asset %s: status=%d body bytes=%d, want non-empty 200", path, assetResponse.Code, assetResponse.Body.Len())
		}
	}
}

func TestGraphiQLPageUsesLocalAssetsAndKeepsNoJavaScriptFallback(t *testing.T) {
	s := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/server/docs/graphql", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GraphiQL page status = %d, want 200", response.Code)
	}
	for _, expected := range []string{
		`id="graphiql"`,
		`data-endpoint="/api/v1/server/graphql"`,
		`/static/vendor/graphiql/graphiql.min.css`,
		`/static/vendor/graphiql/react.production.min.js`,
		`/static/vendor/graphiql/react-dom.production.min.js`,
		`/static/vendor/graphiql/graphiql.min.js`,
		`/static/js/docs-graphql.js`,
		`<noscript>`,
		`id="graphql-form"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("GraphiQL page is missing %q", expected)
		}
	}
	for _, path := range []string{
		"/static/vendor/graphiql/graphiql.min.css",
		"/static/vendor/graphiql/react.production.min.js",
		"/static/vendor/graphiql/react-dom.production.min.js",
		"/static/vendor/graphiql/graphiql.min.js",
		"/static/css/graphiql-theme.css",
		"/static/js/docs-graphql.js",
	} {
		assetRequest := httptest.NewRequest(http.MethodGet, path, nil)
		assetResponse := httptest.NewRecorder()
		s.router.ServeHTTP(assetResponse, assetRequest)
		if assetResponse.Code != http.StatusOK || assetResponse.Body.Len() == 0 {
			t.Errorf("asset %s: status=%d body bytes=%d, want non-empty 200", path, assetResponse.Code, assetResponse.Body.Len())
		}
	}
}

func TestAnnouncementControlsFollowSkipLinks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Announcements.Enabled = true
	cfg.Web.Announcements.Messages = []config.AnnouncementConfig{{
		ID: "maintenance", Type: "warning", Title: "Maintenance", Message: "Scheduled maintenance", Dismissible: true,
	}}
	s := newTestServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET / with announcement returned %d, want 200", response.Code)
	}
	body := response.Body.String()
	mainSkip := strings.Index(body, `href="#main-content"`)
	navigationSkip := strings.Index(body, `href="#site-navigation"`)
	announcement := strings.Index(body, `data-announcement-id="maintenance"`)
	dismiss := strings.Index(body, `class="site-banner-close"`)
	header := strings.Index(body, "<header")
	main := strings.Index(body, "<main")
	if announcement <= navigationSkip || mainSkip < 0 || navigationSkip <= mainSkip || dismiss <= announcement || header <= dismiss || main <= announcement {
		t.Fatalf("announcement/skip-link order is wrong: main skip=%d navigation skip=%d announcement=%d dismiss=%d header=%d main=%d", mainSkip, navigationSkip, announcement, dismiss, header, main)
	}
	if !strings.Contains(body, "Scheduled maintenance") || !strings.Contains(body, `name="id" value="maintenance"`) || strings.Contains(body, `id="announcement-slot"`) {
		t.Fatalf("announcement content or server-rendered dismissal form is missing: %q", body)
	}
}

func TestRenderedPagesUseThemeClasses(t *testing.T) {
	s := newTestServer(t)
	for _, theme := range []string{"dark", "light", "auto"} {
		t.Run(theme, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept", "text/html")
			if theme != "dark" {
				request.AddCookie(&http.Cookie{Name: "theme", Value: theme})
			}
			response := httptest.NewRecorder()
			s.router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("GET / returned %d, want 200", response.Code)
			}
			if !strings.Contains(response.Body.String(), `class="theme-`+theme+`"`) || strings.Contains(response.Body.String(), "data-theme=") {
				t.Errorf("rendered page does not use theme-%s on html", theme)
			}
			body := response.Body.String()
			common := strings.Index(body, `/static/css/common.css`)
			components := strings.Index(body, `/static/css/components.css`)
			public := strings.Index(body, `/static/css/public.css`)
			if common < 0 || components <= common || public <= components {
				t.Errorf("stylesheets are not loaded common, components, public: %d, %d, %d", common, components, public)
			}
		})
	}
}

func TestThemeCSSUsesThemeClasses(t *testing.T) {
	s := newTestServer(t)
	assets := map[string]string{
		"/static/css/common.css":           "html.theme-dark, html.theme-light, html.theme-auto {",
		"/static/css/components.css":       ".btn-primary {",
		"/static/css/public.css":           ".header {",
		"/static/css/swagger-ui-theme.css": swagger.CSSVariables(),
		"/static/css/graphiql-theme.css":   graphqlapi.CSSVariables(),
	}
	commonCSS := doRequest(s, http.MethodGet, "/static/css/common.css", nil)
	if !strings.Contains(commonCSS.Body.String(), "--color-bg-primary: "+commontheme.ThemePaletteDark.Background+";") {
		t.Error("common stylesheet does not contain the shared dark palette")
	}
	if !strings.Contains(commonCSS.Body.String(), "--color-text-muted: "+commontheme.ThemePaletteDark.Muted+";") {
		t.Error("common stylesheet does not contain the shared muted text color")
	}
	for path, required := range assets {
		response := doRequest(s, http.MethodGet, path, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), required) {
			t.Errorf("GET %s status=%d omitted expected theme content", path, response.Code)
		}
	}
	for path := range assets {
		response := doRequest(s, http.MethodGet, path, nil)
		if strings.Contains(response.Body.String(), "[data-theme=") {
			t.Errorf("%s still uses data-theme selectors", path)
		}
	}
}

func TestHTMLAndAPINotFoundResponses(t *testing.T) {
	s := newTestServer(t)
	pageRequest := httptest.NewRequest(http.MethodGet, "/missing", nil)
	pageRequest.Header.Set("Accept", "text/html")
	page := httptest.NewRecorder()
	s.router.ServeHTTP(page, pageRequest)
	if page.Code != http.StatusNotFound || !strings.Contains(page.Header().Get("Content-Type"), "text/html") || !strings.Contains(page.Body.String(), `class="nav-brand"`) || !strings.Contains(page.Body.String(), "not found") {
		t.Fatalf("HTML not-found response status=%d type=%q body=%q", page.Code, page.Header().Get("Content-Type"), page.Body.String())
	}
	apiRequest := httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)
	apiRequest.Header.Set("Accept", "text/html")
	apiRequest.Header.Set("User-Agent", "Mozilla/5.0")
	api := httptest.NewRecorder()
	s.router.ServeHTTP(api, apiRequest)
	if api.Code != http.StatusNotFound || !strings.Contains(api.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("API not-found response status=%d type=%q", api.Code, api.Header().Get("Content-Type"))
	}
}

func TestGraphQLExplorerSubmitsWithoutJavaScript(t *testing.T) {
	s := newTestServer(t)
	form := url.Values{"query": {"{ countries { cca2 } }"}}
	request := httptest.NewRequest(http.MethodPost, "/server/docs/graphql", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `&#34;cca2&#34;`) || !strings.Contains(response.Body.String(), `&#34;TL&#34;`) || !strings.Contains(response.Body.String(), `method="post"`) {
		t.Fatalf("GraphQL HTML form did not execute server-side: status=%d body=%q", response.Code, response.Body.String())
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

	request := httptest.NewRequest(http.MethodOptions, "/api/healthz", nil)
	request.Header.Set("Origin", "https://client.example")
	request.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, request)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, PATCH, DELETE, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
	}
	if rec.Header().Get("Access-Control-Max-Age") != "86400" {
		t.Errorf("Access-Control-Max-Age = %q, want 86400", rec.Header().Get("Access-Control-Max-Age"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("wildcard CORS credentials = %q, want empty", rec.Header().Get("Access-Control-Allow-Credentials"))
	}

	recGet := doRequest(s, http.MethodGet, "/api/healthz", nil)
	if recGet.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("GET without Origin has Access-Control-Allow-Origin = %q, want empty", recGet.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSLearnsOnlyTrustedProxyHosts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.CORS.AllowedOrigins = []string{"https://configured.example"}
	cfg.Server.TrustedProxies.Additional = []string{"192.0.2.10"}
	s := newTestServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	request.RemoteAddr = "192.0.2.10:4000"
	request.Header.Set("X-Forwarded-Host", "learned.example")
	request.Header.Set("Origin", "https://learned.example")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Header().Get("Access-Control-Allow-Origin") != "https://learned.example" {
		t.Fatalf("trusted proxy origin = %q, want learned origin", response.Header().Get("Access-Control-Allow-Origin"))
	}
	request = httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	request.RemoteAddr = "192.0.2.20:4000"
	request.Header.Set("X-Forwarded-Host", "untrusted.example")
	request.Header.Set("Origin", "https://untrusted.example")
	response = httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("untrusted forwarded host received CORS origin %q", response.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSExplicitOriginCredentialsAndDisable(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.CORS = config.CORSConfig{AllowedOrigins: []string{"https://app.example"}, AllowCredentials: true, MaxAge: 86400}
	s := newTestServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodOptions, "/api/healthz", nil)
	request.Header.Set("Origin", "https://app.example")
	request.Header.Set("Access-Control-Request-Method", "POST")
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "https://app.example" || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("credentialed preflight = status %d headers %v", response.Code, response.Header())
	}
	cfg.Server.CORS.AllowedOrigins = []string{""}
	s = newTestServerWithConfig(t, cfg)
	request = httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	request.Header.Set("Origin", "https://app.example")
	response = httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disabled CORS emitted origin %q", response.Header().Get("Access-Control-Allow-Origin"))
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

func TestCountrySearchRejectsOversizedInputAndInvalidPagination(t *testing.T) {
	s := newTestServer(t)
	longQuery := strings.Repeat("x", 257)
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "query exceeds limit", path: "/api/v1/countries/search?q=" + url.QueryEscape(longQuery)},
		{name: "page is not an integer", path: "/api/v1/countries/search?q=Test&page=next"},
		{name: "limit exceeds maximum", path: "/api/v1/countries/search?q=Test&limit=1001"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doRequest(s, http.MethodGet, test.path, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
			}
			var response struct {
				OK      bool   `json:"ok"`
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("invalid JSON error response: %v", err)
			}
			if response.OK || response.Error != "BAD_REQUEST" || response.Message == "" {
				t.Errorf("error envelope = %+v, want BAD_REQUEST with a safe message", response)
			}
			if strings.Contains(rec.Body.String(), longQuery) || strings.Contains(rec.Body.String(), `"status"`) {
				t.Errorf("error response echoed oversized input or duplicated status: %q", rec.Body.String())
			}
		})
	}
}

func TestCoordinateAPIRejectsInvalidRangesAndOversizedBody(t *testing.T) {
	s := newTestServer(t)
	for _, test := range []struct {
		name string
		path string
	}{
		{name: "latitude above range", path: "/api/v1/coordinates?lat=90.1&lon=0"},
		{name: "longitude below range", path: "/api/v1/coordinates?lat=0&lon=-180.1"},
		{name: "non-finite latitude", path: "/api/v1/coordinates?lat=NaN&lon=0"},
		{name: "nearby radius above range", path: "/api/v1/nearby?lat=0&lon=0&radius=20039"},
		{name: "non-finite nearby radius", path: "/api/v1/nearby?lat=0&lon=0&radius=Inf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doRequest(s, http.MethodGet, test.path, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
			}
			var response struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("invalid JSON error response: %v", err)
			}
			if response.OK || response.Error != "BAD_REQUEST" {
				t.Errorf("error envelope = %+v, want BAD_REQUEST", response)
			}
		})
	}

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "missing coordinate", body: []byte(`{"lat":10}`)},
		{name: "out of range coordinate", body: []byte(`{"lat":91,"lon":20}`)},
		{name: "body exceeds limit", body: []byte(strings.Repeat(" ", 4097))},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := doRequest(s, http.MethodPost, "/api/v1/coordinates", test.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
			}
			var response struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("invalid JSON error response: %v", err)
			}
			if response.OK || response.Error != "BAD_REQUEST" {
				t.Errorf("error envelope = %+v, want BAD_REQUEST", response)
			}
		})
	}
}
