// Package network resolves public host names and display URLs.
package network

import (
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// GetFQDN resolves the primary public host without guessing localhost.
func GetFQDN(projectName string) string {
	hostname, _ := os.Hostname()
	return resolveFQDN(GetAllDomains(), hostname, os.Getenv("HOSTNAME"), getGlobalIPv6(), getGlobalIPv4(), projectName)
}

// GetAllDomains returns the non-empty domains configured through DOMAIN.
func GetAllDomains() []string {
	values := strings.Split(os.Getenv("DOMAIN"), ",")
	domains := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		domain := strings.TrimSpace(value)
		if domain == "" {
			continue
		}
		key := strings.ToLower(domain)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		domains = append(domains, domain)
	}
	return domains
}

// GetWildcardDomain returns a wildcard when all configured names share a base domain.
func GetWildcardDomain() string {
	domains := GetAllDomains()
	if len(domains) < 2 {
		return ""
	}
	base := extractBaseDomain(domains[0])
	for _, domain := range domains[1:] {
		if extractBaseDomain(domain) != base {
			return ""
		}
	}
	return "*." + base
}

// GetDisplayURL returns a public URL for a listener; unresolved hosts stay empty.
func GetDisplayURL(projectName string, port int, isHTTPS bool) string {
	host := GetFQDN(projectName)
	return displayURL(host, getGlobalIPv6(), getGlobalIPv4(), projectName, port, isHTTPS)
}

func displayURL(host, ipv6, ipv4, projectName string, port int, isHTTPS bool) string {
	if isOverlayHost(host) {
		if port == 80 || port == 443 {
			return "http://" + host
		}
		return fmt.Sprintf("http://%s:%d", host, port)
	}
	if host == "" || isDevTLD(host, projectName) {
		host = ipv6
		if host == "" {
			host = ipv4
		}
	}
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") && net.ParseIP(strings.Trim(host, "[]")) != nil {
		host = "[" + strings.Trim(host, "[]") + "]"
	}
	return FormatURL(host, port, isHTTPS)
}

func resolveFQDN(domains []string, hostname, hostnameEnv, ipv6, ipv4, projectName string) string {
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain != "" && !isOverlayHost(domain) {
			return domain
		}
	}
	if host := strings.TrimSpace(hostname); host != "" && !isLoopback(host) && !isOverlayHost(host) {
		return host
	}
	if host := strings.TrimSpace(hostnameEnv); host != "" && !isLoopback(host) && !isOverlayHost(host) {
		return host
	}
	if ipv6 != "" && isPublicIP(net.ParseIP(strings.Trim(ipv6, "[]"))) {
		return ipv6
	}
	if ipv4 != "" && isPublicIP(net.ParseIP(ipv4)) {
		return ipv4
	}
	return ""
}

// FormatURL builds a display URL and omits the standard HTTP and HTTPS ports.
func FormatURL(host string, port int, isHTTPS bool) string {
	protocol := "http"
	if isHTTPS || port == 443 {
		protocol = "https"
	}
	if port == 80 || port == 443 {
		return protocol + "://" + host
	}
	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

func isLoopback(host string) bool {
	lower := strings.ToLower(strings.TrimSpace(host))
	if lower == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(lower, "[]"))
	return ip != nil && ip.IsLoopback()
}

func isOverlayHost(host string) bool {
	lower := strings.ToLower(strings.TrimSpace(host))
	return strings.HasSuffix(lower, ".onion") || strings.HasSuffix(lower, ".i2p") || strings.HasSuffix(lower, ".exit")
}

func isDevTLD(host, projectName string) bool {
	lower := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	project := strings.ToLower(strings.TrimSpace(projectName))
	if project != "" && (lower == project || strings.HasSuffix(lower, "."+project) || lower == project+".local" || lower == project+".test") {
		return true
	}
	for _, suffix := range []string{".local", ".test", ".example", ".invalid", ".localhost", ".lan", ".internal", ".home", ".localdomain", ".home.arpa", ".intranet", ".corp", ".private"} {
		if strings.HasSuffix(lower, suffix) || lower == strings.TrimPrefix(suffix, ".") {
			return true
		}
	}
	return false
}

func extractBaseDomain(domain string) string {
	base, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSpace(domain))
	if err != nil {
		return strings.TrimSpace(domain)
	}
	return base
}

func getGlobalIPv6() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() == nil && isPublicIP(ip) {
			return ip.String()
		}
	}
	return ""
}

func getGlobalIPv4() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && isPublicIP(ip) {
			return ip.String()
		}
	}
	return ""
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}
