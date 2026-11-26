package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrontendResponseFormatPriority(t *testing.T) {
	for _, test := range []struct {
		name, accept, userAgent, want string
	}{
		{name: "JSON explicit preference", accept: "application/json, text/html", userAgent: "curl/8", want: "json"},
		{name: "HTML explicit preference", accept: "text/html, text/plain", userAgent: "curl/8", want: "html"},
		{name: "plain text", accept: "text/plain", userAgent: "Mozilla/5.0", want: "text"},
		{name: "own CLI", userAgent: "countries-cli/1.0", want: "json"},
		{name: "text browser", userAgent: "Lynx/2.9", want: "html"},
		{name: "HTTP tool", userAgent: "curl/8.0", want: "text"},
		{name: "empty agent", want: "text"},
		{name: "unknown browser", userAgent: "Mozilla/5.0", want: "html"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept", test.accept)
			request.Header.Set("User-Agent", test.userAgent)
			if got := frontendResponseFormat(request); got != test.want {
				t.Errorf("frontendResponseFormat() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFrontendNegotiationUsesAPIForJSONAndText(t *testing.T) {
	server := newTestServer(t)
	for _, test := range []struct {
		name, accept, userAgent, contentType, contains string
	}{
		{name: "explicit JSON", accept: "application/json", contentType: "application/json", contains: `"ok": true`},
		{name: "curl text", userAgent: "curl/8.0", contentType: "text/plain", contains: "Testland"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Accept", test.accept)
			request.Header.Set("User-Agent", test.userAgent)
			response := httptest.NewRecorder()
			server.router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), test.contentType) || !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("response status=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
			if !strings.HasSuffix(response.Body.String(), "\n") {
				t.Fatal("negotiated response must end with one newline")
			}
		})
	}
}
