package server

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/apimgr/countries/src/geoip"
)

type countryLookup interface {
	LookupCountry(net.IP) (geoip.CountryRecord, bool, error)
}

type allowlistedContextKey struct{}

func (s *Server) buildAllowlist() []*net.IPNet {
	allowlist := make([]*net.IPNet, 0, len(s.config.Server.Security.Allowlist))
	for _, entry := range s.config.Server.Security.Allowlist {
		_, network, err := net.ParseCIDR(entry.CIDR)
		if err == nil {
			allowlist = append(allowlist, network)
		}
	}
	return allowlist
}

func (s *Server) allowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := net.ParseIP(requestClientIP(r.RemoteAddr))
		if ip != nil {
			for _, network := range s.allowlist {
				if network.Contains(ip) {
					r = r.WithContext(context.WithValue(r.Context(), allowlistedContextKey{}, true))
					break
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isAllowlisted(ctx context.Context) bool {
	allowlisted, _ := ctx.Value(allowlistedContextKey{}).(bool)
	return allowlisted
}

func (s *Server) geoIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geoConfig := s.config.Server.GeoIP
		if !geoConfig.Enabled || s.geoIPManager == nil || isAllowlisted(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		ip := net.ParseIP(requestClientIP(r.RemoteAddr))
		if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			next.ServeHTTP(w, r)
			return
		}
		record, found, err := s.geoIPManager.LookupCountry(ip)
		if err != nil {
			log.Printf("GeoIP country lookup unavailable; allowing request: %v", err)
			next.ServeHTTP(w, r)
			return
		}
		if !found || !isCountryBlocked(record.CountryCode, geoConfig.AllowCountries, geoConfig.DenyCountries) {
			next.ServeHTTP(w, r)
			return
		}
		log.Printf("security: event=security.country_blocked ip=%q country=%q", ip.String(), record.CountryCode)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"ok": false, "error": "COUNTRY_BLOCKED", "message": "Requests from your location are not allowed",
		})
	})
}

func isCountryBlocked(countryCode string, allowCountries, denyCountries []string) bool {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	if countryCode == "" {
		return false
	}
	if len(allowCountries) > 0 {
		for _, allowed := range allowCountries {
			if countryCode == allowed {
				return false
			}
		}
		return true
	}
	for _, denied := range denyCountries {
		if countryCode == denied {
			return true
		}
	}
	return false
}
