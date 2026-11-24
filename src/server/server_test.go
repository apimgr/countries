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

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
	"github.com/apimgr/countries/src/database"
	appmode "github.com/apimgr/countries/src/mode"
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
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
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
		if got.Status != "healthy" || got.Cluster.Enabled || got.Project.Name == "" {
			t.Errorf("%s: health response = %+v, want healthy single-instance response", path, got)
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
	withDebug := newTestServerWithConfig(t, cfg)
	paths := []string{
		"/debug/vars", "/debug/config", "/debug/routes", "/debug/cache", "/debug/db",
		"/debug/scheduler", "/debug/memory", "/debug/goroutines",
		"/api/v1/debug/config", "/api/debug/config",
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
	request := httptest.NewRequest(http.MethodGet, "/static/css/main.css", nil)
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("ETag") == "" {
		t.Fatalf("unstamped static response status=%d headers=%v", response.Code, response.Header())
	}

	stampedURL := "/static/css/main.css?v=" + s.assetStamp()
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
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-Prefix", "/countries")
	recorder := httptest.NewRecorder()
	s.router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", recorder.Code)
	}
	for _, want := range []string{`content="/countries"`, `href="/countries/static/css/main.css?v=test-version-abc123"`, `href="/countries/search"`, `action="/countries/server/preferences"`} {
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("page does not contain prefixed URL %q", want)
		}
	}
	manifestRequest := httptest.NewRequest(http.MethodGet, "http://example.com/manifest.json", nil)
	manifestRequest.RemoteAddr = "127.0.0.1:1234"
	manifestRequest.Header.Set("X-Forwarded-Prefix", "/countries")
	manifest := httptest.NewRecorder()
	s.router.ServeHTTP(manifest, manifestRequest)
	if !strings.Contains(manifest.Body.String(), `"start_url":"/countries/"`) {
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
	recorder := doRequest(s, http.MethodGet, "/?lang=fr", nil)
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
	for _, required := range []string{`lang="fr"`, `dir="ltr"`, `class="theme-dark"`, `href="/countries/static/css/main.css?v=test-version-abc123"`, `id="main-content"`} {
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
		"APP_BASE + '/static/css/main.css?v=' + CACHE_VERSION",
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
		"response.headers.get('Cache-Control') || '').includes('no-store')",
		"self.navigator.language",
		`"ar":{"message":"تعذر تحميل هذه الصفحة. تحقق من اتصالك وحاول مرة أخرى.","title":"أنت غير متصل بالإنترنت"}`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("service worker omitted %q", required)
		}
	}
	if strings.Contains(body, "APP_BASE + '/'") || strings.Contains(body, "skipWaiting();\n});\n\nself.addEventListener('activate'") {
		t.Errorf("service worker precaches HTML or activates updates without approval: %s", body)
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
	if got := s.BuildURL(request, "/server/about"); got != "http://localhost/server/about" {
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
		`/static/js/swagger-ui.js`,
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
		"/static/js/swagger-ui.js",
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
	if mainSkip < 0 || navigationSkip <= mainSkip || announcement <= navigationSkip || dismiss <= navigationSkip || header <= dismiss || main <= announcement {
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
		})
	}
}

func TestThemeCSSUsesThemeClasses(t *testing.T) {
	s := newTestServer(t)
	response := doRequest(s, http.MethodGet, "/static/css/main.css", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET main.css returned %d, want 200", response.Code)
	}
	for _, required := range []string{"html.theme-dark, html.theme-light, html.theme-auto {", "html.theme-light {", "html.theme-auto {", "html.theme-light .header {", ".btn-danger {", ".btn-danger:hover {"} {
		if !strings.Contains(response.Body.String(), required) {
			t.Errorf("theme stylesheet omitted %q", required)
		}
	}
	if strings.Contains(response.Body.String(), "[data-theme=") {
		t.Error("theme stylesheet still uses data-theme selectors")
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
	response := httptest.NewRecorder()
	s.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `&#34;cca2&#34;:&#34;TL&#34;`) || !strings.Contains(response.Body.String(), `method="post"`) {
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
