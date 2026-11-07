package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLanguageForRequestUsesConfiguredFallbackOrder(t *testing.T) {
	s := newTestServer(t)

	query := httptest.NewRequest("GET", "/?lang=fr", nil)
	queryRecorder := httptest.NewRecorder()
	if got := s.languageForRequest(queryRecorder, query); got != "fr" {
		t.Fatalf("query language = %q, want fr", got)
	}
	cookies := queryRecorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "lang" || cookies[0].Value != "fr" {
		t.Fatalf("language cookies = %+v, want lang=fr", cookies)
	}

	invalid := httptest.NewRequest("GET", "/?lang=xx", nil)
	invalid.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
	invalid.Header.Set("Accept-Language", "es")
	if got := s.languageForRequest(httptest.NewRecorder(), invalid); got != "en" {
		t.Fatalf("unsupported query language = %q, want en", got)
	}

	accepted := httptest.NewRequest("GET", "/", nil)
	accepted.Header.Set("Accept-Language", "fr;q=0.5, es;q=0.9, en;q=0.1")
	if got := s.languageForRequest(httptest.NewRecorder(), accepted); got != "es" {
		t.Fatalf("Accept-Language result = %q, want es", got)
	}

	cookieRequest := httptest.NewRequest("GET", "/", nil)
	cookieRequest.AddCookie(&http.Cookie{Name: "lang", Value: "de"})
	cookieRequest.Header.Set("Accept-Language", "es")
	if got := s.languageForRequest(httptest.NewRecorder(), cookieRequest); got != "de" {
		t.Fatalf("cookie language = %q, want de", got)
	}
}
