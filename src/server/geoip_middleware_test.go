package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/geoip"
)

type fixedCountryLookup struct {
	record geoip.CountryRecord
	found  bool
	err    error
}

func (f fixedCountryLookup) LookupCountry(net.IP) (geoip.CountryRecord, bool, error) {
	return f.record, f.found, f.err
}

func TestGeoIPMiddlewareCountryPoliciesAndFailOpen(t *testing.T) {
	for _, test := range []struct {
		name       string
		country    string
		allow      []string
		deny       []string
		found      bool
		lookupErr  error
		wantStatus int
	}{
		{name: "deny matching country", country: "CN", deny: []string{"CN"}, found: true, wantStatus: http.StatusForbidden},
		{name: "allow list takes precedence", country: "CN", allow: []string{"US"}, deny: []string{"CN"}, found: true, wantStatus: http.StatusForbidden},
		{name: "allow listed country", country: "US", allow: []string{"US"}, found: true, wantStatus: http.StatusNoContent},
		{name: "missing lookup fails open", country: "CN", deny: []string{"CN"}, wantStatus: http.StatusNoContent},
		{name: "lookup error fails open", lookupErr: geoip.ErrDatabaseUnavailable, wantStatus: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Server.GeoIP.DenyCountries = test.deny
			cfg.Server.GeoIP.AllowCountries = test.allow
			server := &Server{config: cfg, geoIPManager: fixedCountryLookup{record: geoip.CountryRecord{CountryCode: test.country}, found: test.found, err: test.lookupErr}}
			handler := server.geoIPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = "198.51.100.4:1234"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestAllowlistMiddlewareMarksTrustedClientAndRateLimitBypasses(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Security.Allowlist = []config.AllowlistEntry{{CIDR: "198.51.100.0/24", Description: "test"}}
	server := &Server{config: cfg}
	server.allowlist = server.buildAllowlist()
	called := false
	now := time.Now()
	limiter := &rateLimiter{
		config: config.RateLimitConfig{Read: config.RateLimitRuleConfig{Requests: 1, Window: 60}, GlobalBurst: 1},
		windows: map[rateLimitKey]rateLimitWindow{
			{client: "198.51.100.4", class: "read"}:   {events: []time.Time{now}},
			{client: "198.51.100.4", class: "global"}: {events: []time.Time{now}},
		},
		now: func() time.Time { return now },
	}
	handler := server.allowlistMiddleware(limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = isAllowlisted(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.4:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("response/called = %d/%t, want 204/true", response.Code, called)
	}
	if isAllowlisted(context.Background()) {
		t.Fatal("an unrelated context must not be allowlisted")
	}
}
