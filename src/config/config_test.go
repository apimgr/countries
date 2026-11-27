package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/testutil"
	"gopkg.in/yaml.v3"
)

// DefaultConfig must return the documented, safe-by-default values so a
// fresh install works with zero configuration.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Server.PIDFile {
		t.Error("server.pidfile = false, want enabled by default")
	}
	if cfg.Web.Security.PublishPGPKey || len(cfg.Web.Security.Keyservers) != 0 {
		t.Errorf("web.security defaults = %+v, want key publication disabled and no keyservers", cfg.Web.Security)
	}
	if cfg.Server.Healthz.Root.Enabled {
		t.Error("server.healthz.root.enabled = true, want false by default")
	}

	if cfg.Server.Port != "8080" {
		t.Errorf("Server.Port = %q, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Database.Driver != "sqlite" || cfg.Server.Database.URL != "{data_dir}/db/server.db" {
		t.Errorf("Server.Database = %+v, want SQLite at the data directory", cfg.Server.Database)
	}
	if task := cfg.Server.Scheduler.Tasks["database_check"]; !task.Enabled || task.Schedule != "@every 1m" {
		t.Errorf("database_check scheduler defaults = %+v", task)
	}
	if cfg.Server.FQDN != "localhost" {
		t.Errorf("Server.FQDN = %q, want localhost", cfg.Server.FQDN)
	}
	if cfg.Server.SSL.Enabled || cfg.Server.SSL.MinVersion != "TLS1.2" || cfg.Server.SSL.LetsEncrypt.Enabled || cfg.Server.SSL.LetsEncrypt.Challenge != "http-01" {
		t.Errorf("server.ssl defaults = %+v", cfg.Server.SSL)
	}
	if cfg.Server.Address != "::" {
		t.Errorf("Server.Address = %q, want ::", cfg.Server.Address)
	}
	if cfg.Server.Tor.Expose || cfg.Server.Tor.ContactEmail != "" || cfg.Server.I2P.Expose {
		t.Errorf("overlay advertisement defaults = Tor %+v, I2P %+v; want private addresses by default", cfg.Server.Tor, cfg.Server.I2P)
	}
	if cfg.Server.Maintenance.SelfHealing.RetryInterval != "30s" || cfg.Server.Maintenance.SelfHealing.MaxAttempts != 0 {
		t.Errorf("self-healing defaults = %+v, want unlimited retries every 30s", cfg.Server.Maintenance.SelfHealing)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.I18N.DefaultLanguage != "en" || strings.Join(cfg.Server.I18N.AvailableLanguages, ",") != strings.Join(i18n.Languages(), ",") {
		t.Errorf("server.i18n defaults = %+v, want en and all registered languages %v", cfg.Server.I18N, i18n.Languages())
	}
	notifications := cfg.Server.Notifications
	if notifications.WebUI.Position != "top-right" || notifications.WebUI.Duration != 3 || notifications.WebUI.WarningDuration != 5 {
		t.Errorf("notification WebUI defaults = %+v", notifications.WebUI)
	}
	if notifications.Email.SMTP.Port != 587 || notifications.Email.SMTP.TLS != "auto" || notifications.Email.Enabled {
		t.Errorf("notification email defaults = %+v", notifications.Email)
	}
	if !notifications.Email.Events.BackupFailed || !notifications.Email.Events.SSLExpiring || !notifications.Email.Events.SSLRenewalFailed || !notifications.Email.Events.SecurityAlert || !notifications.Email.Events.SchedulerError || !notifications.Email.Events.UpdateInstalled || notifications.Email.Events.Startup || notifications.Email.Events.Shutdown {
		t.Errorf("notification event defaults = %+v", notifications.Email.Events)
	}
	if !notifications.DiskLow.Enabled || notifications.DiskLow.ThresholdPercent != 85 || notifications.DiskLow.CheckInterval != "5m" || notifications.DiskLow.Repeat != "24h" {
		t.Errorf("disk-low defaults = %+v", notifications.DiskLow)
	}
	if !notifications.DatabaseError.Enabled || notifications.DatabaseError.Failures != 3 || notifications.DatabaseError.CheckInterval != "1m" || notifications.DatabaseError.Repeat != "24h" {
		t.Errorf("database-error defaults = %+v", notifications.DatabaseError)
	}
	if cfg.Server.Tokens.MaxLinked != 25 || cfg.Server.Tokens.CookieMaxAge != "1y" || cfg.Server.Tokens.IdlePurgeDays != 0 {
		t.Errorf("token defaults = %+v, want 25 linked tokens, 1y cookie, and no idle purge", cfg.Server.Tokens)
	}
	if cfg.Server.Tokens.CookieMaxAgeSeconds() != 365*24*60*60 {
		t.Errorf("token cookie lifetime = %d seconds, want one year", cfg.Server.Tokens.CookieMaxAgeSeconds())
	}
	if cfg.Server.RateLimit.Strict != (RateLimitRuleConfig{Requests: 10, Window: 900}) || cfg.Server.RateLimit.AuthFailures != (RateLimitRuleConfig{Requests: 5, Window: 900}) {
		t.Errorf("strict/auth-failure limits = %+v, want documented defaults", cfg.Server.RateLimit)
	}
	if !cfg.Server.Metrics.Enabled || !cfg.Server.Metrics.Root.Enabled {
		t.Error("metrics and root aliases should be enabled by default")
	}
	if !cfg.Server.GeoIP.Enabled || len(cfg.Server.GeoIP.DenyCountries) != 0 || len(cfg.Server.GeoIP.AllowCountries) != 0 || len(cfg.Server.GeoIP.Presets) != 0 {
		t.Errorf("GeoIP defaults = %+v, want enabled with empty country lists and presets", cfg.Server.GeoIP)
	}
	if !cfg.Server.Metrics.IncludeSystem || !cfg.Server.Metrics.IncludeRuntime || cfg.Server.Metrics.Loki.MaxEntries != 1000 || cfg.Server.Metrics.Loki.MaxAge != time.Hour {
		t.Errorf("metrics defaults = %+v, want system/runtime collection and bounded Loki defaults", cfg.Server.Metrics)
	}
	if cfg.WebUI.Theme != "dark" {
		t.Errorf("WebUI.Theme = %q, want dark", cfg.WebUI.Theme)
	}
	if !cfg.WebUI.Notifications.Enabled {
		t.Error("WebUI.Notifications.Enabled should default to true")
	}
	if len(cfg.WebRobots.Allow) != 2 || cfg.WebRobots.Allow[0] != "/" || cfg.WebRobots.Allow[1] != "/api" {
		t.Errorf("WebRobots.Allow = %v, want [/ /api]", cfg.WebRobots.Allow)
	}
	if len(cfg.WebRobots.Deny) != 1 || cfg.WebRobots.Deny[0] != "/debug" {
		t.Errorf("WebRobots.Deny = %v, want [/debug]", cfg.WebRobots.Deny)
	}
	if len(cfg.Server.CORS.AllowedOrigins) != 1 || cfg.Server.CORS.AllowedOrigins[0] != "*" || !cfg.Server.CORS.AllowCredentials || cfg.Server.CORS.MaxAge != 86400 {
		t.Errorf("server.cors defaults = %+v, want wildcard, credentials enabled, max_age 86400", cfg.Server.CORS)
	}
	if !cfg.Server.Notifications.Email.Events.UpdateFailed || !cfg.Server.Notifications.Email.Events.UpdateStaged {
		t.Error("update_failed and update_staged email events should default to enabled")
	}
	if !strings.Contains(cfg.Server.Pages.Help.MCP, "getCountries") || !strings.Contains(cfg.Server.Pages.Help.MCP, "Example prompts") {
		t.Errorf("server.pages.help.mcp default is missing project tools or prompts: %q", cfg.Server.Pages.Help.MCP)
	}
	if !cfg.Web.Headers.ClearSiteData.OnTokenRevocation || !cfg.Web.Headers.ClearSiteData.OnConsentWithdrawal || cfg.Web.Headers.ClearSiteData.ExecutionContexts {
		t.Errorf("Clear-Site-Data defaults = %+v", cfg.Web.Headers.ClearSiteData)
	}
}

func TestLoggingConfigUsesDocumentedServerLogsPaths(t *testing.T) {
	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte("server:\n  logs:\n    level: debug\n    defaults:\n      custom: '{remote_ip} {method} {status}'\n      rotate: weekly,50MB\n      keep: none\n    access:\n      format: custom\n      rotate: monthly\n      keep: 3\n"), cfg); err != nil {
		t.Fatalf("decode server.logs config: %v", err)
	}
	if cfg.Server.Logging.Level != "debug" || cfg.Server.Logging.Access.Format != "custom" || cfg.Server.Logging.Defaults.Custom != "{remote_ip} {method} {status}" || cfg.Server.Logging.Defaults.Rotate != "weekly,50MB" || cfg.Server.Logging.Defaults.Keep != "none" || cfg.Server.Logging.Access.Rotate != "monthly" || cfg.Server.Logging.Access.Keep != "3" {
		t.Fatalf("server.logs config = %+v, want documented level/access/defaults values", cfg.Server.Logging)
	}
}

func TestConfiguredConnectionHelpers(t *testing.T) {
	for input, want := range map[string]string{"": "sqlite", " sqlite3 ": "sqlite", "TURSO": "libsql", "custom": "custom"} {
		if got := normalizeConfiguredDatabaseDriver(input); got != want {
			t.Errorf("normalizeConfiguredDatabaseDriver(%q) = %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]string{"libsql://db.example": "libsql", "https://db.example": "https", "file:./server.db": "file", "server.db": ""} {
		if got := connectionURLScheme(input); got != want {
			t.Errorf("connectionURLScheme(%q) = %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]bool{
		"redis://user:password@cache.example":  true,
		"libsql://db.example?authToken=secret": true,
		"libsql://db.example?jwt=secret":       true,
		"libsql://db.example":                  false,
		"broken?password=secret":               true,
	} {
		if got := connectionURLHasCredentials(input); got != want {
			t.Errorf("connectionURLHasCredentials(%q) = %t, want %t", input, got, want)
		}
	}
}

func TestPersistConnectionSeeds(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  cache: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := persistConnectionSeed(configPath, "server.cache.url", "redis://cache.example", false); err != nil {
		t.Fatalf("persist regular connection seed: %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil || !bytes.Contains(configData, []byte("redis://cache.example")) {
		t.Fatalf("server.yml does not contain the saved cache URL: %s (%v)", configData, err)
	}
	if err := persistConnectionSeed(configPath, "server.database.token", "remote-token", true); err != nil {
		t.Fatalf("persist secret connection seed: %v", err)
	}
	secretPath := filepath.Join(dir, "secrets.yml")
	secretData, err := os.ReadFile(secretPath)
	if err != nil || !bytes.Contains(secretData, []byte("remote-token")) {
		t.Fatalf("secrets.yml does not contain the saved token: %s (%v)", secretData, err)
	}
	info, err := os.Stat(secretPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("secrets.yml permissions = %v, %v; want 0600", info, err)
	}
	if err := persistConnectionSeed(filepath.Join(dir, "missing.yml"), "server.cache.url", "redis://cache.example", false); err == nil {
		t.Fatal("persistConnectionSeed() accepted a missing config file")
	}
	if err := os.WriteFile(configPath, []byte("server: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := persistConnectionSeed(configPath, "server.cache.url", "redis://cache.example", false); err == nil {
		t.Fatal("persistConnectionSeed() accepted malformed YAML")
	}
}

func TestResetInvalidNotificationConfig(t *testing.T) {
	tests := []struct {
		message string
		mutate  func(*Config)
		check   func(*testing.T, *Config)
	}{
		{message: "notifications.webui.position", mutate: func(cfg *Config) { cfg.Server.Notifications.WebUI.Position = "invalid" }, check: func(t *testing.T, cfg *Config) {
			if cfg.Server.Notifications.WebUI.Position != DefaultConfig().Server.Notifications.WebUI.Position {
				t.Fatal("toast position was not reset")
			}
		}},
		{message: "disk_low.threshold_percent", mutate: func(cfg *Config) { cfg.Server.Notifications.DiskLow.ThresholdPercent = 999 }, check: func(t *testing.T, cfg *Config) {
			if cfg.Server.Notifications.DiskLow.ThresholdPercent != DefaultConfig().Server.Notifications.DiskLow.ThresholdPercent {
				t.Fatal("disk threshold was not reset")
			}
		}},
		{message: "unclassified notification error", mutate: func(cfg *Config) { cfg.Server.Notifications.WebUI.Duration = -1 }, check: func(t *testing.T, cfg *Config) {
			if !reflect.DeepEqual(cfg.Server.Notifications, DefaultConfig().Server.Notifications) {
				t.Fatal("notification settings were not fully reset")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			cfg := DefaultConfig()
			test.mutate(cfg)
			resetInvalidNotificationConfig(cfg, errors.New(test.message))
			test.check(t, cfg)
		})
	}
}

func TestDebugSettingsDefaultsAndBooleanParsing(t *testing.T) {
	cfg := DefaultConfig()
	debug := cfg.Server.Debug
	if !debug.Pprof || !debug.LogQueries || !debug.LogCache || debug.LogBodies || !debug.RuntimeEndpoints || debug.MaxBodyLogSize != "10KB" || debug.BlockProfileRate != 1 || debug.MutexProfileFraction != 1 {
		t.Fatalf("debug defaults = %+v", debug)
	}
	if err := yaml.Unmarshal([]byte("pprof: no\nlog_bodies: yes\nblock_profile_rate: 0\n"), &cfg.Server.Debug); err != nil {
		t.Fatalf("decode debug settings: %v", err)
	}
	if cfg.Server.Debug.Pprof || !cfg.Server.Debug.LogBodies || cfg.Server.Debug.BlockProfileRate != 0 || cfg.Server.Debug.LogQueries != debug.LogQueries {
		t.Fatalf("parsed debug settings = %+v", cfg.Server.Debug)
	}
	if got := cfg.Server.Debug.BodyLogLimit(); got != 10*1024 {
		t.Errorf("BodyLogLimit() = %d, want %d", got, 10*1024)
	}
}

func TestLoadGeneratesWebhookSecretInPrivateSecretsFile(t *testing.T) {
	directory := testutil.TempDir(t)
	path := filepath.Join(directory, "server.yml")
	contents := "server:\n  contact:\n    admin:\n      webhooks:\n        generic: https://hooks.example.test/notify\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	secret := cfg.Server.Contact.Admin.WebhookSecrets["generic"]
	if len(secret) < 40 {
		t.Fatalf("generated webhook secret length = %d", len(secret))
	}
	info, err := os.Stat(filepath.Join(directory, "secrets.yml"))
	if err != nil {
		t.Fatalf("stat secrets.yml: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("secrets.yml mode = %04o, want 0600", info.Mode().Perm())
	}
	encoded, err := os.ReadFile(filepath.Join(directory, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "generic_secret:") || !strings.Contains(string(encoded), secret) {
		t.Error("secrets.yml does not contain the generated webhook signing secret")
	}
}

func TestWebSecurityKeyserverConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte("web:\n  security:\n    keyservers:\n      - keys.openpgp.org\n    publish_pgp_key: true\n"), cfg); err != nil {
		t.Fatalf("unmarshal web security configuration: %v", err)
	}
	if len(cfg.Web.Security.Keyservers) != 1 || cfg.Web.Security.Keyservers[0] != "keys.openpgp.org" || !cfg.Web.Security.PublishPGPKey {
		t.Fatalf("web security config = %+v", cfg.Web.Security)
	}
}

func TestLoadDNSCredentialsEncryptsAndMigratesToSecretsFile(t *testing.T) {
	dir := testutil.TempDir(t)
	configPath := filepath.Join(dir, "server.yml")
	secretsPath := filepath.Join(dir, "secrets.yml")
	config := "server:\n  ssl:\n    enabled: true\n    letsencrypt:\n      enabled: true\n      challenge: dns-01\n      dns_provider: cloudflare\n      dns_credentials:\n        CF_DNS_API_TOKEN: operator-secret-value\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Server.SSL.Enabled || !cfg.Server.SSL.LetsEncrypt.Enabled || cfg.Server.SSL.LetsEncrypt.Challenge != "dns-01" || cfg.Server.SSL.LetsEncrypt.DNSProvider != "cloudflare" {
		t.Fatalf("server.ssl settings = %+v", cfg.Server.SSL)
	}
	if cfg.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"] != "operator-secret-value" {
		t.Fatal("DNS credential was not available in the loaded config")
	}
	mainConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainConfig), "operator-secret-value") || strings.Contains(string(mainConfig), "dns_credentials") {
		t.Fatal("DNS credential remained in server.yml after migration")
	}
	var secrets struct {
		Server struct {
			SSL struct {
				LetsEncrypt struct {
					DNSCredentials map[string]string `yaml:"dns_credentials"`
				} `yaml:"letsencrypt"`
			} `yaml:"ssl"`
		} `yaml:"server"`
	}
	secretData, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(secretData, &secrets); err != nil {
		t.Fatal(err)
	}
	stored := secrets.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"]
	if !strings.HasPrefix(stored, "enc:v1:") || strings.Contains(string(secretData), "operator-secret-value") {
		t.Fatal("DNS credential was not encrypted at rest")
	}
	secretInfo, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("stat secrets.yml: %v", err)
	}
	if secretInfo.Mode().Perm() != 0600 {
		t.Fatalf("secrets.yml mode = %o, want 0600", secretInfo.Mode().Perm())
	}
	cfg, err = Load(configPath)
	if err != nil {
		t.Fatalf("Load() after migration error = %v", err)
	}
	if cfg.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"] != "operator-secret-value" {
		t.Fatal("encrypted DNS credential did not decrypt on reload")
	}
	updatedConfig := "server:\n  ssl:\n    letsencrypt:\n      dns_credentials:\n        CF_DNS_API_TOKEN: replacement-secret-value\n"
	if err := os.WriteFile(configPath, []byte(updatedConfig), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(configPath)
	if err != nil {
		t.Fatalf("Load() after credential update error = %v", err)
	}
	if cfg.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"] != "replacement-secret-value" {
		t.Fatal("updated plaintext DNS credential did not replace the stored value")
	}
}

func TestLoadHealthzRootAliasConfiguration(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  healthz:\n    root:\n      enabled: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Server.Healthz.Root.Enabled {
		t.Error("server.healthz.root.enabled = false, want true")
	}
}

func TestLoadNotificationConfiguration(t *testing.T) {
	t.Setenv("SMTP_HOST", "relay.example.test")
	t.Setenv("SMTP_PORT", "")
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_TLS", "")
	t.Setenv("SMTP_FROM_NAME", "Countries")
	t.Setenv("SMTP_FROM_EMAIL", "mail@example.test")
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := "server:\n  notifications:\n    webui:\n      position: bottom-left\n      duration: 4\n      warning_duration: 8\n    email:\n      smtp:\n        host: relay.example.test\n        port: 2525\n        tls: starttls\n      from:\n        name: Countries\n        email: mail@example.test\n      reply_to: support@example.test\n      events:\n        startup: true\n    database_error:\n      enabled: false\n      failures: 5\n      check_interval: 2m\n      repeat: 12h\n    disk_low:\n      enabled: false\n      threshold_percent: 90\n      check_interval: 10m\n      repeat: 12h\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := cfg.Server.Notifications
	if got.WebUI != (NotificationWebUIConfig{Position: "bottom-left", Duration: 4, WarningDuration: 8}) {
		t.Errorf("WebUI settings = %+v", got.WebUI)
	}
	if got.Email.Enabled || got.Email.SMTP.Host != "relay.example.test" || got.Email.SMTP.Port != 2525 || got.Email.SMTP.TLS != "starttls" || got.Email.From.Name != "Countries" || got.Email.From.Email != "mail@example.test" || got.Email.ReplyTo != "support@example.test" || !got.Email.Events.Startup {
		t.Errorf("email settings = %+v", got.Email)
	}
	if got.DiskLow.Enabled || got.DiskLow.ThresholdPercent != 90 || got.DiskLow.CheckInterval != "10m" || got.DiskLow.Repeat != "12h" {
		t.Errorf("disk-low settings = %+v", got.DiskLow)
	}
	if got.DatabaseError.Enabled || got.DatabaseError.Failures != 5 || got.DatabaseError.CheckInterval != "2m" || got.DatabaseError.Repeat != "12h" {
		t.Errorf("database-error settings = %+v", got.DatabaseError)
	}
	if task := cfg.Server.Scheduler.Tasks["database_check"]; task.Schedule != "@every 2m" {
		t.Errorf("database_check schedule = %q, want @every 2m", task.Schedule)
	}
}

func TestLoadI18NConfigurationAndInvalidFallback(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := "server:\n  i18n:\n    default_language: de\n    available_languages: [en, es, fr, de, it, ar]\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.I18N.DefaultLanguage != "de" || strings.Join(cfg.Server.I18N.AvailableLanguages, ",") != "en,es,fr,de,it,ar" {
		t.Fatalf("server.i18n = %+v", cfg.Server.I18N)
	}

	contents = "server:\n  i18n:\n    default_language: xx\n    available_languages: [en, es, fr, de, it, xx]\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() with invalid i18n settings error = %v", err)
	}
	if cfg.Server.I18N.DefaultLanguage != "en" || strings.Join(cfg.Server.I18N.AvailableLanguages, ",") != strings.Join(i18n.Languages(), ",") {
		t.Fatalf("invalid server.i18n settings were not replaced by defaults: %+v", cfg.Server.I18N)
	}
}

func TestLoadClearSiteDataBooleanConfiguration(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := "web:\n  headers:\n    clear_site_data:\n      on_token_revocation: yes\n      on_consent_withdrawal: no\n      execution_contexts: enabled\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := cfg.Web.Headers.ClearSiteData
	if !got.OnTokenRevocation || got.OnConsentWithdrawal || !got.ExecutionContexts {
		t.Fatalf("Clear-Site-Data settings = %+v", got)
	}

	if err := os.WriteFile(path, []byte("web:\n  headers:\n    clear_site_data:\n      on_token_revocation: no\n      on_consent_withdrawal: sometimes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() with invalid Clear-Site-Data boolean error = %v, want last good config", err)
	}
	if !cfg.Web.Headers.ClearSiteData.OnConsentWithdrawal {
		t.Fatal("invalid boolean did not reset to its built-in default")
	}
	if cfg.Web.Headers.ClearSiteData.OnTokenRevocation {
		t.Fatal("valid sibling setting was discarded with invalid boolean")
	}
}

func TestLoadTokenConfigurationAndValidation(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := "server:\n  tokens:\n    max_linked: 7\n    cookie_max_age: 30d\n    idle_purge_days: 90\n  rate_limit:\n    strict:\n      requests: 12\n      window: 1200\n    auth_failures:\n      requests: 8\n      window: 600\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Tokens.MaxLinked != 7 || cfg.Server.Tokens.CookieMaxAgeSeconds() != 30*24*60*60 || cfg.Server.Tokens.IdlePurgeDays != 90 {
		t.Errorf("token settings = %+v", cfg.Server.Tokens)
	}
	if cfg.Server.RateLimit.Strict != (RateLimitRuleConfig{Requests: 12, Window: 1200}) || cfg.Server.RateLimit.AuthFailures != (RateLimitRuleConfig{Requests: 8, Window: 600}) {
		t.Errorf("strict/auth-failure settings = %+v", cfg.Server.RateLimit)
	}

	if err := os.WriteFile(path, []byte("server:\n  tokens:\n    max_linked: 0\n    cookie_max_age: never\n    idle_purge_days: -1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() invalid settings error = %v", err)
	}
	defaults := DefaultConfig().Server.Tokens
	if cfg.Server.Tokens != defaults {
		t.Errorf("invalid token settings = %+v, want defaults %+v", cfg.Server.Tokens, defaults)
	}
}

func TestLoadGeoIPConfigurationNormalizesCountryCodes(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	contents := "server:\n  geoip:\n    enabled: false\n    deny_countries: [ cn, RU, invalid ]\n    allow_countries: [us, CA]\n    presets:\n      europe: [de, fr]\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.GeoIP.Enabled {
		t.Error("GeoIP.Enabled = true, want false")
	}
	if strings.Join(cfg.Server.GeoIP.DenyCountries, ",") != "CN,RU" {
		t.Errorf("DenyCountries = %v, want [CN RU]", cfg.Server.GeoIP.DenyCountries)
	}
	if strings.Join(cfg.Server.GeoIP.AllowCountries, ",") != "US,CA" {
		t.Errorf("AllowCountries = %v, want [US CA]", cfg.Server.GeoIP.AllowCountries)
	}
	if strings.Join(cfg.Server.GeoIP.Presets["europe"], ",") != "DE,FR" {
		t.Errorf("presets.europe = %v, want [DE FR]", cfg.Server.GeoIP.Presets["europe"])
	}
}

func TestLoadSecurityAllowlistNormalizesAddressesAndRejectsInvalidEntries(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  security:\n    allowlist:\n      - cidr: 192.0.2.10\n        description: monitoring\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got := cfg.Server.Security.Allowlist[0].CIDR; got != "192.0.2.10/32" {
		t.Errorf("normalized CIDR = %q, want 192.0.2.10/32", got)
	}
	if err := os.WriteFile(path, []byte("server:\n  security:\n    allowlist:\n      - cidr: invalid\n        description: invalid entry\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load() invalid allowlist error = %v, want safe defaults", err)
	}
	if len(cfg.Server.Security.Allowlist) != 0 {
		t.Fatalf("invalid allowlist = %+v, want the default empty allowlist", cfg.Server.Security.Allowlist)
	}
}

func TestSchedulerConfigValidatesSchedulesAndCriticalTaskSwitches(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	contents := "server:\n  scheduler:\n    timezone: America/New_York\n    catch_up_window: 30m\n    tasks:\n      geoip_update:\n        enabled: false\n        schedule: invalid\n      healthcheck_self:\n        enabled: false\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Scheduler.Tasks["geoip_update"].Enabled || cfg.Server.Scheduler.Tasks["geoip_update"].Schedule != "0 3 * * 0" {
		t.Errorf("GeoIP scheduler task = %+v, want disabled with default valid schedule", cfg.Server.Scheduler.Tasks["geoip_update"])
	}
	if !cfg.Server.Scheduler.Tasks["healthcheck_self"].Enabled {
		t.Error("critical healthcheck_self task must stay enabled")
	}
}

func TestLoadMetricsConfigurationAndSecretTokens(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	contents := "server:\n  metrics:\n    enabled: true\n    root:\n      enabled: false\n    auth:\n      allow_unauthenticated: false\n      tokens:\n        prometheus: operator-prometheus-token\n    include_system: false\n    include_runtime: true\n    loki:\n      max_entries: 25\n      max_age: 30m\n    duration_buckets: [0.1, 1]\n    size_buckets: [512, 1024]\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.Server.Metrics.Enabled || cfg.Server.Metrics.Root.Enabled || cfg.Server.Metrics.Auth.AllowUnauthenticated {
		t.Fatalf("metrics switches = %+v, want root and unauthenticated access disabled", cfg.Server.Metrics)
	}
	if cfg.Server.Metrics.IncludeSystem || !cfg.Server.Metrics.IncludeRuntime || cfg.Server.Metrics.Loki.MaxEntries != 25 || cfg.Server.Metrics.Loki.MaxAge != 30*time.Minute {
		t.Fatalf("metrics settings = %+v, want configured collectors and Loki limits", cfg.Server.Metrics)
	}
	if cfg.Server.Metrics.Auth.Tokens.Prometheus != "operator-prometheus-token" {
		t.Errorf("prometheus token = %q, want value migrated from server.yml", cfg.Server.Metrics.Auth.Tokens.Prometheus)
	}
	for name, value := range map[string]string{
		"prometheus": cfg.Server.Metrics.Auth.Tokens.Prometheus,
		"grafana":    cfg.Server.Metrics.Auth.Tokens.Grafana,
		"loki":       cfg.Server.Metrics.Auth.Tokens.Loki,
	} {
		if !strings.HasPrefix(value, "mtr_") && name != "prometheus" {
			t.Errorf("%s token = %q, want mtr_ prefix", name, value)
		}
		if name != "prometheus" && len(value) != 36 {
			t.Errorf("%s token length = %d, want 36", name, len(value))
		}
	}
	serverConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverConfig), "operator-prometheus-token") || strings.Contains(string(serverConfig), "tokens:") {
		t.Fatalf("server.yml still contains metrics token configuration: %s", serverConfig)
	}
	secrets, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secrets), "operator-prometheus-token") || !strings.Contains(string(secrets), cfg.Server.Metrics.Auth.Tokens.Grafana) || !strings.Contains(string(secrets), cfg.Server.Metrics.Auth.Tokens.Loki) {
		t.Fatalf("secrets.yml does not contain the metrics service tokens")
	}
	secretInfo, err := os.Stat(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if secretInfo.Mode().Perm() != 0600 {
		t.Errorf("secrets.yml permissions = %04o, want 0600", secretInfo.Mode().Perm())
	}
}

func TestLoadMetricsSecretsWinAndAreNotRewrittenToServerYML(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  metrics:\n    auth:\n      tokens:\n        prometheus: stale-config-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(dir, "secrets.yml")
	if err := os.WriteFile(secrets, []byte("server:\n  security:\n    encryption_key: "+base64.StdEncoding.EncodeToString(make([]byte, 32))+"\n  metrics:\n    auth:\n      tokens:\n        prometheus: persisted-prometheus-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Metrics.Auth.Tokens.Prometheus != "persisted-prometheus-token" {
		t.Errorf("prometheus token = %q, want secret-file value", cfg.Server.Metrics.Auth.Tokens.Prometheus)
	}
	serverConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverConfig), "stale-config-token") {
		t.Errorf("server.yml retained a metrics token: %s", serverConfig)
	}
}

func TestLoadReplacesInvalidRateLimitValues(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := "server:\n  rate_limit:\n    read:\n      requests: 0\n      window: -1\n    global_burst: 0\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	defaults := DefaultConfig().Server.RateLimit
	if cfg.Server.RateLimit.Read != defaults.Read || cfg.Server.RateLimit.GlobalBurst != defaults.GlobalBurst {
		t.Fatalf("invalid limits were not replaced by defaults: %+v", cfg.Server.RateLimit)
	}
}

func TestLoadNormalizesUpdateChannelAndDeferral(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  update:\n    branch: daily\n    defer_days: 400\n    cli_min_version: ' 1.2.3 '\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Update.Branch != "devel" || cfg.Server.Update.DeferDays != 0 || cfg.Server.Update.CLIMinVersion != "1.2.3" {
		t.Fatalf("update config = %+v, want normalized channel, deferral, and minimum version", cfg.Server.Update)
	}
}

func TestParseBoolSupportsConfiguredTruthValues(t *testing.T) {
	if !IsTruthy(" YES ") || IsTruthy("no") {
		t.Fatal("IsTruthy returned unexpected values")
	}
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: " YES ", want: true},
		{value: "enable", want: true},
		{value: "oui", want: true},
		{value: "off", want: false},
		{value: "disabled", want: false},
		{value: "nein", want: false},
	} {
		got, err := ParseBool(test.value, !test.want)
		if err != nil || got != test.want {
			t.Errorf("ParseBool(%q) = (%v, %v), want (%v, nil)", test.value, got, err, test.want)
		}
	}
	if _, err := ParseBool("maybe", false); err == nil {
		t.Error("ParseBool accepted an invalid value")
	}
	if got, err := ParseBool("", true); err != nil || !got {
		t.Errorf("ParseBool(empty) = (%v, %v), want (true, nil)", got, err)
	}
}

func TestParseBoolOrDefaultUsesFallbackForInvalidConfiguration(t *testing.T) {
	if got := ParseBoolOrDefault("test.boolean.invalid", "maybe", true); !got {
		t.Fatal("ParseBoolOrDefault() did not use the configured default")
	}
	if got := ParseBoolOrDefault("test.boolean.valid", "off", true); got {
		t.Fatal("ParseBoolOrDefault() ignored a valid false value")
	}
}

func TestLoadParsesFlexibleBooleanValues(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := "server:\n  metrics:\n    enabled: enable\nweb-ui:\n  notifications:\n    enabled: off\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Server.Metrics.Enabled || cfg.WebUI.Notifications.Enabled {
		t.Fatalf("flexible booleans were not parsed: metrics=%t notifications=%t", cfg.Server.Metrics.Enabled, cfg.WebUI.Notifications.Enabled)
	}
}

func TestRateLimitsCannotBeDisabledByConfiguration(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  rate_limit:\n    enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.RateLimit.Read.Requests < 1 || cfg.Server.RateLimit.Strict.Requests < 1 || cfg.Server.RateLimit.GlobalBurst < 1 {
		t.Fatalf("obsolete enabled setting disabled mandatory request limits: %+v", cfg.Server.RateLimit)
	}
}

// Load on a non-existent path must create the file with default contents
// and return that default config without error.
func TestLoad_CreatesDefaultWhenMissing(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "config.yml")
	t.Setenv("PORT", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if runningInContainer() {
		if cfg.Server.Port != "80" {
			t.Errorf("Load() returned Port = %q, want container default 80", cfg.Server.Port)
		}
	} else if port, parseErr := strconv.Atoi(cfg.Server.Port); parseErr != nil || port < 64000 || port > 64999 {
		t.Errorf("Load() returned Port = %q, want an available port from 64000-64999", cfg.Server.Port)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected Load() to create %q: %v", path, err)
	}
}

func TestLoad_FirstRunUsesPortEnvironment(t *testing.T) {
	t.Setenv("PORT", "64567")
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "64567" {
		t.Fatalf("Load() port = %q, want PORT value 64567", cfg.Server.Port)
	}
}

func TestLoad_ConfigWithoutPortSelectsAndPersistsDefault(t *testing.T) {
	t.Setenv("PORT", "")
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  address: 127.0.0.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if runningInContainer() {
		if cfg.Server.Port != "80" {
			t.Fatalf("Load() port = %q, want container default 80", cfg.Server.Port)
		}
	} else if port, parseErr := strconv.Atoi(cfg.Server.Port); parseErr != nil || port < 64000 || port > 64999 {
		t.Fatalf("Load() port = %q, want an available port from 64000-64999", cfg.Server.Port)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "port:") {
		t.Fatalf("server.yml does not persist the selected port:\n%s", data)
	}
}

func TestSelectInitialServerPort(t *testing.T) {
	t.Run("environment wins", func(t *testing.T) {
		port, err := selectInitialServerPort("64567", true)
		if err != nil || port != "64567" {
			t.Fatalf("selectInitialServerPort() = (%q, %v), want 64567", port, err)
		}
	})
	t.Run("container default", func(t *testing.T) {
		port, err := selectInitialServerPort("", true)
		if err != nil || port != "80" {
			t.Fatalf("selectInitialServerPort() = (%q, %v), want 80", port, err)
		}
	})
	t.Run("native random port", func(t *testing.T) {
		port, err := selectInitialServerPort("", false)
		if err != nil {
			t.Fatal(err)
		}
		value, err := strconv.Atoi(port)
		if err != nil || value < 64000 || value > 64999 {
			t.Fatalf("selectInitialServerPort() = %q, want port from 64000-64999", port)
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("", port))
		if err != nil {
			t.Fatalf("selected port %s is not available: %v", port, err)
		}
		_ = listener.Close()
	})
}

// Load must round-trip a previously saved config, overriding the defaults
// with whatever was persisted.
func TestLoad_RoundTrip(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "config.yml")

	original := DefaultConfig()
	original.Server.Port = "9999"
	original.Server.FQDN = "example.test"
	original.Server.Contact.DPO = PublicContactConfig{Name: "Privacy Team", Email: "dpo@example.test"}
	original.Server.Contact.General.Email = "contact@example.test"
	original.Server.Contact.Abuse.Email = "abuse@example.test"
	original.Server.Pages.Contact.Enabled = false
	original.Server.Pages.Contact.Antispam.MinFillSeconds = 7
	original.Server.Notifications.Email.SMTP = SMTPConfig{Host: "mail.example.test", Port: 587, TLS: "starttls"}
	original.Server.Notifications.Email.From.Email = "reports@example.test"
	original.Server.Privacy.Data.Sold = true

	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Server.Port != "9999" {
		t.Errorf("loaded Server.Port = %q, want 9999", loaded.Server.Port)
	}
	if loaded.Server.FQDN != "example.test" {
		t.Errorf("loaded Server.FQDN = %q, want example.test", loaded.Server.FQDN)
	}
	if loaded.Server.Contact.DPO.Name != original.Server.Contact.DPO.Name || loaded.Server.Contact.DPO.Email != original.Server.Contact.DPO.Email || loaded.Server.Contact.General.Email != original.Server.Contact.General.Email {
		t.Errorf("loaded contact config = %+v, want %+v", loaded.Server.Contact, original.Server.Contact)
	}
	if loaded.Server.Contact.Abuse.Name != original.Server.Contact.Abuse.Name || loaded.Server.Contact.Abuse.Email != original.Server.Contact.Abuse.Email || !reflect.DeepEqual(loaded.Server.Contact.Abuse.Webhooks, original.Server.Contact.Abuse.Webhooks) || loaded.Server.Pages.Contact.Enabled || loaded.Server.Pages.Contact.Antispam.MinFillSeconds != 7 || loaded.Server.Notifications.Email.SMTP != original.Server.Notifications.Email.SMTP || loaded.Server.Notifications.Email.From != original.Server.Notifications.Email.From {
		t.Errorf("contact settings did not round-trip: pages=%+v smtp=%+v want=%+v from=%+v want=%+v abuse=%+v", loaded.Server.Pages.Contact, loaded.Server.Notifications.Email.SMTP, original.Server.Notifications.Email.SMTP, loaded.Server.Notifications.Email.From, original.Server.Notifications.Email.From, loaded.Server.Contact.Abuse)
	}
	if !loaded.Server.Privacy.Data.Sold {
		t.Error("loaded Privacy.Data.Sold = false, want true")
	}
}

func TestPrivacyConfigDefaultsAndDynamicContent(t *testing.T) {
	privacy := DefaultConfig().Server.Privacy
	if !privacy.Data.StoredOnServer || privacy.Data.Sold {
		t.Fatalf("privacy data defaults = %+v", privacy.Data)
	}
	if !privacy.Consent.ShowUntilAcknowledged || privacy.Consent.Policy.URL != "/server/privacy" || !privacy.Consent.ShowPreferences {
		t.Fatalf("privacy consent defaults = %+v", privacy.Consent)
	}
	if !privacy.Cookies.Essential.Enabled || !privacy.Cookies.Preferences.Enabled || !privacy.Cookies.Analytics.Enabled {
		t.Fatalf("privacy cookie defaults = %+v", privacy.Cookies)
	}
	if privacy.GetConsentMessage() != privacy.Consent.Message || !strings.Contains(privacy.GetAnalyticsDescription(), privacy.Cookies.Analytics.DescriptionSuffixNotSold) {
		t.Fatal("not-sold privacy messages do not use configured defaults")
	}
	if privacy.IsCCPAApplicable() || !strings.Contains(privacy.GetDataUsageContent(), "Sold to third parties") {
		t.Fatal("not-sold privacy policy incorrectly advertises CCPA opt-out")
	}

	privacy.Data.Sold = true
	if !privacy.IsCCPAApplicable() || privacy.GetConsentMessage() != privacy.Consent.MessageIfSold {
		t.Fatal("sold privacy configuration did not select sold consent")
	}
	if !strings.Contains(privacy.GetAnalyticsDescription(), privacy.Cookies.Analytics.DescriptionSuffixSold) || !strings.Contains(privacy.GetDataUsageContent(), "opt out") {
		t.Fatal("sold privacy configuration did not select sold analytics and data usage text")
	}
}

func TestLoadPrivacyConfiguration(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	contents := `server:
  privacy:
    data:
      sold: true
      sharing:
        - condition: analytics
          when: visitor consents
          data: anonymous page views
    retention:
      period: 30 days
      export_available: true
    third_party:
      services:
        - name: Example
          purpose: Analytics
          data_sent: Country only
          policy_url: https://example.test/privacy
    consent:
      message: Custom consent text
    cookies:
      analytics:
        description: Custom analytics description
`
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	privacy := cfg.Server.Privacy
	if !privacy.Data.Sold || !privacy.Data.StoredOnServer || len(privacy.Data.Sharing) != 1 {
		t.Fatalf("privacy data did not load over defaults: %+v", privacy.Data)
	}
	if privacy.GetConsentMessage() != privacy.Consent.MessageIfSold || !strings.Contains(privacy.GetAnalyticsDescription(), privacy.Cookies.Analytics.DescriptionSuffixSold) {
		t.Fatalf("privacy dynamic values did not combine configured values and defaults: %+v", privacy)
	}
	notSold := privacy
	notSold.Data.Sold = false
	if notSold.GetConsentMessage() != "Custom consent text" {
		t.Fatalf("privacy consent message did not use the configured not-sold value: %q", notSold.GetConsentMessage())
	}
	if privacy.Retention.Period != "30 days" || !privacy.Retention.ExportAvailable || !privacy.Retention.DeletionAvailable {
		t.Fatalf("privacy retention did not load over defaults: %+v", privacy.Retention)
	}
	if len(privacy.ThirdParty.Services) != 1 || privacy.ThirdParty.Services[0].Name != "Example" {
		t.Fatalf("privacy third-party services = %+v", privacy.ThirdParty.Services)
	}
}

func TestLoadBrandingDropIn(t *testing.T) {
	directory := testutil.TempDir(t)
	dropIn := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropIn, 0755); err != nil {
		t.Fatal(err)
	}
	brandingPath := filepath.Join(dropIn, "branding.yml")
	contents := `server:
  branding:
    title: Countries Service
    content:
      about:
        format: md
        body:
          default: About the service
          fr: À propos du service
      terms:
        format: txt
        body: Terms text
      invalid:
        format: unknown
        body: ignored
  seo:
    author: Countries maintainers
web:
  ui:
    theme: light
    accent: '#336699'
`
	if err := os.WriteFile(brandingPath, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(filepath.Join(directory, "server.yml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Branding.Title != "Countries Service" || cfg.Server.Branding.Content["about"].Format != "md" {
		t.Fatalf("branding config = %+v", cfg.Server.Branding)
	}
	localizedBody, ok := cfg.Server.Branding.Content["about"].Body.(map[string]any)
	if !ok || localizedBody["fr"] != "À propos du service" {
		t.Fatalf("localized branding body = %#v", cfg.Server.Branding.Content["about"].Body)
	}
	if cfg.Server.SEO.Author != "Countries maintainers" || cfg.WebUI.Theme != "light" || cfg.WebUI.Accent != "#336699" {
		t.Fatalf("branding SEO/theme config = %+v, %+v", cfg.Server.SEO, cfg.WebUI)
	}
	if _, exists := cfg.Server.Branding.Content["invalid"]; exists {
		t.Fatal("invalid branding content was not ignored")
	}
	fileInfo, err := os.Stat(brandingPath)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("branding file mode = %04o, want 0600", fileInfo.Mode().Perm())
	}
	directoryInfo, err := os.Stat(dropIn)
	if err != nil {
		t.Fatal(err)
	}
	if directoryInfo.Mode().Perm() != 0700 {
		t.Errorf("yaml.d mode = %04o, want 0700", directoryInfo.Mode().Perm())
	}
	serverYML, err := os.ReadFile(filepath.Join(directory, "server.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverYML), "branding:") || strings.Contains(string(serverYML), "Countries Service") {
		t.Errorf("branding settings were written to server.yml: %s", serverYML)
	}
}

// Malformed YAML is ignored with a warning so configuration errors do not
// prevent the server from starting with built-in defaults.
func TestLoad_MalformedYAMLUsesDefaults(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "config.yml")

	if err := os.WriteFile(path, []byte("server: [this is not: valid: yaml"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() with malformed YAML error = %v, want safe defaults", err)
	}
	if cfg.Server.Mode != "production" || cfg.Server.Port == "" {
		t.Errorf("Load() malformed YAML config = %+v, want usable defaults", cfg.Server)
	}
}

func TestLoadSecretsKeepsCredentialsOutOfServerConfig(t *testing.T) {
	dir := testutil.TempDir(t)
	configPath := filepath.Join(dir, "server.yml")
	if err := os.WriteFile(filepath.Join(dir, "secrets.yml"), []byte("server:\n  notifications:\n    email:\n      smtp:\n        password: private-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousOutput) })
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Server.Notifications.Email.SMTP.Password != "private-value" {
		t.Fatalf("SMTP password was not loaded from secrets.yml")
	}
	if len(cfg.Server.Security.EncryptionKey) == 0 {
		t.Fatal("generated encryption key is empty")
	}
	if !validBearerToken(cfg.Server.Token, "adm_") {
		t.Fatalf("generated operator token has invalid format")
	}
	mainConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainConfig), "encryption_key") || strings.Contains(string(mainConfig), "private-value") {
		t.Fatal("secret value appeared in server.yml")
	}
	secretInfo, err := os.Stat(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if secretInfo.Mode().Perm() != 0600 {
		t.Fatalf("secrets.yml mode=%04o, want 0600", secretInfo.Mode().Perm())
	}
	secretContents, err := os.ReadFile(filepath.Join(dir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretContents), cfg.Server.Token) {
		t.Fatal("generated operator token was not persisted in secrets.yml")
	}
	if strings.Contains(logs.String(), cfg.Server.Token) || !strings.Contains(logs.String(), "operator token generated and stored") {
		t.Fatal("operator token generation log omitted its status or exposed the token")
	}
}

func TestLoadMovesOperatorTokenFromServerConfigToSecrets(t *testing.T) {
	directory := testutil.TempDir(t)
	configPath := filepath.Join(directory, "server.yml")
	operatorToken := "adm_" + strings.Repeat("A", 32)
	if err := os.WriteFile(configPath, []byte("server:\n  token: "+operatorToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Token != operatorToken {
		t.Fatal("operator token was not retained from server.yml")
	}
	mainConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mainConfig), operatorToken) {
		t.Fatal("operator token remained in server.yml")
	}
	secretConfig, err := os.ReadFile(filepath.Join(directory, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretConfig), operatorToken) {
		t.Fatal("operator token was not moved into secrets.yml")
	}
}

// Save must write a file that Load can read back byte-for-byte-equivalent
// in the fields that matter.
func TestSave(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "sub", "config.yml")

	// Save into a directory that does not exist yet must fail cleanly
	// (Save does not create parent directories).
	if err := Save(path, DefaultConfig()); err == nil {
		t.Error("Save() into missing directory expected error, got nil")
	}
}

func TestSaveNodeConfigurationWritesOnlyNodeSettings(t *testing.T) {
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	cfg := DefaultConfig()
	cfg.Server.Port = "64501"
	if err := saveNodeConfiguration(path, cfg); err != nil {
		t.Fatalf("saveNodeConfiguration() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read server.yml: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("# "+serverConfigHeader+"\n")) {
		t.Fatalf("server.yml header = %q", data[:min(len(data), 160)])
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse server.yml: %v", err)
	}
	server, ok := document["server"].(map[string]any)
	if !ok {
		t.Fatalf("server.yml server settings = %#v", document["server"])
	}
	if server["port"] != "64501" || server["database"] == nil || server["ssl"] == nil {
		t.Fatalf("server.yml node settings = %#v", server)
	}
	for _, key := range []string{"branding", "seo", "pages", "notifications", "security", "i18n"} {
		if _, exists := server[key]; exists {
			t.Errorf("server.yml contains area setting %q", key)
		}
	}
}

func TestMigrateConfigLayoutSplitsAreasAndSecrets(t *testing.T) {
	_ = ConsumeLegacyApplicationSecrets()
	dir := testutil.TempDir(t)
	path := filepath.Join(dir, "server.yml")
	if err := os.Mkdir(filepath.Join(dir, "yaml.d"), 0700); err != nil {
		t.Fatalf("create yaml.d: %v", err)
	}
	if err := os.WriteFile(path, []byte("server:\n  port: \"64502\"\n  database:\n    driver: sqlite\n  token: from-server\n  security:\n    encryption_key: from-server-key\n    installation_secret: legacy-app-secret\n  branding:\n    title: Migrated title\n    description: Migrated description\n  notifications:\n    email:\n      smtp:\n        password: smtp-secret\n  pages:\n    help:\n      enabled: true\nweb-ui:\n  theme: light\nweb:\n  api_contact: api@example.test\ndata:\n  cve:\n    api_key: cve-secret\ncustom:\n  enabled: true\n"), 0600); err != nil {
		t.Fatalf("write server.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaml.d", "branding.yml"), []byte("server:\n  branding:\n    title: Existing title\n"), 0600); err != nil {
		t.Fatalf("write existing branding drop-in: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.yml"), []byte("server:\n  token: existing-token\n"), 0600); err != nil {
		t.Fatalf("write existing secrets: %v", err)
	}
	if err := migrateConfigLayout(path); err != nil {
		t.Fatalf("migrateConfigLayout() error = %v", err)
	}
	read := func(name string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(data, &document); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return document
	}
	server := read(path)["server"].(map[string]any)
	if len(server) != 2 || server["port"] != "64502" || server["database"] == nil {
		t.Errorf("remaining node settings = %#v", server)
	}
	branding := read(filepath.Join(dir, "yaml.d", "branding.yml"))
	serverArea := branding["server"].(map[string]any)
	brand := serverArea["branding"].(map[string]any)
	if brand["title"] != "Existing title" || brand["description"] != "Migrated description" {
		t.Errorf("migrated branding = %#v", brand)
	}
	if branding["web"].(map[string]any)["ui"].(map[string]any)["theme"] != "light" {
		t.Errorf("migrated web UI = %#v", branding["web"])
	}
	secrets := read(filepath.Join(dir, "secrets.yml"))["server"].(map[string]any)
	if secrets["token"] != "existing-token" || secrets["security"].(map[string]any)["encryption_key"] != "from-server-key" || secrets["notifications"].(map[string]any)["email"].(map[string]any)["smtp"].(map[string]any)["password"] != "smtp-secret" {
		t.Errorf("migrated secrets = %#v", secrets)
	}
	if _, err := os.Stat(filepath.Join(dir, "yaml.d", "app.yml")); err != nil {
		t.Errorf("app.yml missing: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("server.yml.bak missing: %v", err)
	}
	if got := ConsumeLegacyApplicationSecrets()["installation_secret"]; got != "legacy-app-secret" {
		t.Errorf("legacy app secret handoff = %q, want source value", got)
	}
}

func TestSaveUsesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "server.yml")
	if err := os.WriteFile(path, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, DefaultConfig()); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("config permissions = %04o, want 0600", info.Mode().Perm())
	}
}

// migrateYamlToYml must rename a sibling .yaml file to .yml when the .yml
// path itself does not already exist, preserving user data across the
// extension change.
func TestMigrateYamlToYml_RenamesWhenYmlMissing(t *testing.T) {
	dir := testutil.TempDir(t)
	ymlPath := filepath.Join(dir, "config.yml")
	yamlPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(yamlPath, []byte("server:\n  port: \"1234\"\n"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	migrateYamlToYml(ymlPath)

	if _, err := os.Stat(ymlPath); err != nil {
		t.Errorf("expected %q to exist after migration: %v", ymlPath, err)
	}
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
		t.Errorf("expected %q to be gone after migration, stat err = %v", yamlPath, err)
	}
}

// migrateYamlToYml must not overwrite an existing .yml file with the
// contents of an older .yaml file.
func TestMigrateYamlToYml_DoesNotOverwriteExistingYml(t *testing.T) {
	dir := testutil.TempDir(t)
	ymlPath := filepath.Join(dir, "config.yml")
	yamlPath := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(ymlPath, []byte("new"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	if err := os.WriteFile(yamlPath, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	migrateYamlToYml(ymlPath)

	got, err := os.ReadFile(ymlPath)
	if err != nil {
		t.Fatalf("failed to read %q: %v", ymlPath, err)
	}
	if string(got) != "new" {
		t.Errorf("config.yml contents = %q, want unchanged %q", got, "new")
	}
}

// A path that does not end in .yml must be left alone entirely.
func TestMigrateYamlToYml_IgnoresNonYmlPath(t *testing.T) {
	dir := testutil.TempDir(t)
	jsonPath := filepath.Join(dir, "config.json")

	// Must not panic or error for an unrelated extension.
	migrateYamlToYml(jsonPath)
}

func TestValidateTrackingConfigurations(t *testing.T) {
	tests := []struct {
		name    string
		config  TrackingConfig
		wantErr bool
	}{
		{name: "disabled"},
		{name: "google ga4", config: TrackingConfig{Type: "google", ID: "G-ABC123"}},
		{name: "google legacy", config: TrackingConfig{Type: "google", ID: "UA-123-4"}},
		{name: "matomo", config: TrackingConfig{Type: "matomo", ID: "1", URL: "https://analytics.example.com"}},
		{name: "piwik", config: TrackingConfig{Type: "piwik", ID: "1", URL: "https://analytics.example.com"}},
		{name: "owa", config: TrackingConfig{Type: "owa", ID: "site", URL: "https://analytics.example.com"}},
		{name: "fathom", config: TrackingConfig{Type: "fathom", ID: "ABCDEFGH"}},
		{name: "plausible", config: TrackingConfig{Type: "plausible", ID: "example.com"}},
		{name: "umami", config: TrackingConfig{Type: "umami", ID: "123e4567-e89b-12d3-a456-426614174000", URL: "https://analytics.example.com"}},
		{name: "simple", config: TrackingConfig{Type: "simple"}},
		{name: "cloudflare", config: TrackingConfig{Type: "cloudflare", ID: "beacon-token"}},
		{name: "unknown type", config: TrackingConfig{Type: "unknown", ID: "id"}, wantErr: true},
		{name: "invalid google id", config: TrackingConfig{Type: "google", ID: "not-a-google-id"}, wantErr: true},
		{name: "matomo requires url", config: TrackingConfig{Type: "matomo", ID: "1"}, wantErr: true},
		{name: "invalid plausible domain", config: TrackingConfig{Type: "plausible", ID: "https://example.com"}, wantErr: true},
		{name: "invalid umami uuid", config: TrackingConfig{Type: "umami", ID: "not-a-uuid", URL: "https://analytics.example.com"}, wantErr: true},
		{name: "invalid provider url", config: TrackingConfig{Type: "fathom", ID: "ABCDEFGH", URL: "javascript:alert(1)"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.config
			err := ValidateTracking(&cfg)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateTracking(%+v) error = %v, wantErr %t", test.config, err, test.wantErr)
			}
		})
	}
}

func TestRedactConnectionURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "database token query", url: "libsql://db.example.test?authToken=secret&tls=true", want: "libsql://db.example.test?authToken=%5Bredacted%5D&tls=true"},
		{name: "password userinfo", url: "rediss://alice:p%40ss@cache.example.test:6379/2", want: "rediss://alice:%5Bredacted%5D@cache.example.test:6379/2"},
		{name: "password-only userinfo", url: "valkey://p%40ss@cache.example.test:6379/0", want: "valkey://:%5Bredacted%5D@cache.example.test:6379/0"},
		{name: "local file path", url: "/var/lib/countries/server.db", want: "/var/lib/countries/server.db"},
		{name: "malformed url", url: "http://[broken", want: "[redacted]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RedactConnectionURL(test.url); got != test.want {
				t.Fatalf("RedactConnectionURL(%q) = %q, want %q", test.url, got, test.want)
			}
		})
	}
}

func TestConfigSanitizedRedactsSecretsWithoutMutatingConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Token = "operator-secret"
	cfg.Server.Database.URL = "libsql://db.example.test?authToken=database-secret"
	cfg.Server.Database.Token = "database-secret"
	cfg.Server.Cache.URL = "rediss://user:cache-secret@cache.example.test:6379/0"
	cfg.Server.Cache.Password = "cache-secret"
	cfg.Server.Cache.TLSKey = "/config/ssl/cache.key"
	cfg.Server.Backup.Password = "backup-secret"
	cfg.Server.Security.EncryptionKey = "encryption-secret"
	cfg.Server.SSL.Key = "/config/ssl/server.key"
	cfg.Server.SSL.LetsEncrypt.DNSCredentials = map[string]string{"provider": "dns-secret"}
	cfg.Server.Pages.Contact.CaptchaSecretKey = "captcha-secret"
	cfg.Server.Notifications.Email.SMTP.Password = "smtp-secret"
	cfg.Server.Metrics.Auth.Tokens = MetricsAuthTokens{Prometheus: "prom-secret", Grafana: "grafana-secret", Loki: "loki-secret"}
	cfg.Server.Contact.Admin.WebhookSecrets = map[string]string{"discord": "webhook-secret"}
	cfg.Server.Contact.Admin.Webhooks = map[string]string{"discord": "https://user:" + "webhook-secret@example.test/hook"}
	cfg.Data.CVE.APIKey = "cve-secret"

	sanitized := cfg.Sanitized()
	encoded, err := yaml.Marshal(sanitized)
	if err != nil {
		t.Fatalf("marshal sanitized config: %v", err)
	}
	for _, secret := range []string{"operator-secret", "database-secret", "cache-secret", "backup-secret", "encryption-secret", "server.key", "dns-secret", "captcha-secret", "smtp-secret", "prom-secret", "grafana-secret", "loki-secret", "webhook-secret", "cve-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("sanitized config contains sensitive value %q", secret)
		}
	}
	if got := sanitized.Server.Database.URL; got != "libsql://db.example.test?authToken=%5Bredacted%5D" {
		t.Errorf("sanitized database URL = %q", got)
	}
	if got := sanitized.Server.Cache.URL; got != "rediss://user:%5Bredacted%5D@cache.example.test:6379/0" {
		t.Errorf("sanitized cache URL = %q", got)
	}
	if cfg.Server.Database.URL != "libsql://db.example.test?authToken=database-secret" || cfg.Server.Contact.Admin.Webhooks["discord"] != "https://user:webhook-secret@example.test/hook" {
		t.Fatal("Sanitized mutated the active configuration")
	}
}
