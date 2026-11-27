package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	servermetrics "github.com/apimgr/countries/src/server/metrics"
	"github.com/redis/go-redis/v9"
)

type rateLimitKey struct {
	client string
	class  string
}

var rateLimitEventSequence atomic.Uint64

type rateLimitWindow struct {
	events []time.Time
}

type pendingRateEvent struct {
	key       string
	timestamp int64
}

type rateLimiter struct {
	mu            sync.Mutex
	windows       map[rateLimitKey]rateLimitWindow
	lastPrunedAt  time.Time
	config        config.RateLimitConfig
	db            *sql.DB
	now           func() time.Time
	metrics       *servermetrics.Metrics
	cache         redis.UniversalClient
	cachePrefix   string
	cacheFailure  func()
	cacheRecovery func()
	seeded        map[rateLimitKey]bool
	pending       []pendingRateEvent
	pendingMu     sync.Mutex
	flushStop     chan struct{}
	flushDone     chan struct{}
	closeOnce     sync.Once
	clientKey     func(*http.Request) string
	authRequired  func(*http.Request) bool
	authValid     func(*http.Request) bool
}

func newRateLimiter(cfg config.RateLimitConfig, db ...*sql.DB) *rateLimiter {
	l := &rateLimiter{
		windows: make(map[rateLimitKey]rateLimitWindow),
		config:  cfg,
		now:     time.Now,
		seeded:  make(map[rateLimitKey]bool),
	}
	if len(db) > 0 {
		l.db = db[0]
	}
	l.clientKey = func(r *http.Request) string { return requestClientIP(r.RemoteAddr) }
	if l.db != nil {
		l.flushStop = make(chan struct{})
		l.flushDone = make(chan struct{})
		l.seedPersistedRateLimits()
		go l.runRateLimitFlush()
	}
	return l
}

func (l *rateLimiter) seedPersistedRateLimits() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := l.db.QueryContext(ctx, `SELECT key, window_start FROM rate_limits WHERE window_start >= ?`, l.now().Add(-15*time.Minute).Unix())
	if err != nil {
		log.Printf("rate-limit startup seed failed")
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("rate-limit startup seed rows close failed")
		}
	}()
	classes := []string{"read", "write", "health", "strict", "auth_failures", "reports", "reports_burst", "global"}
	for rows.Next() {
		var storedKey string
		var timestamp int64
		if err := rows.Scan(&storedKey, &timestamp); err != nil {
			continue
		}
		if !strings.HasPrefix(storedKey, "ip:") {
			continue
		}
		for _, class := range classes {
			suffix := ":" + class + ":"
			index := strings.LastIndex(storedKey, suffix)
			if index < 3 {
				continue
			}
			client := strings.TrimPrefix(storedKey[:index], "ip:")
			key := rateLimitKey{client: client, class: class}
			l.mu.Lock()
			window := l.windows[key]
			window.events = append(window.events, time.Unix(timestamp, 0))
			l.windows[key] = window
			l.seeded[key] = true
			l.mu.Unlock()
			break
		}
	}
}

func (l *rateLimiter) runRateLimitFlush() {
	defer close(l.flushDone)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := l.flushPersistedEvents(context.Background()); err != nil {
				log.Printf("rate-limit counter flush failed")
			}
		case <-l.flushStop:
			return
		}
	}
}

func (l *rateLimiter) queuePersistedEvent(client, class string, now time.Time) {
	if l.db == nil {
		return
	}
	key := fmt.Sprintf("ip:%s:%s:%d-%d", client, class, now.UnixNano(), rateLimitEventSequence.Add(1))
	l.pendingMu.Lock()
	l.pending = append(l.pending, pendingRateEvent{key: key, timestamp: now.Unix()})
	l.pendingMu.Unlock()
}

func (l *rateLimiter) flushPersistedEvents(ctx context.Context) error {
	if l.db == nil {
		return nil
	}
	l.pendingMu.Lock()
	pending := append([]pendingRateEvent(nil), l.pending...)
	l.pending = nil
	l.pendingMu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	err := database.Write(ctx, l.db, func(tx *sql.Tx) error {
		for _, event := range pending {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO rate_limits (key, count, window_start, updated_at) VALUES (?, 1, ?, ?)`, event.key, event.timestamp, event.timestamp); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		l.pendingMu.Lock()
		l.pending = append(pending, l.pending...)
		l.pendingMu.Unlock()
	}
	return err
}

func (l *rateLimiter) Close() error {
	if l.db == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		close(l.flushStop)
		<-l.flushDone
	})
	return l.flushPersistedEvents(context.Background())
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
	if l.cache != nil {
		count, first, err := l.redisCount(ctx, ip, "auth_failures", l.now().Add(-window))
		if err == nil {
			if l.cacheRecovery != nil {
				l.cacheRecovery()
			}
			if count >= rule.Requests {
				return true, retrySeconds(first, window, l.now()), nil
			}
			return false, "", nil
		}
		l.cacheUnavailable(ip, "auth_failures")
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
	if l.cache != nil {
		allowed, retry, err := l.redisRecord(ctx, ip, "auth_failures", l.now(), rule.Requests, window)
		if err == nil {
			if l.cacheRecovery != nil {
				l.cacheRecovery()
			}
			return allowed, retry, nil
		}
		l.cacheUnavailable(ip, "auth_failures")
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
	l.queuePersistedEvent(ip, "auth_failures", now)
	if len(current.events) >= rule.Requests {
		return false, retrySeconds(current.events[0], window, now), nil
	}
	return true, "", nil
}

func (l *rateLimiter) recordClass(ctx context.Context, client, class string, now time.Time, requests int, window time.Duration) (bool, string, error) {
	if requests <= 0 || window <= 0 {
		return true, "", nil
	}
	if l.cache != nil {
		allowed, retry, err := l.redisRecord(ctx, client, class, now, requests, window)
		if err == nil {
			if l.cacheRecovery != nil {
				l.cacheRecovery()
			}
			return allowed, retry, nil
		}
		l.cacheUnavailable(client, class)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := rateLimitKey{client: client, class: class}
	current := l.withinWindow(key, now, window)
	if len(current.events) >= requests {
		l.windows[key] = current
		return false, retrySeconds(current.events[0], window, now), nil
	}
	current.events = append(current.events, now)
	l.windows[key] = current
	l.queuePersistedEvent(client, class, now)
	if len(current.events) >= requests {
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
		var err error
		if r.URL.Path == api.APIBasePath()+"/server/auth/check" {
			_, err = database.CheckOwnerToken(r.Context(), s.db, raw, time.Now())
		} else {
			_, err = database.LookupOwnerToken(r.Context(), s.db, raw, time.Now())
		}
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
	if path == api.APIBasePath()+"/server/auth/check" {
		return true
	}
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
	if path == api.APIBasePath()+"/server/auth/check" {
		token := bearerValue(r)
		if s.validOperatorToken(token) {
			return true
		}
		if token == "" || s.db == nil {
			return false
		}
		_, err := database.CheckOwnerToken(r.Context(), s.db, token, time.Now())
		return err == nil
	}
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
	if l.cache != nil {
		allowed, retry, err := l.redisAllow(ctx, client, class, now, rule.Requests, window, l.config.GlobalBurst)
		if err == nil {
			if l.cacheRecovery != nil {
				l.cacheRecovery()
			}
			return allowed, retry, nil
		}
		l.cacheUnavailable(client, class)
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
	l.queuePersistedEvent(client, class, now)
	l.queuePersistedEvent(client, "global", now)
	return true, "", nil
}

const redisSlidingWindowScript = `
local now = tonumber(ARGV[1])
local class_cutoff = tonumber(ARGV[2])
local global_cutoff = tonumber(ARGV[3])
local class_limit = tonumber(ARGV[4])
local global_limit = tonumber(ARGV[5])
local member = ARGV[6]
local class_window = tonumber(ARGV[7])
local global_window = tonumber(ARGV[8])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', class_cutoff)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', global_cutoff)
local class_count = redis.call('ZCARD', KEYS[1])
local global_count = redis.call('ZCARD', KEYS[2])
if class_count >= class_limit then
  local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
  return {0, math.max(1, tonumber(first[2]) + class_window - now)}
end
if global_count >= global_limit then
  local first = redis.call('ZRANGE', KEYS[2], 0, 0, 'WITHSCORES')
  return {0, math.max(1, tonumber(first[2]) + global_window - now)}
end
redis.call('ZADD', KEYS[1], now, member)
redis.call('ZADD', KEYS[2], now, member)
redis.call('PEXPIRE', KEYS[1], class_window * 2)
redis.call('PEXPIRE', KEYS[2], global_window * 2)
return {1, 0}
`

const redisClassWindowScript = `
local now = tonumber(ARGV[1])
local cutoff = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]
local window = tonumber(ARGV[5])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', cutoff)
local count = redis.call('ZCARD', KEYS[1])
if count >= limit then
  local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
  return {0, math.max(1, tonumber(first[2]) + window - now), count}
end
redis.call('ZADD', KEYS[1], now, member)
redis.call('PEXPIRE', KEYS[1], window * 2)
count = count + 1
if count >= limit then
  local first = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
  return {0, math.max(1, tonumber(first[2]) + window - now), count}
end
return {1, 0, count}
`

func (l *rateLimiter) redisAllow(ctx context.Context, client, class string, now time.Time, classLimit int, classWindow time.Duration, globalLimit int) (bool, string, error) {
	nowMS := now.UnixMilli()
	classWindowMS := classWindow.Milliseconds()
	globalWindowMS := int64(time.Minute / time.Millisecond)
	member := fmt.Sprintf("%d-%d", now.UnixNano(), rateLimitEventSequence.Add(1))
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := l.cache.Eval(ctx, redisSlidingWindowScript, []string{
		l.cacheKey(client, class), l.cacheKey(client, "global"),
	}, nowMS, nowMS-classWindowMS, nowMS-globalWindowMS, classLimit, globalLimit, member, classWindowMS, globalWindowMS).Result()
	if err != nil {
		return false, "", err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 2 {
		return false, "", fmt.Errorf("unexpected cache rate-limit response")
	}
	allowed := redisInt(values[0]) == 1
	retry := int64(math.Ceil(float64(redisInt(values[1])) / 1000))
	if retry < 1 {
		retry = 1
	}
	return allowed, strconv.FormatInt(retry, 10), nil
}

func (l *rateLimiter) redisRecord(ctx context.Context, client, class string, now time.Time, limit int, window time.Duration) (bool, string, error) {
	nowMS, windowMS := now.UnixMilli(), window.Milliseconds()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := l.cache.Eval(ctx, redisClassWindowScript, []string{l.cacheKey(client, class)}, nowMS, nowMS-windowMS, limit, fmt.Sprintf("%d-%d", now.UnixNano(), rateLimitEventSequence.Add(1)), windowMS).Result()
	if err != nil {
		return false, "", err
	}
	values, ok := result.([]interface{})
	if !ok || len(values) < 2 {
		return false, "", fmt.Errorf("unexpected cache rate-limit response")
	}
	retry := int64(math.Ceil(float64(redisInt(values[1])) / 1000))
	if retry < 1 {
		retry = 1
	}
	return redisInt(values[0]) == 1, strconv.FormatInt(retry, 10), nil
}

func (l *rateLimiter) redisCount(ctx context.Context, client, class string, cutoff time.Time) (int, time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	key := l.cacheKey(client, class)
	if err := l.cache.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff.UnixMilli(), 10)).Err(); err != nil {
		return 0, time.Time{}, err
	}
	count, err := l.cache.ZCard(ctx, key).Result()
	if err != nil || count == 0 {
		return int(count), time.Time{}, err
	}
	first, err := l.cache.ZRangeWithScores(ctx, key, 0, 0).Result()
	if err != nil || len(first) == 0 {
		return int(count), time.Time{}, err
	}
	return int(count), time.UnixMilli(int64(first[0].Score)), nil
}

func (l *rateLimiter) cacheKey(client, class string) string {
	prefix := strings.TrimSpace(l.cachePrefix)
	if prefix == "" {
		prefix = "countries:"
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	hash := sha256.Sum256([]byte(client))
	return prefix + "rate:{" + hex.EncodeToString(hash[:16]) + "}:" + class
}

func (l *rateLimiter) cacheUnavailable(client, class string) {
	if l.cacheFailure != nil {
		l.cacheFailure()
	}
	l.seedMemoryFromDatabase(client, class, l.now())
}

func (l *rateLimiter) seedMemoryFromDatabase(client, class string, now time.Time) {
	if l.db == nil {
		return
	}
	keys := []rateLimitKey{{client: client, class: class}, {client: client, class: "global"}}
	for _, key := range keys {
		l.mu.Lock()
		if l.seeded[key] {
			l.mu.Unlock()
			continue
		}
		l.seeded[key] = true
		l.mu.Unlock()
		window := time.Minute
		if key.class == class {
			switch key.class {
			case "auth_failures":
				window = time.Duration(l.config.AuthFailures.Window) * time.Second
			case "read":
				window = time.Duration(l.config.Read.Window) * time.Second
			case "write":
				window = time.Duration(l.config.Write.Window) * time.Second
			case "health":
				window = time.Duration(l.config.Health.Window) * time.Second
			case "strict":
				window = time.Duration(l.config.Strict.Window) * time.Second
			case "reports":
				window = time.Minute
			case "reports_burst":
				window = 10 * time.Second
			}
		}
		if window <= 0 {
			continue
		}
		prefix := "ip:" + key.client + ":" + key.class + ":"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		rows, err := l.db.QueryContext(ctx, `SELECT window_start FROM rate_limits WHERE key LIKE ? AND window_start >= ? ORDER BY window_start`, prefix+"%", now.Add(-window).Unix())
		if err == nil {
			for rows.Next() {
				var timestamp int64
				if scanErr := rows.Scan(&timestamp); scanErr == nil {
					l.mu.Lock()
					windowState := l.windows[key]
					windowState.events = append(windowState.events, time.Unix(timestamp, 0))
					l.windows[key] = windowState
					l.mu.Unlock()
				}
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				log.Printf("rate-limit fallback seed failed: category=%s", key.class)
			}
			if closeErr := rows.Close(); closeErr != nil {
				log.Printf("rate-limit fallback seed close failed: category=%s", key.class)
			}
		} else {
			log.Printf("rate-limit fallback seed failed: category=%s", key.class)
		}
		cancel()
	}
}

func redisInt(value interface{}) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case int:
		return int64(number)
	case string:
		parsed, _ := strconv.ParseInt(number, 10, 64)
		return parsed
	case []byte:
		parsed, _ := strconv.ParseInt(string(number), 10, 64)
		return parsed
	default:
		return 0
	}
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
	if ip == nil || ip.To4() != nil {
		return ""
	}
	return "tor:" + hex.EncodeToString(ip[12:16])
}

func retrySeconds(first time.Time, window time.Duration, now time.Time) string {
	remaining := first.Add(window).Sub(now)
	seconds := int(math.Ceil(remaining.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
