package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	appmode "github.com/apimgr/countries/src/mode"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"gopkg.in/yaml.v3"
)

var debugSensitiveFields = map[string]struct{}{
	"authorization": {}, "token": {}, "session": {}, "code": {}, "key": {},
	"password": {}, "secret": {}, "auth": {}, "pwd": {}, "api_key": {},
	"apikey": {}, "access_token": {}, "refresh_token": {}, "cookie": {},
	"security_id": {},
}

func (s *Server) debugMiddleware(next http.Handler) http.Handler {
	if !appmode.DebugEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		var requestBody []byte
		bodyLimit := s.config.Server.Debug.BodyLogLimit()
		captureLimit := 0
		if s.config.Server.Debug.LogBodies {
			captureLimit = int(bodyLimit)
		}
		if captureLimit > 0 && r.Body != nil && r.ContentLength != 0 {
			requestBody, _ = io.ReadAll(io.LimitReader(r.Body, bodyLimit+1))
			r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(requestBody), r.Body))
			if int64(len(requestBody)) > bodyLimit {
				requestBody = requestBody[:bodyLimit]
			}
		}
		wrapped := &debugResponseWriter{
			WrapResponseWriter: middleware.NewWrapResponseWriter(w, r.ProtoMajor),
			limit:              captureLimit,
		}
		next.ServeHTTP(wrapped, r)
		s.debugLog(r, wrapped.Status(), time.Since(started), wrapped.BytesWritten(), requestBody, wrapped.body, wrapped.Header().Get("Content-Type"))
	})
}

type debugResponseWriter struct {
	middleware.WrapResponseWriter
	limit int
	body  []byte
}

func (w *debugResponseWriter) Write(body []byte) (int, error) {
	remaining := w.limit + 1 - len(w.body)
	if remaining > 0 {
		if len(body) > remaining {
			w.body = append(w.body, body[:remaining]...)
		} else {
			w.body = append(w.body, body...)
		}
	}
	return w.WrapResponseWriter.Write(body)
}

func sanitizeDebugBody(body []byte, contentType string) string {
	if len(body) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) {
	case "application/json":
		var value interface{}
		if json.Unmarshal(body, &value) != nil {
			return "[unparseable JSON omitted]"
		}
		redactDebugValue(value)
		encoded, err := json.Marshal(value)
		if err != nil {
			return "[JSON omitted]"
		}
		return string(encoded)
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return "[unparseable form omitted]"
		}
		for name := range values {
			if isDebugSensitiveField(name) {
				values.Set(name, "[redacted]")
			}
		}
		return values.Encode()
	default:
		return "[body omitted]"
	}
}

func redactDebugValue(value interface{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for name, child := range typed {
			if isDebugSensitiveField(name) {
				typed[name] = "[redacted]"
				continue
			}
			if strings.EqualFold(name, "url") || strings.HasSuffix(strings.ToLower(name), "_url") {
				if raw, ok := child.(string); ok && strings.TrimSpace(raw) != "" {
					typed[name] = sanitizeDebugURL(raw)
					continue
				}
			}
			redactDebugValue(child)
		}
	case []interface{}:
		for _, child := range typed {
			redactDebugValue(child)
		}
	}
}

func isDebugSensitiveField(name string) bool {
	name = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), " ", "_"))
	if _, sensitive := debugSensitiveFields[name]; sensitive {
		return true
	}
	for _, part := range []string{"secret", "token", "password", "credential", "apikey"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

func (s *Server) debugLog(r *http.Request, status int, duration time.Duration, size int, requestBody, responseBody []byte, responseContentType string) {
	if !appmode.DebugEnabled() {
		return
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices && isSuccessfulHealthRequest(r.URL.Path) {
		return
	}
	query := r.URL.Query()
	for name := range query {
		if isDebugSensitiveField(name) {
			query.Set(name, "[redacted]")
		}
	}
	attrs := []any{
		"method", r.Method,
		"path", r.URL.Path,
		"query", query.Encode(),
		"status", status,
		"duration_ms", duration.Milliseconds(),
		"size", size,
		"remote_addr", requestClientIP(r.RemoteAddr),
		"user_agent", r.UserAgent(),
		"referer", sanitizeDebugURL(r.Referer()),
		"request_id", RequestIDFromContext(r.Context()),
	}
	if len(requestBody) > 0 {
		attrs = append(attrs, "request_body", sanitizeDebugBody(requestBody, r.Header.Get("Content-Type")))
	}
	if len(responseBody) > 0 {
		attrs = append(attrs, "response_body", sanitizeDebugBody(responseBody, responseContentType))
	}
	slog.Debug("request", attrs...)
}

func sanitizeDebugURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[redacted]"
	}
	lower := strings.ToLower(raw)
	if parsed.Scheme == "" && parsed.Host == "" && (strings.Contains(lower, "password=") || strings.Contains(lower, "passwd=") || strings.Contains(lower, "pwd=") || strings.Contains(lower, "token=") || strings.Contains(lower, "secret=")) {
		return "[redacted]"
	}
	parsed.User = nil
	query := parsed.Query()
	for name := range query {
		if isDebugSensitiveField(name) {
			query.Set(name, "[redacted]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (s *Server) handleDebugConfig(w http.ResponseWriter, r *http.Request) {
	encoded, err := yaml.Marshal(s.config.Sanitized())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Configuration is unavailable")
		return
	}
	var sanitized map[string]interface{}
	if err := yaml.Unmarshal(encoded, &sanitized); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Configuration is unavailable")
		return
	}
	redactDebugValue(sanitized)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": sanitized})
}

func (s *Server) handleDebugRoutes(w http.ResponseWriter, _ *http.Request) {
	routes := make([]map[string]string, 0)
	err := chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, map[string]string{"method": method, "route": route})
		return nil
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Registered routes are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"count": len(routes), "routes": routes}})
}

func (s *Server) handleDebugCache(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{
		"available": false,
		"backend":   "none",
		"entries":   0,
		"bytes":     0,
	}})
}

func (s *Server) handleDebugDB(w http.ResponseWriter, _ *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{"available": false}})
		return
	}
	stats := s.db.Stats()
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{
		"available":           true,
		"open_connections":    stats.OpenConnections,
		"in_use":              stats.InUse,
		"idle":                stats.Idle,
		"wait_count":          stats.WaitCount,
		"wait_duration_ms":    stats.WaitDuration.Milliseconds(),
		"max_idle_closed":     stats.MaxIdleClosed,
		"max_lifetime_closed": stats.MaxLifetimeClosed,
	}})
}

func (s *Server) handleDebugScheduler(w http.ResponseWriter, _ *http.Request) {
	tasks := make([]map[string]interface{}, 0)
	if s.scheduler != nil {
		for _, task := range s.scheduler.GetTasks() {
			tasks = append(tasks, map[string]interface{}{
				"id":          task.ID,
				"name":        task.Name,
				"schedule":    task.Schedule,
				"enabled":     task.Enabled,
				"last_run":    task.LastRun,
				"next_run":    task.NextRun,
				"last_status": task.LastStatus,
				"run_count":   task.RunCount,
				"fail_count":  task.FailCount,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{
		"running": s.schedulerRunning.Load(),
		"tasks":   tasks,
	}})
}

func (s *Server) handleDebugMemory(w http.ResponseWriter, _ *http.Request) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": map[string]interface{}{
		"alloc_mb":       memory.Alloc / 1024 / 1024,
		"total_alloc_mb": memory.TotalAlloc / 1024 / 1024,
		"sys_mb":         memory.Sys / 1024 / 1024,
		"num_gc":         memory.NumGC,
		"heap_objects":   memory.HeapObjects,
		"goroutines":     runtime.NumGoroutine(),
	}})
}

func (s *Server) handleDebugGoroutines(w http.ResponseWriter, _ *http.Request) {
	stack := make([]byte, 1<<20)
	length := runtime.Stack(stack, true)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(ensureTextNewline(stack[:length]))
}
