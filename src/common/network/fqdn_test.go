package network

import (
	"net"
	"testing"
)

func TestResolveFQDNPrecedenceAndUnresolvedValue(t *testing.T) {
	publicIPv6 := "2001:4860:4860::8888"
	publicIPv4 := "8.8.8.8"
	for _, test := range []struct {
		name, want string
		domains    []string
		hostname   string
		hostEnv    string
		ipv6       string
		ipv4       string
	}{
		{name: "configured primary", domains: []string{" api.example.test ", "www.example.test"}, hostname: "node", want: "api.example.test"},
		{name: "hostname", hostname: "node.example.test", hostEnv: "fallback", ipv6: publicIPv6, want: "node.example.test"},
		{name: "hostname environment", hostname: "localhost", hostEnv: "node.example.test", ipv6: publicIPv6, want: "node.example.test"},
		{name: "public IPv6", hostname: "localhost", hostEnv: "127.0.0.1", ipv6: publicIPv6, ipv4: publicIPv4, want: publicIPv6},
		{name: "public IPv4", hostname: "localhost", hostEnv: "127.0.0.1", ipv4: publicIPv4, want: publicIPv4},
		{name: "unresolved stays empty", hostname: "localhost", hostEnv: "127.0.0.1"},
		{name: "overlay domain skipped", domains: []string{"hidden.onion"}, hostname: "node.example.test", want: "node.example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveFQDN(test.domains, test.hostname, test.hostEnv, test.ipv6, test.ipv4, "countries"); got != test.want {
				t.Fatalf("resolveFQDN() = %q, want %q", got, test.want)
			}
		})
	}
	if !isPublicIP(net.ParseIP(publicIPv4)) || !isPublicIP(net.ParseIP(publicIPv6)) {
		t.Fatal("test public addresses were rejected")
	}
}

func TestDisplayURLDevelopmentAndPortRules(t *testing.T) {
	for _, test := range []struct {
		name, host, ipv6, ipv4, project string
		port                            int
		https                           bool
		want                            string
	}{
		{name: "standard HTTP port", host: "api.example.com", port: 80, want: "http://api.example.com"},
		{name: "standard HTTPS port", host: "api.example.com", port: 443, want: "https://api.example.com"},
		{name: "custom HTTPS port", host: "api.example.com", port: 8443, https: true, want: "https://api.example.com:8443"},
		{name: "dev TLD uses public IPv6", host: "countries.test", ipv6: "2001:4860:4860::8888", project: "countries", port: 8080, want: "http://[2001:4860:4860::8888]:8080"},
		{name: "unresolved stays empty", host: "", port: 8080},
		{name: "overlays stay HTTP", host: "example.onion", port: 443, https: true, want: "http://example.onion"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := displayURL(test.host, test.ipv6, test.ipv4, test.project, test.port, test.https); got != test.want {
				t.Fatalf("displayURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGetWildcardDomain(t *testing.T) {
	t.Setenv("DOMAIN", "api.example.co.uk,www.example.co.uk")
	if got := GetWildcardDomain(); got != "*.example.co.uk" {
		t.Fatalf("GetWildcardDomain() = %q, want *.example.co.uk", got)
	}
	t.Setenv("DOMAIN", "api.example.com,service.other.com")
	if got := GetWildcardDomain(); got != "" {
		t.Fatalf("GetWildcardDomain() = %q, want empty for unrelated zones", got)
	}
}
