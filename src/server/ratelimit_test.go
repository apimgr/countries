package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
)

func TestRateLimiterAppliesSlidingClassAndGlobalLimits(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(config.RateLimitConfig{
		Enabled:     true,
		Read:        config.RateLimitRuleConfig{Requests: 2, Window: 10},
		Write:       config.RateLimitRuleConfig{Requests: 1, Window: 10},
		Health:      config.RateLimitRuleConfig{Requests: 2, Window: 10},
		GlobalBurst: 3,
	})
	limiter.now = func() time.Time { return now }

	for _, class := range []string{"read", "read", "write"} {
		if allowed, _, err := limiter.allow(context.Background(), "192.0.2.1", class, config.RateLimitRuleConfig{Requests: 2, Window: 10}); err != nil || !allowed {
			t.Fatalf("request class %q was rejected before its limit", class)
		}
	}
	if allowed, retry, err := limiter.allow(context.Background(), "192.0.2.1", "read", config.RateLimitRuleConfig{Requests: 2, Window: 10}); err != nil || allowed || retry != "10" {
		t.Fatalf("class limit result = (%v, %q), want (false, 10)", allowed, retry)
	}
	if allowed, retry, err := limiter.allow(context.Background(), "192.0.2.2", "read", config.RateLimitRuleConfig{Requests: 2, Window: 10}); err != nil || !allowed || retry != "" {
		t.Fatalf("separate client result = (%v, %q), want (true, empty)", allowed, retry)
	}

	now = now.Add(time.Minute)
	if allowed, retry, err := limiter.allow(context.Background(), "192.0.2.1", "read", config.RateLimitRuleConfig{Requests: 2, Window: 10}); err != nil || !allowed || retry != "" {
		t.Fatalf("expired sliding window result = (%v, %q), want (true, empty)", allowed, retry)
	}
}

func TestRateLimitMiddlewareReturnsCanonical429(t *testing.T) {
	limiter := newRateLimiter(config.RateLimitConfig{
		Enabled:     true,
		Read:        config.RateLimitRuleConfig{Requests: 1, Window: 60},
		Write:       config.RateLimitRuleConfig{Requests: 1, Window: 60},
		Health:      config.RateLimitRuleConfig{Requests: 1, Window: 60},
		GlobalBurst: 10,
	})
	limiter.now = func() time.Time { return time.Unix(100, 0) }
	next := limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/countries", nil)
	request.RemoteAddr = "192.0.2.8:1234"
	next.ServeHTTP(httptest.NewRecorder(), request)
	recorder := httptest.NewRecorder()
	next.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if recorder.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", recorder.Header().Get("Retry-After"))
	}
	if !strings.Contains(recorder.Body.String(), `"error": "RATE_LIMITED"`) || strings.Contains(recorder.Body.String(), "retry_after") || !strings.HasSuffix(recorder.Body.String(), "\n") {
		t.Fatalf("response body does not match canonical rate-limit error: %s", recorder.Body.String())
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	limiter := newRateLimiter(config.RateLimitConfig{Enabled: false})
	called := false
	recorder := httptest.NewRecorder()
	limiter.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatal("disabled limiter did not pass the request through")
	}
}
