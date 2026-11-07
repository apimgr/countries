package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/countries/src/common/api"
	"github.com/go-chi/chi/v5"
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
		origin := s.reportOrigin(r)
		reportURL := origin + api.APIPath("server/reports/default")
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
		policy := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' https:; connect-src 'self'; media-src 'self' blob:; worker-src 'self' blob:; manifest-src 'self'; frame-src 'self'; frame-ancestors 'self'; base-uri 'self'; form-action 'self'; object-src 'none'; upgrade-insecure-requests; report-to default; report-uri " + api.APIPath("server/reports/csp")
		secure := r.TLS != nil || (s.isTrustedPeer(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
		if secure {
			header.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		header.Set("Content-Security-Policy", policy)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) reportOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || (s.isTrustedPeer(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
		scheme = "https"
	}
	host := r.Host
	if s.isTrustedPeer(r) {
		if forwardedHost := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwardedHost != "" {
			host = forwardedHost
		}
	}
	parsed, err := url.Parse(scheme + "://" + host)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "http://localhost"
	}
	return parsed.Scheme + "://" + parsed.Host
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
	defer r.Body.Close()
	if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	log.Printf("security report received: request_id=%s", RequestIDFromContext(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}
