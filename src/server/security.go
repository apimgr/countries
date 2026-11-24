package server

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/apimgr/countries/src/common/api"
	"github.com/go-chi/chi/v5"
	"golang.org/x/net/publicsuffix"
)

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "SAMEORIGIN")
		header.Set("X-XSS-Protection", "1; mode=block")
		header.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		header.Set("X-Permitted-Cross-Domain-Policies", "none")
		header.Set("Origin-Agent-Cluster", "?1")
		header.Set("Cross-Origin-Opener-Policy", "unsafe-none")
		header.Set("Cross-Origin-Embedder-Policy", "unsafe-none")
		header.Set("Cross-Origin-Resource-Policy", "cross-origin")
		header.Set("Permissions-Policy", "accelerometer=(), ambient-light-sensor=(), battery=(), camera=(), display-capture=(), geolocation=(self), gyroscope=(), hid=(), idle-detection=(), magnetometer=(), microphone=(), midi=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=(), attribution-reporting=(), browsing-topics=(), interest-cohort=(), autoplay=(self), encrypted-media=(self), fullscreen=(self), payment=(self), picture-in-picture=(self), publickey-credentials-get=(self), storage-access=(self), web-share=(self)")
		reportURL := s.BuildURL(r, api.APIPath("server/reports/default"))
		header.Set("Reporting-Endpoints", `default="`+reportURL+`"`)
		reportTo, _ := json.Marshal(map[string]interface{}{
			"group": "default", "max_age": 10886400,
			"endpoints": []map[string]string{{"url": reportURL}},
		})
		header.Set("Report-To", string(reportTo))
		nel, _ := json.Marshal(map[string]interface{}{
			"report_to": "default", "max_age": 2592000, "include_subdomains": true,
		})
		header.Set("NEL", string(nel))
		scriptSources, frameSources, connectSources := "'self'", "'self'", "'self'"
		imageSources := "'self' data: blob: https:"
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
		policy := "default-src 'self'; script-src " + scriptSources + "; style-src 'self' 'unsafe-inline'; img-src " + imageSources + "; font-src 'self' https:; connect-src " + connectSources + "; media-src 'self' blob:; worker-src 'self' blob:; manifest-src 'self'; frame-src " + frameSources + "; frame-ancestors 'self'; base-uri 'self'; form-action 'self'; object-src 'none';"
		if secure && !isOverlayRequest(s, r) {
			policy += " upgrade-insecure-requests;"
		}
		policy += " report-to default; report-uri " + api.APIPath("server/reports/csp")
		if secure {
			header.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		header.Set("Content-Security-Policy", policy)
		next.ServeHTTP(w, r)
	})
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
		host = strings.TrimSpace(s.config.Server.FQDN)
		requestedHost, requestedPort, valid = splitURLHost(host)
	}
	if !valid {
		requestedHost, requestedPort = "localhost", ""
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
	if !s.isTrustedPeer(r) {
		return ""
	}
	return cleanURLPrefix(firstHeaderValue(r.Header.Get("X-Forwarded-Prefix"), r.Header.Get("X-Forwarded-Path"), r.Header.Get("X-Script-Name")))
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
