package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	pathpkg "path"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/apimgr/countries/src/common/network"
	appmode "github.com/apimgr/countries/src/mode"
	"github.com/go-chi/chi/v5"
	"golang.org/x/net/publicsuffix"
)

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		web := s.config.Web
		configured := web.Headers
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "SAMEORIGIN")
		header.Set("X-XSS-Protection", "1; mode=block")
		referrerPolicy := "strict-origin-when-cross-origin"
		if strings.HasPrefix(r.URL.Path, "/server/security/report/") {
			referrerPolicy = "no-referrer"
		}
		header.Set("Referrer-Policy", referrerPolicy)
		header.Set("X-Permitted-Cross-Domain-Policies", headerValue(configured.CrossDomainPolicies, "none"))
		if configured.OriginAgentCluster {
			header.Set("Origin-Agent-Cluster", "?1")
		}
		header.Set("Cross-Origin-Opener-Policy", headerValue(configured.COOP, "unsafe-none"))
		header.Set("Cross-Origin-Embedder-Policy", headerValue(configured.COEP, "unsafe-none"))
		header.Set("Cross-Origin-Resource-Policy", headerValue(configured.CORP, "cross-origin"))
		if configured.DNSPrefetchControl == "off" || configured.DNSPrefetchControl == "on" {
			header.Set("X-DNS-Prefetch-Control", configured.DNSPrefetchControl)
		}
		header.Set("Permissions-Policy", "accelerometer=(), camera=(), display-capture=(), geolocation=(), gyroscope=(), hid=(), idle-detection=(), magnetometer=(), microphone=(), midi=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=(), attribution-reporting=(), browsing-topics=(), autoplay=(self), encrypted-media=(self), fullscreen=(self), payment=(self), picture-in-picture=(self), publickey-credentials-get=(self), storage-access=(self)")
		reportURL := s.BuildURL(r, api.APIPath("server/reports/default"))
		reporting := []string{`default="` + reportURL + `"`}
		legacyReports := make([]string, 0, 2)
		defaultGroup, _ := json.Marshal(map[string]interface{}{"group": "default", "max_age": 10886400, "endpoints": []map[string]string{{"url": reportURL}}})
		legacyReports = append(legacyReports, string(defaultGroup))
		if configured.NEL.Enabled {
			nelURL := s.BuildURL(r, api.APIPath("server/reports/nel"))
			reporting = append(reporting, `nel="`+nelURL+`"`)
			nelGroup, _ := json.Marshal(map[string]interface{}{"group": "nel", "max_age": 10886400, "endpoints": []map[string]string{{"url": nelURL}}})
			legacyReports = append(legacyReports, string(nelGroup))
			nel := map[string]interface{}{"report_to": "nel", "max_age": configured.NEL.MaxAgeSeconds, "failure_fraction": configured.NEL.SampleRate, "success_fraction": 0}
			if configured.NEL.IncludeSubdomains {
				nel["include_subdomains"] = true
			}
			nelHeader, _ := json.Marshal(nel)
			header.Set("NEL", string(nelHeader))
		}
		header.Set("Reporting-Endpoints", strings.Join(reporting, ", "))
		header.Set("Report-To", strings.Join(legacyReports, ", "))
		scriptSources, frameSources, connectSources := "'self'", "'self'", "'self'"
		imageSources, styleSources, fontSources := "'self' data: blob: https:", "'self' 'unsafe-inline'", "'self' https:"
		tracking, trackingConfigured := s.analyticsTracker()
		if trackingConfigured && s.trackingEnabled() && !isOverlayRequest(s, r) {
			for _, source := range tracking.scriptHosts {
				scriptSources = appendCSPSource(scriptSources, source)
			}
			for _, source := range tracking.connectHosts {
				connectSources = appendCSPSource(connectSources, source)
			}
			for _, source := range tracking.imageHosts {
				imageSources = appendCSPSource(imageSources, source)
			}
		}
		provider := strings.ToLower(strings.TrimSpace(s.config.Server.Pages.Contact.Captcha))
		contactPath := strings.HasSuffix(r.URL.Path, "/server/contact") || r.URL.Path == "/server/contact"
		if contactPath && !strings.Contains(strings.ToLower(r.Host), ".onion") && !strings.Contains(strings.ToLower(r.Host), ".i2p") {
			switch provider {
			case "recaptcha":
				scriptSources += " https://www.google.com/recaptcha/ https://www.gstatic.com/recaptcha/"
				frameSources += " https://www.google.com/recaptcha/ https://recaptcha.google.com/recaptcha/"
				connectSources += " https://www.google.com/recaptcha/"
			case "hcaptcha":
				scriptSources += " https://hcaptcha.com https://*.hcaptcha.com"
				frameSources += " https://hcaptcha.com https://*.hcaptcha.com"
				connectSources += " https://hcaptcha.com https://*.hcaptcha.com"
			}
		}
		secure := r.TLS != nil || (s.isTrustedPeer(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
		csp := web.CSP
		scriptSources = cspDirective(scriptSources, csp.ScriptSrcExtra, csp.ScriptSrcOverride)
		styleSources = cspDirective(styleSources, csp.StyleSrcExtra, csp.StyleSrcOverride)
		imageSources = cspDirective(imageSources, csp.ImgSrcExtra, csp.ImgSrcOverride)
		fontSources = cspDirective(fontSources, csp.FontSrcExtra, csp.FontSrcOverride)
		connectSources = cspDirective(connectSources, csp.ConnectSrcExtra, csp.ConnectSrcOverride)
		frameSources = cspDirective(frameSources, csp.FrameSrcExtra, csp.FrameSrcOverride)
		formActions := cspDirective("'self'", csp.FormActionExtra, csp.FormActionOverride)
		frameAncestors := "'self'"
		corsOrigins, corsWildcard, corsDisabled := s.resolveCORSOrigins(r)
		if !corsDisabled {
			for origin := range corsOrigins {
				connectSources = appendCSPSource(connectSources, origin)
			}
			if corsWildcard {
				connectSources = appendCSPSource(connectSources, "*")
			}
		}
		policy := "default-src 'self'; script-src " + scriptSources + "; style-src " + styleSources + "; img-src " + imageSources + "; font-src " + fontSources + "; connect-src " + connectSources + "; media-src 'self' blob:; worker-src 'self' blob:; manifest-src 'self'; frame-src " + frameSources + "; frame-ancestors " + frameAncestors + "; base-uri 'self'; form-action " + formActions + "; object-src 'none';"
		if secure && !isOverlayRequest(s, r) {
			policy += " upgrade-insecure-requests;"
		}
		if csp.ReportsEnabled && cspReportSampled(r, csp.ReportsSampleRate) {
			policy += " report-to default; report-uri " + strings.TrimSuffix(s.requestURLPrefix(r), "/") + api.APIPath("server/reports/csp")
		}
		if csp.Enabled {
			mode := strings.ToLower(strings.TrimSpace(csp.Mode))
			if mode == "report-only" || appmode.DebugEnabled() && !csp.ModeExplicit {
				header.Set("Content-Security-Policy-Report-Only", policy)
			} else {
				header.Set("Content-Security-Policy", policy)
			}
		}
		if secure && !isOverlayRequest(s, r) && web.HSTS.Enabled && web.HSTS.MaxAgeSeconds > 0 {
			hsts := fmt.Sprintf("max-age=%d", web.HSTS.MaxAgeSeconds)
			if web.HSTS.IncludeSubdomains {
				hsts += "; includeSubDomains"
			}
			if web.HSTS.Preload {
				hsts += "; preload"
			}
			header.Set("Strict-Transport-Security", hsts)
		}
		next.ServeHTTP(w, r)
	})
}

func cspReportSampled(r *http.Request, sampleRate float64) bool {
	if sampleRate >= 1 {
		return true
	}
	if sampleRate <= 0 {
		return false
	}
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		return true
	}
	hash := sha256.Sum256([]byte(requestID))
	sample := float64(binary.BigEndian.Uint64(hash[:8])) / float64(^uint64(0))
	return sample < sampleRate
}

func headerValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return fallback
	}
	return value
}

func cspDirective(base, extra, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.Join(strings.Fields(strings.ReplaceAll(override, ";", " ")), " ")
	}
	for _, source := range strings.Fields(strings.ReplaceAll(extra, ";", " ")) {
		base = appendCSPSource(base, source)
	}
	return base
}

type serverTimingResponseWriter struct {
	http.ResponseWriter
	status   int
	body     bytes.Buffer
	flushed  bool
	maxBytes int
}

func (w *serverTimingResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *serverTimingResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.flushed && w.body.Len()+len(body) <= w.maxBytes {
		return w.body.Write(body)
	}
	if err := w.flushBuffered(); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(body)
}

func (w *serverTimingResponseWriter) Flush() {
	if err := w.flushBuffered(); err != nil {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *serverTimingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *serverTimingResponseWriter) flushBuffered() error {
	if w.flushed {
		return nil
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.flushed = true
	w.ResponseWriter.WriteHeader(w.status)
	if w.body.Len() == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.body.Bytes())
	w.body.Reset()
	return err
}

func (s *Server) serverTimingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !appmode.DebugEnabled() || !s.config.Web.Headers.ServerTimingInDebugOnly || isMCPRequestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		wrapped := &serverTimingResponseWriter{ResponseWriter: w, maxBytes: 2 << 20}
		next.ServeHTTP(wrapped, r)
		if !wrapped.flushed {
			w.Header().Set("Server-Timing", fmt.Sprintf("total;dur=%.1f", float64(time.Since(started).Microseconds())/1000))
		}
		if err := wrapped.flushBuffered(); err != nil {
			log.Printf("flush server timing response: %v", err)
		}
	})
}

func (s *Server) privacySignalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signal := ""
		if s.config.Web.Headers.HonorSecGPC && strings.TrimSpace(r.Header.Get("Sec-GPC")) == "1" {
			signal = "gpc"
		} else if s.config.Web.Headers.HonorDNT && strings.TrimSpace(r.Header.Get("DNT")) == "1" {
			signal = "dnt"
		}
		if signal != "" {
			r = r.WithContext(context.WithValue(r.Context(), privacySignalContextKey{}, signal))
			action := "compliance.gpc_honored"
			if signal == "dnt" {
				action = "compliance.dnt_honored"
			}
			s.recordAudit(r, "compliance", action, "request", "", map[string]any{"signal": signal}, true)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) secFetchValidationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.Web.Headers.SecFetchValidation || isBackendOrCompatPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")), "iframe") &&
			strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")) != "" &&
			!strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin") {
			s.errorResponse(w, r, "Cross-site framing is not allowed", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") && !hasBearerAuthorization(r) && !csrfExemptPath(s.config.Server.CSRF.ExemptPaths, r.URL.Path) {
				s.errorResponse(w, r, "Cross-site state changes are not allowed", http.StatusForbidden)
				return
			}
			if isSensitiveOperatorStateChange(r.URL.Path) && strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")), "navigate") &&
				r.Header.Get("Sec-Fetch-User") != "?1" {
				s.errorResponse(w, r, "User-initiated navigation is required", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isBackendOrCompatPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") || path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func csrfExemptPath(paths []string, requestPath string) bool {
	for _, exempt := range paths {
		exempt = strings.TrimSpace(exempt)
		if exempt != "" && (requestPath == strings.TrimSuffix(exempt, "/") || strings.HasPrefix(requestPath, strings.TrimSuffix(exempt, "/")+"/") || matchesPathGlob(exempt, requestPath)) {
			return true
		}
	}
	return false
}

func matchesPathGlob(pattern, value string) bool {
	matched, err := pathpkg.Match(pattern, value)
	return err == nil && matched
}

func isSensitiveOperatorStateChange(path string) bool {
	for _, suffix := range []string{"/token/reveal", "/token/rotate", "/tokens/restore", "/token/restore", "/token/forget"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return strings.Contains(path, "/preferences/tokens/") && (strings.HasSuffix(path, "/reveal") || strings.HasSuffix(path, "/rotate") || strings.HasSuffix(path, "/revoke"))
}

func hasBearerAuthorization(r *http.Request) bool {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	return len(value) > len("Bearer ") && strings.EqualFold(value[:len("Bearer ")], "Bearer ")
}

func appendCSPSource(sources, source string) string {
	if source == "" || strings.Contains(" "+sources+" ", " "+source+" ") {
		return sources
	}
	return sources + " " + source
}

func (s *Server) reportOrigin(r *http.Request) string {
	return s.BuildURL(r, "")
}

func (s *Server) BuildURL(r *http.Request, path string) string {
	scheme := "http"
	trusted := s.isTrustedPeer(r)
	if r.TLS != nil || (trusted && strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")) {
		scheme = "https"
	}
	domains := configuredDomains(os.Getenv("DOMAIN"))
	host := strings.TrimSpace(r.Host)
	if trusted {
		for _, header := range []string{"X-Forwarded-Host", "X-Real-Host", "X-Original-Host"} {
			if value := strings.TrimSpace(strings.Split(r.Header.Get(header), ",")[0]); value != "" {
				host = value
				break
			}
		}
	}
	requestedHost, requestedPort, valid := splitURLHost(host)
	if !valid || !isAllowedURLHost(requestedHost, domains) {
		host = network.GetFQDN("countries")
		requestedHost, requestedPort, valid = splitURLHost(host)
	}
	if !valid {
		return ""
	}
	if requestedPort == "80" && scheme == "http" || requestedPort == "443" && scheme == "https" {
		requestedPort = ""
	}
	if requestedPort != "" {
		if parsedPort, err := strconv.Atoi(requestedPort); err != nil || parsedPort < 1 || parsedPort > 65535 {
			requestedPort = ""
		}
	}
	if requestedPort != "" {
		requestedHost = net.JoinHostPort(requestedHost, requestedPort)
	} else if strings.Contains(requestedHost, ":") {
		requestedHost = "[" + requestedHost + "]"
	}
	prefix := s.requestURLPrefix(r)
	cleanPath := path
	if cleanPath != "" && !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	return scheme + "://" + requestedHost + prefix + cleanPath
}

func configuredDomains(raw string) []string {
	domains := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		wildcard := strings.HasPrefix(item, "*.")
		if wildcard {
			item = strings.TrimPrefix(item, "*.")
		}
		host, _, ok := splitURLHost(item)
		if ok && host != "" {
			domain := strings.ToLower(host)
			if net.ParseIP(host) == nil && host != "localhost" {
				if base, err := publicsuffix.EffectiveTLDPlusOne(domain); err == nil {
					domain = base
				}
			}
			if wildcard {
				domain = "*." + domain
			}
			domains = append(domains, domain)
		}
	}
	return domains
}

func splitURLHost(value string) (string, string, bool) {
	if value == "" || strings.ContainsAny(value, " /\\?#@\x00") {
		return "", "", false
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		host = value
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			host = strings.Trim(value, "[]")
		}
		if strings.Count(value, ":") > 1 && net.ParseIP(host) == nil {
			return "", "", false
		}
	} else if port == "" {
		return "", "", false
	}
	if net.ParseIP(host) == nil {
		if len(host) > 253 {
			return "", "", false
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", "", false
			}
			for _, char := range label {
				if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
					return "", "", false
				}
			}
		}
	}
	if port != "" {
		parsedPort, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", "", false
		}
	}
	return host, port, true
}

func isAllowedURLHost(host string, domains []string) bool {
	if len(domains) == 0 || strings.HasSuffix(strings.ToLower(host), ".onion") || strings.HasSuffix(strings.ToLower(host), ".b32.i2p") {
		return true
	}
	host = strings.ToLower(host)
	for _, domain := range domains {
		if host == domain || strings.HasPrefix(domain, "*.") && strings.HasSuffix(host, strings.TrimPrefix(domain, "*")) {
			return true
		}
		if !strings.HasPrefix(domain, "*.") && strings.Contains(domain, ".") && strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func firstHeaderValue(values ...string) string {
	for _, value := range values {
		if item := strings.TrimSpace(strings.Split(value, ",")[0]); item != "" {
			return item
		}
	}
	return ""
}

func cleanURLPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\\\x00") {
		return ""
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return ""
		}
	}
	return strings.TrimSuffix(value, "/")
}

func (s *Server) requestURLPrefix(r *http.Request) string {
	if s.isTrustedPeer(r) {
		if prefix := cleanURLPrefix(firstHeaderValue(r.Header.Get("X-Forwarded-Prefix"), r.Header.Get("X-Forwarded-Path"), r.Header.Get("X-Script-Name"))); prefix != "" {
			return prefix
		}
	}
	return cleanURLPrefix(s.config.Server.BaseURL)
}

func (s *Server) handleSecurityReport(w http.ResponseWriter, r *http.Request) {
	if reportType := chi.URLParam(r, "report"); reportType != "default" && reportType != "csp" && reportType != "nel" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/csp-report") && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/reports+json") {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	clientIP := requestClientIP(r.RemoteAddr)
	reportRule := s.config.Web.Reports
	for _, limit := range []struct {
		class    string
		requests int
		window   time.Duration
	}{
		{class: "reports", requests: reportRule.RateLimitPerMinute, window: time.Minute},
		{class: "reports_burst", requests: reportRule.RateLimitPerIPBurst, window: 10 * time.Second},
	} {
		allowed, retry, err := s.rateLimiter.recordClass(r.Context(), clientIP, limit.class, time.Now(), limit.requests, limit.window)
		if err != nil {
			writeRateLimitUnavailable(w)
			return
		}
		if !allowed {
			writeRateLimited(w, retry)
			return
		}
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Printf("close security report body: %v", err)
		}
	}()
	if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	log.Printf("security report received: request_id=%s", RequestIDFromContext(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}
