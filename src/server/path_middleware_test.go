package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestURLNormalizeMiddlewareRedirectsCanonicalPaths(t *testing.T) {
	handler := URLNormalizeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		method, path string
		status       int
		location     string
	}{
		{method: http.MethodGet, path: "/", status: http.StatusNoContent},
		{method: http.MethodGet, path: "/server/about/", status: http.StatusMovedPermanently, location: "/server/about"},
		{method: http.MethodGet, path: "/api/v1/countries/?page=2", status: http.StatusMovedPermanently, location: "/api/v1/countries?page=2"},
		{method: http.MethodPost, path: "/server/contact/", status: http.StatusPermanentRedirect, location: "/server/contact"},
		{method: http.MethodGet, path: "/static//css/site.css", status: http.StatusMovedPermanently, location: "/static/css/site.css"},
		{method: http.MethodGet, path: "/dir/index.html/", status: http.StatusNoContent},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Location") != test.location {
			t.Errorf("%s %s => status %d location %q, want %d %q", test.method, test.path, response.Code, response.Header().Get("Location"), test.status, test.location)
		}
	}
}
