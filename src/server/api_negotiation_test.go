package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/countries/src/common/api"
)

func TestAPIContentNegotiationTextSerializer(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		path      string
		accept    string
		userAgent string
		status    int
		content   string
		body      string
		wantType  string
		wantBody  string
		wantPath  string
	}{
		{
			name:   "plain accept serializes objects and nested values",
			method: http.MethodGet, path: "/api/v1/items", accept: "text/plain", status: http.StatusOK,
			content: "application/json; charset=utf-8", body: `{"ok":true,"data":{"z":"last","stats":{"total":4},"tags":["one","two"]}}`,
			wantType: "text/plain; charset=utf-8", wantBody: "OK: Retrieved successfully\nstats.total: 4\ntags: one, two\nz: last\n", wantPath: "/api/v1/items",
		},
		{
			name:   "txt suffix selects text and routes without suffix",
			method: http.MethodGet, path: "/api/v1/items.txt", accept: "application/json", status: http.StatusOK,
			content: "application/json", body: `{"ok":true,"data":{"count":249}}`,
			wantType: "text/plain; charset=utf-8", wantBody: "OK: Retrieved successfully\ncount: 249\n", wantPath: "/api/v1/items",
		},
		{
			name:   "primary value is bare",
			method: http.MethodGet, path: "/api/v1/count", accept: "text/plain", status: http.StatusOK,
			content: "application/json", body: `{"ok":true,"data":{"count":249}}`,
			wantType: "text/plain; charset=utf-8", wantBody: "249\n", wantPath: "/api/v1/count",
		},
		{
			name:   "health uses flat text without success prefix",
			method: http.MethodGet, path: "/api/v1/server/healthz", accept: "text/plain", status: http.StatusOK,
			content: "application/json", body: `{"status":"healthy","checks":{"database":"ok"}}`,
			wantType: "text/plain; charset=utf-8", wantBody: "checks.database: ok\nstatus: healthy\n", wantPath: "/api/v1/server/healthz",
		},
		{
			name:   "errors keep status and use text envelope",
			method: http.MethodGet, path: "/api/v1/missing", accept: "text/plain", status: http.StatusNotFound,
			content: "application/json", body: `{"ok":false,"error":"NOT_FOUND","message":"Missing item"}`,
			wantType: "text/plain; charset=utf-8", wantBody: "ERROR: NOT_FOUND: Missing item\n", wantPath: "/api/v1/missing",
		},
		{
			name:   "explicit json beats a command line user agent",
			method: http.MethodGet, path: "/api/v1/items", accept: "application/json", userAgent: "curl/8.0", status: http.StatusOK,
			content: "application/json", body: `{"ok":true,"data":{"count":1}}`,
			wantType: "application/json", wantBody: `{"ok":true,"data":{"count":1}}`, wantPath: "/api/v1/items",
		},
		{
			name:   "noninteractive client defaults to text",
			method: http.MethodGet, path: "/api/v1/items", userAgent: "Go-http-client/1.1", status: http.StatusOK,
			content: "application/json", body: `{"ok":true,"data":["one","two"]}`,
			wantType: "text/plain; charset=utf-8", wantBody: "OK: 2 items found\none\ntwo\n", wantPath: "/api/v1/items",
		},
		{
			name:   "browser user agent defaults to json",
			method: http.MethodGet, path: "/api/v1/items", userAgent: "Mozilla/5.0", status: http.StatusOK,
			content: "application/json", body: `{"ok":true,"data":{"count":1}}`,
			wantType: "application/json", wantBody: `{"ok":true,"data":{"count":1}}`, wantPath: "/api/v1/items",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.wantPath {
					t.Errorf("handler path = %q, want %q", r.URL.Path, test.wantPath)
				}
				w.Header().Set("Content-Type", test.content)
				w.Header().Set("X-Request-ID", "negotiation-test")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			request := httptest.NewRequest(test.method, test.path, nil)
			if test.accept != "" {
				request.Header.Set("Accept", test.accept)
			}
			if test.userAgent != "" {
				request.Header.Set("User-Agent", test.userAgent)
			}
			response := httptest.NewRecorder()
			apiContentNegotiation(next).ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if got := response.Header().Get("Content-Type"); got != test.wantType {
				t.Fatalf("Content-Type = %q, want %q", got, test.wantType)
			}
			if got := response.Body.String(); got != test.wantBody {
				t.Fatalf("body = %q, want %q", got, test.wantBody)
			}
			if got := response.Header().Get("X-Request-ID"); got != "negotiation-test" {
				t.Fatalf("X-Request-ID = %q, want preserved header", got)
			}
		})
	}
}

func TestAPIContentNegotiationLeavesNonJSONTextResponseAlone(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("status: healthy\n"))
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz.txt", nil)
	response := httptest.NewRecorder()
	apiContentNegotiation(next).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") || response.Body.String() != "status: healthy\n" {
		t.Fatalf("response status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestExplicitTextAPIRoutesPreserveTheirRegisteredPath(t *testing.T) {
	base := api.APIBasePath()
	for _, path := range []string{base + "/stats.txt", base + "/count.txt", base + "/server/healthz.txt"} {
		if !isExplicitTextAPIRoute(path) {
			t.Errorf("%s was not recognized as an explicit text route", path)
		}
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				t.Errorf("registered handler path = %q, want %q", r.URL.Path, path)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("result\n"))
		})
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		apiContentNegotiation(next).ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != "result\n" {
			t.Errorf("GET %s returned status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestMetricsAPIRoutesPreserveProtocolSuccessBodies(t *testing.T) {
	cases := []struct {
		path        string
		contentType string
		body        string
	}{
		{path: api.APIPath("server/metrics/prometheus"), contentType: "text/plain; version=0.0.4", body: "countries_up 1\n"},
		{path: api.APIPath("server/metrics/grafana"), contentType: "application/json; charset=utf-8", body: `{"title":"Countries"}`},
		{path: "/api/metrics/loki", contentType: "application/json; charset=utf-8", body: `{"streams":[]}`},
	}
	for _, test := range cases {
		t.Run(test.path, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				_, _ = w.Write([]byte(test.body))
			})
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Accept", "text/plain")
			response := httptest.NewRecorder()
			apiContentNegotiation(next).ServeHTTP(response, request)
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}
			if got := response.Body.String(); got != test.body {
				t.Errorf("body = %q, want %q", got, test.body)
			}
		})
	}
}

func TestAPIContentNegotiationSetsSafeCacheHeaders(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		path   string
		status int
		cache  string
	}{
		{name: "public country data", method: http.MethodGet, path: api.APIPath("countries/US"), status: http.StatusOK, cache: "public, max-age=60"},
		{name: "private server page", method: http.MethodGet, path: api.APIPath("server/healthz"), status: http.StatusOK, cache: "private, no-store"},
		{name: "public route error", method: http.MethodGet, path: api.APIPath("countries/unknown"), status: http.StatusNotFound, cache: "no-store"},
		{name: "public route write", method: http.MethodPost, path: api.APIPath("coordinates"), status: http.StatusOK, cache: "private, no-store"},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set("Accept", "application/json")
			response := httptest.NewRecorder()
			apiContentNegotiation(next).ServeHTTP(response, request)
			if got := response.Header().Get("Cache-Control"); got != test.cache {
				t.Errorf("Cache-Control = %q, want %q", got, test.cache)
			}
			if strings.HasPrefix(test.cache, "public") && !strings.Contains(response.Header().Get("Vary"), "Cookie") {
				t.Errorf("public response Vary = %q, want Cookie", response.Header().Get("Vary"))
			}
		})
	}
}
