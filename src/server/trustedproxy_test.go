package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
)

func TestTrustedProxyMiddlewareRejectsSpoofedPublicPeer(t *testing.T) {
	svc, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := New(svc, config.DefaultConfig(), "0.0.0.0", "8080", "test", "", "")
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "203.0.113.9:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.4")
	var got string
	s.trustedProxyMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr })).ServeHTTP(httptest.NewRecorder(), request)
	if got != "203.0.113.9:4321" {
		t.Fatalf("untrusted peer was rewritten to %q", got)
	}
}

func TestTrustedProxyMiddlewareAcceptsTrustedPeerAndPreservesOriginal(t *testing.T) {
	svc, err := countries.NewService([]byte(fixtureJSON))
	if err != nil {
		t.Fatal(err)
	}
	s := New(svc, config.DefaultConfig(), "0.0.0.0", "8080", "test", "", "")
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.RemoteAddr = "127.0.0.1:4321"
	request.Header.Set("X-Forwarded-For", "198.51.100.4")
	var got string
	s.trustedProxyMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
		if peer, ok := r.Context().Value(originalPeerKey{}).(string); !ok || peer != "127.0.0.1:4321" {
			t.Errorf("preserved original peer = %q, %t", peer, ok)
		}
	})).ServeHTTP(httptest.NewRecorder(), request)
	if got != "198.51.100.4" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}
}
