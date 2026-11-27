package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/countries"
)

func TestBuildTrustedProxiesIncludesListenAddressSubnets(t *testing.T) {
	for _, test := range []struct {
		listen  string
		trusted string
		outside string
	}{
		{listen: "192.0.2.10:8080", trusted: "192.0.2.250", outside: "192.0.3.1"},
		{listen: "[2001:db8:1:2::10]:8080", trusted: "2001:db8:1:2::ffff", outside: "2001:db8:1:3::1"},
	} {
		t.Run(test.listen, func(t *testing.T) {
			networks := buildTrustedProxies(test.listen, nil)
			contains := func(value string) bool {
				ip := net.ParseIP(value)
				for _, network := range networks {
					if network.Contains(ip) {
						return true
					}
				}
				return false
			}
			if !contains(test.trusted) || contains(test.outside) {
				t.Fatalf("derived trusted network did not match %s and exclude %s", test.trusted, test.outside)
			}
		})
	}
}

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
