package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
	"github.com/shirou/gopsutil/v3/disk"
	"gopkg.in/yaml.v3"
)

func TestMainHelpers(t *testing.T) {
	t.Run("retry delay", func(t *testing.T) {
		for _, test := range []struct {
			value string
			want  time.Duration
		}{{"2m", 2 * time.Minute}, {"0s", time.Hour}, {"invalid", time.Hour}, {"-1s", time.Hour}} {
			if got := taskRetryDelay(test.value); got != test.want {
				t.Errorf("taskRetryDelay(%q) = %s, want %s", test.value, got, test.want)
			}
		}
	})

	t.Run("listener resolution", func(t *testing.T) {
		for _, test := range []struct {
			ports string
			ssl   bool
			http  string
			https string
			err   bool
		}{{"8080", false, "8080", "", false}, {"443", false, "", "443", false}, {"8443", true, "", "8443", false}, {"80,443", false, "80", "443", false}, {"80,443,8080", false, "", "", true}, {"0", false, "", "", true}} {
			httpPort, httpsPort, err := resolveServerListeners(test.ports, test.ssl)
			if (err != nil) != test.err || httpPort != test.http || httpsPort != test.https {
				t.Errorf("resolveServerListeners(%q, %t) = (%q, %q, %v)", test.ports, test.ssl, httpPort, httpsPort, err)
			}
		}
		if got := listenerAddress("[::1]", "8080"); got != "[::1]:8080" {
			t.Errorf("listenerAddress() = %q", got)
		}
		if listenerAddress("127.0.0.1", "") != "" {
			t.Fatal("listenerAddress() should be empty without a port")
		}
	})

	t.Run("challenge normalization", func(t *testing.T) {
		for value, want := range map[string]string{"": "http-01", "http": "http-01", "http01": "http-01", "TLS-ALPN": "tls-alpn-01", "dns01": "dns-01", "CUSTOM": "custom"} {
			if got := normalizeSSLChallenge(value); got != want {
				t.Errorf("normalizeSSLChallenge(%q) = %q, want %q", value, got, want)
			}
		}
	})

	t.Run("domain list", func(t *testing.T) {
		t.Setenv("DOMAIN", " Example.com,example.com, other.example ,, ")
		cfg := config.DefaultConfig()
		if got := strings.Join(sslDomains(cfg), ","); got != "Example.com,other.example" {
			t.Fatalf("sslDomains() = %q", got)
		}
		t.Setenv("DOMAIN", "")
		cfg.Server.FQDN = "countries.example"
		if got := strings.Join(sslDomains(cfg), ","); got != "countries.example" {
			t.Fatalf("sslDomains(config) = %q", got)
		}
	})

	t.Run("SMTP address and FQDN", func(t *testing.T) {
		for value, want := range map[string]string{"127.0.0.1": "127.0.0.1", "10.2.3.4": "10.2.3.4", "169.254.1.2": "169.254.1.2", "100.64.0.1": "100.64.0.1", "8.8.8.8": "", "mail.example": ""} {
			if got := privateSMTPAddress(value); got != want {
				t.Errorf("privateSMTPAddress(%q) = %q, want %q", value, got, want)
			}
		}
		cfg := config.DefaultConfig()
		cfg.Server.FQDN = "countries.example"
		if got := notificationFQDN(cfg); got != "countries.example" {
			t.Fatalf("notificationFQDN() = %q", got)
		}
		cfg.Server.FQDN = "localhost"
		if got := notificationFQDN(cfg); got == "" {
			t.Fatal("notificationFQDN() returned an empty fallback")
		}
		if got := smtpSenderConfig(config.SMTPConfig{Host: "smtp.example", Port: 587, TLS: "starttls"}); got.Host != "smtp.example" || got.Port != 587 || got.TLS != "starttls" {
			t.Fatalf("smtpSenderConfig() = %+v", got)
		}
	})

	t.Run("persist detected SMTP", func(t *testing.T) {
		path := filepath.Join(testutil.TempDir(t), "server.yml")
		if err := os.WriteFile(path, []byte("server:\n  mode: production\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := persistDetectedSMTP(path, "smtp.example", 587); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if err := yaml.Unmarshal(data, &values); err != nil {
			t.Fatal(err)
		}
		serverValues := values["server"].(map[string]any)
		smtp := serverValues["notifications"].(map[string]any)["email"].(map[string]any)["smtp"].(map[string]any)
		if smtp["host"] != "smtp.example" || smtp["port"] != 587 || serverValues["mode"] != "production" {
			t.Fatalf("persisted SMTP/config values = %#v", values)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("server.yml permissions = %v, err = %v", info.Mode().Perm(), err)
		}
		if err := persistDetectedSMTP(filepath.Join(filepath.Dir(path), "missing.yml"), "smtp.example", 25); err == nil {
			t.Fatal("persistDetectedSMTP() accepted a missing config")
		}
	})

	t.Run("YAML mapping helpers", func(t *testing.T) {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		first := yamlMappingValue(root, "server", true)
		if yamlMappingValue(root, "server", false) != first || yamlMappingValue(root, "missing", false) != nil {
			t.Fatal("yamlMappingValue() failed to find/create mapping")
		}
		if yamlMappingValue(&yaml.Node{Kind: yaml.ScalarNode}, "nested", true).Kind != yaml.MappingNode {
			t.Fatal("yamlMappingValue() did not normalize the node")
		}
		value := &yaml.Node{Kind: yaml.ScalarNode, Value: "first"}
		yamlSetMappingValue(first, "key", value)
		replacement := &yaml.Node{Kind: yaml.ScalarNode, Value: "second"}
		yamlSetMappingValue(first, "key", replacement)
		if yamlMappingValue(first, "key", false) != replacement {
			t.Fatal("yamlSetMappingValue() did not replace existing value")
		}
	})

	t.Run("DNS fingerprint and persisted state", func(t *testing.T) {
		first := dnsCredentialsFingerprint(" Route53 ", map[string]string{"secret": "b", "key": "a"})
		if first != dnsCredentialsFingerprint("route53", map[string]string{"key": "a", "secret": "b"}) || first == dnsCredentialsFingerprint("route53", map[string]string{"key": "changed"}) {
			t.Fatal("DNS credential fingerprint is not normalized and deterministic")
		}
		dbPath := filepath.Join(testutil.TempDir(t), "dns.db")
		db, err := database.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Close(); err != nil {
				t.Errorf("close database: %v", err)
			}
		}()
		if err := syncDNSValidationState(db, "route53", map[string]string{"key": "a"}); err != nil {
			t.Fatal(err)
		}
		if err := database.SetAppSecret(context.Background(), db, "dns_validated_at", "now", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := syncDNSValidationState(db, "route53", map[string]string{"key": "changed"}); err != nil {
			t.Fatal(err)
		}
		if _, found, err := database.GetAppSecret(context.Background(), db, "dns_validated_at"); err != nil || found {
			t.Fatalf("changed credentials retained DNS validation: found=%t err=%v", found, err)
		}
	})

	t.Run("backup args and size limits", func(t *testing.T) {
		name, ssl, data, err := parseBackupArgs([]string{"backup.tar", "--include-ssl", "--include-data"})
		if err != nil || name != "backup.tar" || !ssl || !data {
			t.Fatalf("parseBackupArgs() = %q %t %t %v", name, ssl, data, err)
		}
		for _, args := range [][]string{{"-x"}, {"one", "two"}} {
			if _, _, _, err := parseBackupArgs(args); err == nil {
				t.Errorf("parseBackupArgs(%v) accepted invalid input", args)
			}
		}
		for value, want := range map[string]int64{"2KiB": 2048, "3MB": 3000000, "4G": 4 << 30, "5B": 5, "bad": 0, "-1": 0, "": 0} {
			if got := parseSizeLimit(value); got != want {
				t.Errorf("parseSizeLimit(%q) = %d, want %d", value, got, want)
			}
		}
		if got := backupLimits(config.RestoreConfig{MaxArchiveSize: "10MB", MaxFiles: 4, MaxRatio: 3}); got.MaxArchiveSize != 10_000_000 || got.MaxFiles != 4 || got.MaxRatio != 3 {
			t.Fatalf("backupLimits() = %+v", got)
		}
	})

	t.Run("disk paths and byte formatting", func(t *testing.T) {
		root := testutil.TempDir(t)
		missing := filepath.Join(root, "missing", "nested")
		if got := existingDiskPath(missing); got != root {
			t.Fatalf("existingDiskPath() = %q, want %q", got, root)
		}
		partitions := []disk.PartitionStat{{Mountpoint: root}, {Mountpoint: filepath.Join(root, "nested")}}
		if got := diskVolumeForPath(filepath.Join(root, "nested", "data"), partitions); got != filepath.Join(root, "nested") {
			t.Fatalf("diskVolumeForPath() = %q", got)
		}
		if got := diskVolumeForPath("/outside/path", nil); got != filepath.Clean("/outside/path") {
			t.Fatalf("diskVolumeForPath(fallback) = %q", got)
		}
		for value, want := range map[uint64]string{0: "0 B", 1024: "1.0 KB", 1 << 20: "1.0 MB"} {
			if got := humanBytes(value); got != want {
				t.Errorf("humanBytes(%d) = %q, want %q", value, got, want)
			}
		}
	})

	t.Run("disk monitor cancellation", func(t *testing.T) {
		monitor := newDiskLowMonitor(0, "bad")
		if monitor.threshold != 85 || monitor.repeat != 24*time.Hour || monitor.Status() != "ok" {
			t.Fatalf("monitor defaults = %+v", monitor)
		}
		monitor.setStatus("error")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := monitor.Check(ctx, testutil.TempDir(t), testutil.TempDir(t), testutil.TempDir(t), nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("Check(canceled) = %v", err)
		}
	})

	t.Run("email command preview and argument errors", func(t *testing.T) {
		cfg := config.DefaultConfig()
		configDir := testutil.TempDir(t)
		preview := captureOutput(t, func() {
			if err := runEmailCommand([]string{"test", "--dry-run"}, cfg, "", configDir); err != nil {
				t.Fatalf("runEmailCommand(dry-run): %v", err)
			}
		})
		if !strings.Contains(preview, "Subject:") || !strings.Contains(preview, "Countries API") {
			t.Fatalf("email preview omitted rendered template fields: %q", preview)
		}
		for _, args := range [][]string{{}, {"send"}, {"test", "--to"}, {"test", "--unknown"}} {
			if err := runEmailCommand(args, cfg, "", configDir); err == nil {
				t.Errorf("runEmailCommand(%v) accepted invalid arguments", args)
			}
		}
	})

	t.Run("disk monitor healthy paths", func(t *testing.T) {
		root := testutil.TempDir(t)
		monitor := newDiskLowMonitor(101, "1h")
		if err := monitor.Check(context.Background(), root, root, root, nil); err != nil {
			t.Fatalf("Check(healthy paths) error = %v", err)
		}
		if got := monitor.Status(); got != "ok" {
			t.Fatalf("monitor status = %q, want ok", got)
		}
	})

	t.Run("security cleanup", func(t *testing.T) {
		root := testutil.TempDir(t)
		db, err := database.Open(filepath.Join(root, "security.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS security_reports (closed_at DATETIME)`); err != nil {
			t.Fatal(err)
		}
		if err := runSecurityCleanup(context.Background(), db, root, root, 30, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`DROP TABLE security_reports`); err != nil {
			t.Fatal(err)
		}
		if err := runSecurityCleanup(context.Background(), db, root, root, 30, time.Now()); err == nil {
			t.Fatal("runSecurityCleanup() ignored an unusable database")
		}
	})

	t.Run("wait for listener failures", func(t *testing.T) {
		done := make(chan error, 1)
		wantErr := errors.New("listener failed")
		done <- wantErr
		if ready, err := waitForListener(context.Background(), "not-an-address", done); err == nil || ready {
			t.Fatalf("waitForListener(invalid address) = %t, %v", ready, err)
		}
		if ready, err := waitForListener(context.Background(), "127.0.0.1:0", done); err == nil || ready {
			t.Fatalf("waitForListener(unbound) = %t, %v", ready, err)
		}
	})

	t.Run("update flags and branch aliases", func(t *testing.T) {
		var update optionalUpdateFlag
		if update.String() != "" || !update.IsBoolFlag() {
			t.Fatal("optional update flag defaults are incorrect")
		}
		if err := update.Set("branch"); err != nil || !update.set || update.value != "branch" {
			t.Fatalf("optional update flag Set() = %+v, %v", update, err)
		}
		for value, want := range map[string]string{"daily": "devel", "rolling": "devel", "development": "devel", "BETA": "beta", "stable": "stable"} {
			if got := normalizeUpdateBranch(value); got != want {
				t.Errorf("normalizeUpdateBranch(%q) = %q, want %q", value, got, want)
			}
		}
	})

	t.Run("notification manager without SMTP", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Server.Notifications.Email.SMTP.Host = "127.0.0.1"
		cfg.Server.Notifications.Email.SMTP.Port = 1
		cfg.Server.Notifications.Email.SMTP.TLS = "none"
		manager, err := prepareNotificationManager(cfg, filepath.Join(testutil.TempDir(t), "server.yml"), testutil.TempDir(t))
		if err == nil || manager == nil || cfg.Server.Notifications.Email.Enabled {
			t.Fatalf("prepareNotificationManager() = manager %v, enabled %t, err %v", manager != nil, cfg.Server.Notifications.Email.Enabled, err)
		}
	})

	t.Run("listener ready and canceled", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		address := listener.Addr().String()
		if ready, err := waitForListener(context.Background(), address, make(chan error)); err != nil || !ready {
			t.Fatalf("waitForListener(ready) = %t, %v", ready, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if ready, err := waitForListener(ctx, "127.0.0.1:1", make(chan error)); err != nil || ready {
			t.Fatalf("waitForListener(canceled) = %t, %v", ready, err)
		}
	})
}
