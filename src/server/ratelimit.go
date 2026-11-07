package server

import (
	"context"
	"database/sql"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
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
	return l
}

func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.config.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		client := requestClientIP(r.RemoteAddr)
		class, rule := l.classify(r)
		allowed, retryAfter, err := l.allow(r.Context(), client, class, rule)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"ok": false, "error": "RATE_LIMIT_UNAVAILABLE", "message": "Request limits are temporarily unavailable",
			})
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", retryAfter)
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"ok": false, "error": "RATE_LIMITED", "message": "Too many requests",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
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
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		remoteAddr = host
	}
	if ip := net.ParseIP(remoteAddr); ip != nil {
		return ip.String()
	}
	return "unknown"
}

func retrySeconds(first time.Time, window time.Duration, now time.Time) string {
	remaining := first.Add(window).Sub(now)
	seconds := int(math.Ceil(remaining.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
