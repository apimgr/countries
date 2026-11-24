package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	servermetrics "github.com/apimgr/countries/src/server/metrics"
)

type rateLimitKey struct {
	client string
	class  string
}

type rateLimitWindow struct {
	events []time.Time
}

type rateLimiter struct {
	mu           sync.Mutex
	windows      map[rateLimitKey]rateLimitWindow
	lastPrunedAt time.Time
	config       config.RateLimitConfig
	db           *sql.DB
	now          func() time.Time
	metrics      *servermetrics.Metrics
	clientKey    func(*http.Request) string
	authRequired func(*http.Request) bool
	authValid    func(*http.Request) bool
}

func newRateLimiter(cfg config.RateLimitConfig, db ...*sql.DB) *rateLimiter {
	l := &rateLimiter{
		windows: make(map[rateLimitKey]rateLimitWindow),
		config:  cfg,
		now:     time.Now,
	}
	if len(db) > 0 {
		l.db = db[0]
	}
	l.clientKey = func(r *http.Request) string { return requestClientIP(r.RemoteAddr) }
	return l
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAllowlisted(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		ip := requestClientIP(r.RemoteAddr)
		if l.authRequired != nil && l.authRequired(r) {
			if blocked, retry, err := l.authFailureBlocked(r.Context(), ip); err != nil {
				writeRateLimitUnavailable(w)
				return
			} else if blocked {
				writeRateLimited(w, retry)
				return
			}
			if l.authValid != nil && !l.authValid(r) {
				allowed, retry, err := l.recordAuthFailure(r.Context(), ip)
				if err != nil {
					writeRateLimitUnavailable(w)
					return
				}
				if !allowed {
					writeRateLimited(w, retry)
					return
				}
			}
		} else if blocked, retry, err := l.authFailureBlocked(r.Context(), ip); err != nil {
			writeRateLimitUnavailable(w)
			return
		} else if blocked {
			writeRateLimited(w, retry)
			return
		}
		client := l.clientKey(r)
		class, rule := l.classify(r)
		allowed, retryAfter, err := l.allow(r.Context(), client, class, rule)
		if err != nil {
			writeRateLimitUnavailable(w)
			return
		}
		if !allowed {
			if l.metrics != nil {
				l.metrics.RecordRateLimit(rateLimitMetricName(class), true)
			}
			writeRateLimited(w, retryAfter)
			return
		}
		if l.metrics != nil {
			l.metrics.RecordRateLimit(rateLimitMetricName(class), false)
		}
		next.ServeHTTP(w, r)
	})
}

func writeRateLimitUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
		"ok": false, "error": "RATE_LIMIT_UNAVAILABLE", "message": "Request limits are temporarily unavailable",
	})
}

func writeRateLimited(w http.ResponseWriter, retry string) {
	w.Header().Set("Retry-After", retry)
	writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
		"ok": false, "error": "RATE_LIMITED", "message": "Too many requests",
	})
}

func (l *rateLimiter) authFailureBlocked(ctx context.Context, ip string) (bool, string, error) {
	rule := l.config.AuthFailures
	if rule.Requests <= 0 || rule.Window <= 0 {
		return false, "", nil
	}
	window := time.Duration(rule.Window) * time.Second
	if l.db != nil {
		blocked, retry, err := database.CheckRateLimit(ctx, l.db, ip, "auth_failures", l.now(), rule.Requests, window)
		return blocked, strconv.Itoa(maxInt(1, int(math.Ceil(retry.Seconds())))), err
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := rateLimitKey{client: ip, class: "auth_failures"}
	current := l.withinWindow(key, now, window)
	l.windows[key] = current
	if len(current.events) < rule.Requests {
		return false, "", nil
	}
	return true, retrySeconds(current.events[0], window, now), nil
}

func (l *rateLimiter) recordAuthFailure(ctx context.Context, ip string) (bool, string, error) {
	rule := l.config.AuthFailures
	if rule.Requests <= 0 || rule.Window <= 0 {
		return true, "", nil
	}
	window := time.Duration(rule.Window) * time.Second
	if l.db != nil {
		allowed, retry, err := database.RecordRateLimitClass(ctx, l.db, ip, "auth_failures", l.now(), rule.Requests, window)
		return allowed, strconv.Itoa(maxInt(1, int(math.Ceil(retry.Seconds())))), err
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := rateLimitKey{client: ip, class: "auth_failures"}
	current := l.withinWindow(key, now, window)
	if len(current.events) >= rule.Requests {
		l.windows[key] = current
		return false, retrySeconds(current.events[0], window, now), nil
	}
	current.events = append(current.events, now)
	l.windows[key] = current
	if len(current.events) >= rule.Requests {
		return false, retrySeconds(current.events[0], window, now), nil
	}
	return true, "", nil
}

func (s *Server) rateLimitClientKey(r *http.Request) string {
	remoteIP := requestClientIP(r.RemoteAddr)
	raw := bearerValue(r)
	if raw == "" {
		return remoteIP
	}
	valid := s.validOperatorToken(raw)
	if !valid && s.db != nil {
		_, err := database.LookupOwnerToken(r.Context(), s.db, raw, time.Now())
		valid = err == nil
	}
	if !valid {
		return remoteIP
	}
	hash := sha256.Sum256([]byte(raw))
	return "token:" + hex.EncodeToString(hash[:])
}

func (s *Server) rateLimitAuthRequired(r *http.Request) bool {
	path := r.URL.Path
	if path == "/mcp" || path == "/api/mcp" || path == api.APIBasePath()+"/server/mcp" {
		return true
	}
	base := api.APIBasePath() + "/server/preferences/tokens"
	if strings.HasPrefix(path, base) {
		return path != base+"/primary"
	}
	switch path {
	case "/server/preferences/token/reveal", "/server/preferences/token/rotate", "/server/preferences/tokens":
		return true
	}
	return strings.HasPrefix(path, "/server/preferences/tokens/")
}

func (s *Server) rateLimitAuthValid(r *http.Request) bool {
	path := r.URL.Path
	if path == "/mcp" || path == "/api/mcp" || path == api.APIBasePath()+"/server/mcp" {
		token := bearerValue(r)
		if s.validOperatorToken(token) {
			return true
		}
		if token == "" || s.db == nil {
			return false
		}
		_, err := database.LookupOwnerToken(r.Context(), s.db, token, time.Now())
		return err == nil
	}
	if strings.HasPrefix(path, "/server/preferences/") {
		cookie, err := r.Cookie(ownerTokenCookieName)
		if err != nil || s.db == nil {
			return false
		}
		identity, err := database.LookupOwnerToken(r.Context(), s.db, cookie.Value, time.Now())
		return err == nil && identity.Kind == "primary"
	}
	token := bearerValue(r)
	if token == "" || s.db == nil {
		return false
	}
	_, err := database.LookupOwnerToken(r.Context(), s.db, token, time.Now())
	return err == nil
}

func rateLimitMetricName(class string) string {
	switch class {
	case "read", "write", "health":
		return "per_endpoint"
	default:
		return "global"
	}
}

func (l *rateLimiter) classify(r *http.Request) (string, config.RateLimitRuleConfig) {
	if strings.HasSuffix(r.URL.Path, "/server/healthz") || r.URL.Path == "/healthz" {
		return "health", l.config.Health
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return "read", l.config.Read
	}
	return "write", l.config.Write
}

func (l *rateLimiter) allow(ctx context.Context, client, class string, rule config.RateLimitRuleConfig) (bool, string, error) {
	now := l.now()
	window := time.Duration(rule.Window) * time.Second
	if window <= 0 || rule.Requests <= 0 || l.config.GlobalBurst <= 0 {
		return true, "", nil
	}
	if l.db != nil {
		allowed, retry, err := database.AllowRateLimit(ctx, l.db, client, class, now, rule.Requests, window, l.config.GlobalBurst)
		return allowed, strconv.Itoa(maxInt(1, int(math.Ceil(retry.Seconds())))), err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastPrunedAt.IsZero() || now.Sub(l.lastPrunedAt) >= time.Minute {
		l.prune(now)
		l.lastPrunedAt = now
	}
	classKey := rateLimitKey{client: client, class: class}
	globalKey := rateLimitKey{client: client, class: "global"}
	classWindow := l.withinWindow(classKey, now, window)
	globalWindow := l.withinWindow(globalKey, now, time.Minute)
	if len(classWindow.events) >= rule.Requests {
		return false, retrySeconds(classWindow.events[0], window, now), nil
	}
	if len(globalWindow.events) >= l.config.GlobalBurst {
		return false, retrySeconds(globalWindow.events[0], time.Minute, now), nil
	}
	classWindow.events = append(classWindow.events, now)
	globalWindow.events = append(globalWindow.events, now)
	l.windows[classKey] = classWindow
	l.windows[globalKey] = globalWindow
	return true, "", nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (l *rateLimiter) withinWindow(key rateLimitKey, now time.Time, window time.Duration) rateLimitWindow {
	current := l.windows[key]
	firstValid := 0
	for firstValid < len(current.events) && now.Sub(current.events[firstValid]) >= window {
		firstValid++
	}
	current.events = current.events[firstValid:]
	return current
}

func (l *rateLimiter) prune(now time.Time) {
	for key, current := range l.windows {
		window := time.Minute
		if key.class == "read" && l.config.Read.Window > 0 {
			window = time.Duration(l.config.Read.Window) * time.Second
		}
		if key.class == "write" && l.config.Write.Window > 0 {
			window = time.Duration(l.config.Write.Window) * time.Second
		}
		if key.class == "health" && l.config.Health.Window > 0 {
			window = time.Duration(l.config.Health.Window) * time.Second
		}
		current = l.withinWindow(key, now, window)
		if len(current.events) == 0 {
			delete(l.windows, key)
		} else {
			l.windows[key] = current
		}
	}
}

func requestClientIP(remoteAddr string) string {
	if remoteAddr == "tor" || strings.HasPrefix(remoteAddr, "tor:") {
		return remoteAddr
	}
	if identity := torCircuitIdentity(remoteAddr); identity != "" {
		return identity
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		remoteAddr = host
	}
	if ip := net.ParseIP(remoteAddr); ip != nil {
		return ip.String()
	}
	return "unknown"
}

func torCircuitIdentity(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		remoteAddr = host
	}
	ip := net.ParseIP(remoteAddr).To16()
	if ip == nil || ip[0] != 0xfc {
		return ""
	}
	return "tor:" + hex.EncodeToString(ip)
}

func retrySeconds(first time.Time, window time.Duration, now time.Time) string {
	remaining := first.Add(window).Sub(now)
	seconds := int(math.Ceil(remaining.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
