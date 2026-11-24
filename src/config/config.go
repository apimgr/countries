// Package config loads, validates, and persists application configuration.
package config

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/countries/src/common/i18n"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/paths"
	backgroundscheduler "github.com/apimgr/countries/src/scheduler"
	"gopkg.in/yaml.v3"
)

type Config struct {
	ConfigDir   string            `yaml:"-"`
	Data        DataConfig        `yaml:"data"`
	Server      ServerConfig      `yaml:"server"`
	CLI         CLIConfig         `yaml:"cli"`
	Web         WebConfig         `yaml:"web"`
	WebUI       WebUIConfig       `yaml:"web-ui"`
	WebRobots   WebRobotsConfig   `yaml:"web-robots"`
	WebSecurity WebSecurityConfig `yaml:"web-security"`
}

type DataConfig struct {
	SecurityDir string          `yaml:"security_dir"`
	CVE         CVEDataConfig   `yaml:"cve"`
	Trivy       TrivyDataConfig `yaml:"trivy"`
}

type CVEDataConfig struct {
	Source      string `yaml:"source"`
	FilterByCPE bool   `yaml:"filter_by_cpe"`
	APIKey      string `yaml:"-"`
}

type TrivyDataConfig struct {
	Enabled bool   `yaml:"enabled"`
	Source  string `yaml:"source"`
}

type CLIConfig struct {
	BinaryDownload CLIBinaryDownloadConfig `yaml:"binary_download"`
}

type CLIBinaryDownloadConfig struct {
	RequireAuth bool `yaml:"require_auth"`
}

type WebConfig struct {
	Headers       WebHeadersConfig       `yaml:"headers"`
	Announcements WebAnnouncementsConfig `yaml:"announcements"`
	LLMS          WebLLMSConfig          `yaml:"llms"`
	APIContact    string                 `yaml:"api_contact"`
	Security      WebDisclosureConfig    `yaml:"security"`
}

type WebLLMSConfig struct {
	IncludeEndpoints bool     `yaml:"include_endpoints"`
	IncludeSchemas   bool     `yaml:"include_schemas"`
	CustomSections   []string `yaml:"custom_sections"`
}

type WebDisclosureConfig struct {
	Policy        string   `yaml:"policy"`
	Contact       string   `yaml:"contact"`
	Keyservers    []string `yaml:"keyservers"`
	PublishPGPKey bool     `yaml:"publish_pgp_key"`
}

type WebAnnouncementsConfig struct {
	Enabled  bool                 `yaml:"enabled"`
	Messages []AnnouncementConfig `yaml:"messages"`
}

type AnnouncementConfig struct {
	ID          string `yaml:"id"`
	Type        string `yaml:"type"`
	Title       string `yaml:"title"`
	Message     string `yaml:"message"`
	Start       string `yaml:"start"`
	End         string `yaml:"end"`
	Dismissible bool   `yaml:"dismissible"`
}

type WebHeadersConfig struct {
	ClearSiteData ClearSiteDataConfig `yaml:"clear_site_data"`
}

type ClearSiteDataConfig struct {
	OnTokenRevocation   bool `yaml:"on_token_revocation"`
	OnConsentWithdrawal bool `yaml:"on_consent_withdrawal"`
	ExecutionContexts   bool `yaml:"execution_contexts"`
}

func (c *ClearSiteDataConfig) UnmarshalYAML(node *yaml.Node) error {
	fields := []struct {
		name    string
		current bool
		target  *bool
	}{
		{name: "on_token_revocation", current: c.OnTokenRevocation, target: &c.OnTokenRevocation},
		{name: "on_consent_withdrawal", current: c.OnConsentWithdrawal, target: &c.OnConsentWithdrawal},
		{name: "execution_contexts", current: c.ExecutionContexts, target: &c.ExecutionContexts},
	}
	for _, field := range fields {
		value, err := parseYAMLBool(mappingValue(node, field.name), field.current)
		if err != nil {
			return fmt.Errorf("web.headers.clear_site_data.%s: %w", field.name, err)
		}
		*field.target = value
	}
	return nil
}

type ServerConfig struct {
	Port           string                    `yaml:"port"`
	PIDFile        bool                      `yaml:"pidfile"`
	Database       DatabaseConfig            `yaml:"database"`
	FQDN           string                    `yaml:"fqdn"`
	Address        string                    `yaml:"address"`
	Mode           string                    `yaml:"mode"`
	Daemonize      bool                      `yaml:"daemonize"`
	Debug          bool                      `yaml:"debug"`
	I18N           I18NConfig                `yaml:"i18n"`
	Token          string                    `yaml:"-"`
	Update         UpdateConfig              `yaml:"update"`
	Tokens         TokensConfig              `yaml:"tokens"`
	Metrics        MetricsConfig             `yaml:"metrics"`
	Healthz        HealthzConfig             `yaml:"healthz"`
	SSL            SSLConfig                 `yaml:"ssl"`
	Tor            TorConfig                 `yaml:"tor"`
	I2P            I2PConfig                 `yaml:"i2p"`
	Tracking       TrackingConfig            `yaml:"tracking"`
	GeoIP          GeoIPConfig               `yaml:"geoip"`
	Scheduler      SchedulerConfig           `yaml:"scheduler"`
	RateLimit      RateLimitConfig           `yaml:"rate_limit"`
	Logging        LoggingConfig             `yaml:"logging"`
	TrustedProxies TrustedProxiesConfig      `yaml:"trusted_proxies"`
	Contact        ContactConfig             `yaml:"contact"`
	Pages          PagesConfig               `yaml:"pages"`
	Branding       BrandingConfig            `yaml:"-"`
	SEO            SEOConfig                 `yaml:"-"`
	Notifications  ServerNotificationsConfig `yaml:"notifications"`
	Security       ServerSecurityConfig      `yaml:"security"`
	Backup         BackupConfig              `yaml:"backup"`
	Compliance     ComplianceConfig          `yaml:"compliance"`
	Restore        RestoreConfig             `yaml:"restore"`
	Privacy        PrivacyConfig             `yaml:"privacy"`
}

type BackupConfig struct {
	Encryption BackupEncryptionConfig `yaml:"encryption"`
	Retention  BackupRetentionConfig  `yaml:"retention"`
	Password   string                 `yaml:"-"`
}

type BackupEncryptionConfig struct {
	Enabled bool `yaml:"enabled"`
}

type BackupRetentionConfig struct {
	MaxBackups   int    `yaml:"max_backups"`
	KeepWeekly   int    `yaml:"keep_weekly"`
	KeepMonthly  int    `yaml:"keep_monthly"`
	KeepYearly   int    `yaml:"keep_yearly"`
	MaxTotalSize string `yaml:"max_total_size"`
}

type ComplianceConfig struct {
	Enabled bool `yaml:"enabled"`
}

type RestoreConfig struct {
	MaxArchiveSize  string `yaml:"max_archive_size"`
	MaxExpandedSize string `yaml:"max_expanded_size"`
	MaxFiles        int    `yaml:"max_files"`
	MaxFileSize     string `yaml:"max_file_size"`
	MaxRatio        int64  `yaml:"max_ratio"`
}

func runningInContainer() bool {
	return paths.IsContainer()
}

type TorConfig struct {
	Binary                    string `yaml:"binary"`
	UseNetwork                bool   `yaml:"use_network"`
	MaxCircuits               int    `yaml:"max_circuits"`
	StartupTimeout            int    `yaml:"startup_timeout"`
	CircuitTimeout            int    `yaml:"circuit_timeout"`
	BootstrapTimeout          int    `yaml:"bootstrap_timeout"`
	SafeLogging               bool   `yaml:"safe_logging"`
	MaxStreamsPerCircuit      int    `yaml:"max_streams_per_circuit"`
	CloseCircuitOnStreamLimit bool   `yaml:"close_circuit_on_stream_limit"`
	BandwidthRate             string `yaml:"bandwidth_rate"`
	BandwidthBurst            string `yaml:"bandwidth_burst"`
	MaxMonthlyBandwidth       string `yaml:"max_monthly_bandwidth"`
	NumIntroPoints            int    `yaml:"num_intro_points"`
	VirtualPort               int    `yaml:"virtual_port"`
}

type I2PConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Binary           string `yaml:"binary"`
	SAMAddress       string `yaml:"sam_address"`
	VirtualPort      int    `yaml:"virtual_port"`
	InboundLength    int    `yaml:"inbound_length"`
	OutboundLength   int    `yaml:"outbound_length"`
	InboundQuantity  int    `yaml:"inbound_quantity"`
	OutboundQuantity int    `yaml:"outbound_quantity"`
	SignatureType    int    `yaml:"signature_type"`
	BootstrapTimeout int    `yaml:"bootstrap_timeout"`
}

type SSLConfig struct {
	Enabled     bool                `yaml:"enabled"`
	Cert        string              `yaml:"cert"`
	Key         string              `yaml:"key"`
	MinVersion  string              `yaml:"min_version"`
	LetsEncrypt LetsEncryptSettings `yaml:"letsencrypt"`
}

type LetsEncryptSettings struct {
	Enabled        bool              `yaml:"enabled"`
	Email          string            `yaml:"email"`
	Challenge      string            `yaml:"challenge"`
	Staging        bool              `yaml:"staging"`
	DNSProvider    string            `yaml:"dns_provider"`
	DNSCredentials map[string]string `yaml:"dns_credentials"`
}

type I18NConfig struct {
	DefaultLanguage    string   `yaml:"default_language"`
	AvailableLanguages []string `yaml:"available_languages"`
}

type PrivacyConfig struct {
	Data       PrivacyDataConfig       `yaml:"data"`
	Consent    PrivacyConsentConfig    `yaml:"consent"`
	Cookies    PrivacyCookiesConfig    `yaml:"cookies"`
	Retention  PrivacyRetentionConfig  `yaml:"retention"`
	ThirdParty PrivacyThirdPartyConfig `yaml:"third_party"`
}

type PrivacyDataConfig struct {
	Sold           bool                   `yaml:"sold"`
	StoredOnServer bool                   `yaml:"stored_on_server"`
	Sharing        []PrivacySharingConfig `yaml:"sharing"`
}

type PrivacySharingConfig struct {
	Condition string `yaml:"condition"`
	When      string `yaml:"when"`
	Data      string `yaml:"data"`
}

type PrivacyConsentConfig struct {
	ShowUntilAcknowledged bool                        `yaml:"show_until_acknowledged"`
	Message               string                      `yaml:"message"`
	MessageIfSold         string                      `yaml:"message_if_sold"`
	Policy                PrivacyConsentPolicyConfig  `yaml:"policy"`
	Buttons               PrivacyConsentButtonsConfig `yaml:"buttons"`
	Position              string                      `yaml:"position"`
	ShowPreferences       bool                        `yaml:"show_preferences"`
	PreferencesText       string                      `yaml:"preferences_text"`
}

type PrivacyConsentPolicyConfig struct {
	URL  string `yaml:"url"`
	Text string `yaml:"text"`
}

type PrivacyConsentButtonsConfig struct {
	Decline string `yaml:"decline"`
	Accept  string `yaml:"accept"`
}

type PrivacyCookiesConfig struct {
	Essential   PrivacyCookieConfig `yaml:"essential"`
	Preferences PrivacyCookieConfig `yaml:"preferences"`
	Analytics   PrivacyCookieConfig `yaml:"analytics"`
}

type PrivacyCookieConfig struct {
	Enabled                  bool   `yaml:"enabled"`
	Description              string `yaml:"description"`
	DescriptionSuffixSold    string `yaml:"description_suffix_sold"`
	DescriptionSuffixNotSold string `yaml:"description_suffix_not_sold"`
}

type PrivacyContent struct {
	DataCollection  string
	DataUsage       string
	DataUsageIfSold string
	DataSecurity    string
}

type PrivacyRetentionConfig struct {
	Period            string `yaml:"period"`
	ExportAvailable   bool   `yaml:"export_available"`
	DeletionAvailable bool   `yaml:"deletion_available"`
}

type PrivacyThirdPartyConfig struct {
	Services []PrivacyThirdPartyService `yaml:"services"`
}

type PrivacyThirdPartyService struct {
	Name      string `yaml:"name"`
	Purpose   string `yaml:"purpose"`
	DataSent  string `yaml:"data_sent"`
	PolicyURL string `yaml:"policy_url"`
}

func (p PrivacyConfig) IsCCPAApplicable() bool {
	return p.Data.Sold
}

func (p PrivacyConfig) BuiltInContent(languages ...string) PrivacyContent {
	lang := "en"
	if len(languages) > 0 {
		lang = languages[0]
	}
	return PrivacyContent{
		DataCollection:  i18n.Translate(lang, "privacy.data_collection_content"),
		DataUsage:       i18n.Translate(lang, "privacy.data_usage_content"),
		DataUsageIfSold: i18n.Translate(lang, "privacy.data_usage_if_sold_content"),
		DataSecurity:    i18n.Translate(lang, "privacy.data_security_content"),
	}
}

func (p PrivacyConfig) GetDataUsageContent(languages ...string) string {
	lang := "en"
	if len(languages) > 0 {
		lang = languages[0]
	}
	content := p.BuiltInContent(lang)
	if p.Data.Sold {
		return content.DataUsageIfSold
	}
	return content.DataUsage
}

func (p PrivacyConfig) GetAnalyticsDescription() string {
	analytics := p.Cookies.Analytics
	if p.Data.Sold {
		return strings.TrimSpace(analytics.Description + " " + analytics.DescriptionSuffixSold)
	}
	return strings.TrimSpace(analytics.Description + " " + analytics.DescriptionSuffixNotSold)
}

func (p PrivacyConfig) GetConsentMessage() string {
	if p.Data.Sold {
		return p.Consent.MessageIfSold
	}
	return p.Consent.Message
}

type ContactConfig struct {
	General  PublicContactConfig `yaml:"general"`
	Abuse    PublicContactConfig `yaml:"abuse"`
	Security PublicContactConfig `yaml:"security"`
	Admin    PublicContactConfig `yaml:"admin"`
	DPO      PublicContactConfig `yaml:"dpo"`
}

type PagesConfig struct {
	Contact ContactPageConfig `yaml:"contact"`
	Help    HelpPagesConfig   `yaml:"help"`
}

type HelpPagesConfig struct {
	Content string `yaml:"content"`
	MCP     string `yaml:"mcp"`
}

type BrandingConfig struct {
	Title       string                       `yaml:"title"`
	Tagline     string                       `yaml:"tagline"`
	Description string                       `yaml:"description"`
	Favicon     string                       `yaml:"favicon"`
	Logo        string                       `yaml:"logo"`
	Content     map[string]PageContentConfig `yaml:"content"`
}

type SEOConfig struct {
	Keywords       []string           `yaml:"keywords"`
	Author         string             `yaml:"author"`
	OpenGraphImage string             `yaml:"og_image"`
	TwitterHandle  string             `yaml:"twitter_handle"`
	TitleSeparator string             `yaml:"title_separator"`
	MetaTags       map[string]string  `yaml:"meta_tags"`
	Canonical      string             `yaml:"canonical"`
	NoIndex        bool               `yaml:"noindex"`
	OpenGraph      SEOOpenGraphConfig `yaml:"opengraph"`
	Twitter        SEOTwitterConfig   `yaml:"twitter"`
}

type SEOOpenGraphConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Type     string `yaml:"type"`
	SiteName string `yaml:"site_name"`
}

type SEOTwitterConfig struct {
	Enabled bool   `yaml:"enabled"`
	Card    string `yaml:"card"`
	Creator string `yaml:"creator"`
}

type PageContentConfig struct {
	Format string `yaml:"format"`
	Body   any    `yaml:"body"`
}

type ContactPageConfig struct {
	Enabled          bool                  `yaml:"enabled"`
	Captcha          string                `yaml:"captcha"`
	CaptchaSiteKey   string                `yaml:"captcha_site_key"`
	CaptchaSecretKey string                `yaml:"-"`
	Antispam         ContactAntispamConfig `yaml:"antispam"`
	SuccessMessage   string                `yaml:"success_message"`
}

type ContactAntispamConfig struct {
	Honeypot             bool     `yaml:"honeypot"`
	MinFillSeconds       int      `yaml:"min_fill_seconds"`
	MaxAgeSeconds        int      `yaml:"max_age_seconds"`
	PerIP                int      `yaml:"per_ip"`
	PerEmail             int      `yaml:"per_email"`
	Global               int      `yaml:"global"`
	Window               string   `yaml:"window"`
	MaxLinks             int      `yaml:"max_links"`
	RejectMarkup         bool     `yaml:"reject_markup"`
	BlockedWords         []string `yaml:"blocked_words"`
	BlockedEmailDomains  []string `yaml:"blocked_email_domains"`
	Strikes              int      `yaml:"strikes"`
	BanHours             int      `yaml:"ban_hours"`
	ReviewMinSeconds     int      `yaml:"review_min_seconds"`
	ReviewMaxSeconds     int      `yaml:"review_max_seconds"`
	DuplicateWindowHours int      `yaml:"duplicate_window_hours"`
}

type ServerNotificationsConfig struct {
	WebUI   NotificationWebUIConfig       `yaml:"webui"`
	Email   ServerNotificationEmailConfig `yaml:"email"`
	DiskLow DiskLowNotificationConfig     `yaml:"disk_low"`
}
type ServerNotificationEmailConfig struct {
	SMTP    SMTPConfig                    `yaml:"smtp"`
	From    NotificationEmailFromConfig   `yaml:"from"`
	ReplyTo string                        `yaml:"reply_to"`
	Events  NotificationEmailEventsConfig `yaml:"events"`
	Enabled bool                          `yaml:"-"`
}
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"-"`
	TLS      string `yaml:"tls"`
}

type NotificationEmailFromConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type NotificationEmailEventsConfig struct {
	Startup          bool `yaml:"startup"`
	Shutdown         bool `yaml:"shutdown"`
	BackupComplete   bool `yaml:"backup_complete"`
	BackupFailed     bool `yaml:"backup_failed"`
	SSLExpiring      bool `yaml:"ssl_expiring"`
	SSLRenewed       bool `yaml:"ssl_renewed"`
	SSLRenewalFailed bool `yaml:"ssl_renewal_failed"`
	SecurityAlert    bool `yaml:"security_alert"`
	SchedulerError   bool `yaml:"scheduler_error"`
	UpdateAvailable  bool `yaml:"update_available"`
	UpdateInstalled  bool `yaml:"update_installed"`
}

type NotificationWebUIConfig struct {
	Position        string `yaml:"position"`
	Duration        int    `yaml:"duration"`
	WarningDuration int    `yaml:"warning_duration"`
}

type DiskLowNotificationConfig struct {
	Enabled          bool   `yaml:"enabled"`
	ThresholdPercent int    `yaml:"threshold_percent"`
	CheckInterval    string `yaml:"check_interval"`
	Repeat           string `yaml:"repeat"`
}

type PublicContactConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type DatabaseConfig struct {
	Driver string `yaml:"driver"`
	URL    string `yaml:"url"`
}

type UpdateConfig struct {
	Branch        string `yaml:"branch"`
	AutoInstall   bool   `yaml:"auto_install"`
	DeferDays     int    `yaml:"defer_days"`
	CLIMinVersion string `yaml:"cli_min_version"`
}

type TrackingConfig struct {
	Type string `yaml:"type"`
	ID   string `yaml:"id"`
	URL  string `yaml:"url"`
}

type TokensConfig struct {
	MaxLinked     int    `yaml:"max_linked"`
	CookieMaxAge  string `yaml:"cookie_max_age"`
	IdlePurgeDays int    `yaml:"idle_purge_days"`
}

func (c TokensConfig) CookieMaxAgeSeconds() int {
	seconds, err := parseTokenCookieMaxAge(c.CookieMaxAge)
	if err != nil {
		return 365 * 24 * 60 * 60
	}
	return seconds
}

type RateLimitConfig struct {
	Read         RateLimitRuleConfig `yaml:"read"`
	Write        RateLimitRuleConfig `yaml:"write"`
	Health       RateLimitRuleConfig `yaml:"health"`
	Strict       RateLimitRuleConfig `yaml:"strict"`
	AuthFailures RateLimitRuleConfig `yaml:"auth_failures"`
	GlobalBurst  int                 `yaml:"global_burst"`
}

type GeoIPConfig struct {
	Enabled        bool                `yaml:"enabled"`
	DenyCountries  []string            `yaml:"deny_countries"`
	AllowCountries []string            `yaml:"allow_countries"`
	Presets        map[string][]string `yaml:"presets"`
}

type SchedulerConfig struct {
	Timezone      string                         `yaml:"timezone"`
	CatchUpWindow string                         `yaml:"catch_up_window"`
	Tasks         map[string]SchedulerTaskConfig `yaml:"tasks"`
}

type SchedulerTaskConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Schedule    string `yaml:"schedule"`
	RetryOnFail bool   `yaml:"retry_on_fail"`
	RetryDelay  string `yaml:"retry_delay"`
}

func (c *MetricsConfig) UnmarshalYAML(node *yaml.Node) error {
	fields := metricsConfigFields{
		Root: c.Root, Auth: MetricsAuthConfig{AllowUnauthenticated: c.Auth.AllowUnauthenticated},
		IncludeSystem: c.IncludeSystem, IncludeRuntime: c.IncludeRuntime, Loki: c.Loki,
		DurationBuckets: c.DurationBuckets, SizeBuckets: c.SizeBuckets,
	}
	if err := node.Decode(&fields); err != nil {
		return err
	}
	enabled, err := parseYAMLBool(mappingValue(node, "enabled"), c.Enabled)
	if err != nil {
		return fmt.Errorf("server.metrics.enabled: %w", err)
	}
	c.Enabled = enabled
	rootNode := mappingValue(node, "root")
	rootEnabled, err := parseYAMLBool(mappingValue(rootNode, "enabled"), c.Root.Enabled)
	if err != nil {
		return fmt.Errorf("server.metrics.root.enabled: %w", err)
	}
	allowUnauthenticated, err := parseYAMLBool(mappingValue(mappingValue(node, "auth"), "allow_unauthenticated"), c.Auth.AllowUnauthenticated)
	if err != nil {
		return fmt.Errorf("server.metrics.auth.allow_unauthenticated: %w", err)
	}
	c.Root = fields.Root
	c.Root.Enabled = rootEnabled
	c.Auth.AllowUnauthenticated = allowUnauthenticated
	c.IncludeSystem = fields.IncludeSystem
	c.IncludeRuntime = fields.IncludeRuntime
	c.Loki = fields.Loki
	c.DurationBuckets = fields.DurationBuckets
	c.SizeBuckets = fields.SizeBuckets
	return nil
}

type metricsConfigFields struct {
	Root            MetricsRootConfig `yaml:"root"`
	Auth            MetricsAuthConfig `yaml:"auth"`
	IncludeSystem   bool              `yaml:"include_system"`
	IncludeRuntime  bool              `yaml:"include_runtime"`
	Loki            MetricsLokiConfig `yaml:"loki"`
	DurationBuckets []float64         `yaml:"duration_buckets"`
	SizeBuckets     []float64         `yaml:"size_buckets"`
}

func (c *NotificationsConfig) UnmarshalYAML(node *yaml.Node) error {
	fields := notificationsConfigFields{Announcements: c.Announcements}
	if err := node.Decode(&fields); err != nil {
		return err
	}
	enabled, err := parseYAMLBool(mappingValue(node, "enabled"), c.Enabled)
	if err != nil {
		return fmt.Errorf("web-ui.notifications.enabled: %w", err)
	}
	c.Enabled = enabled
	c.Announcements = fields.Announcements
	return nil
}

type notificationsConfigFields struct {
	Announcements []string `yaml:"announcements"`
}

type RateLimitRuleConfig struct {
	Requests int `yaml:"requests"`
	Window   int `yaml:"window"`
}

type TrustedProxiesConfig struct {
	Additional []string `yaml:"additional"`
}

type MetricsConfig struct {
	Enabled         bool              `yaml:"enabled"`
	Root            MetricsRootConfig `yaml:"root"`
	Auth            MetricsAuthConfig `yaml:"auth"`
	IncludeSystem   bool              `yaml:"include_system"`
	IncludeRuntime  bool              `yaml:"include_runtime"`
	Loki            MetricsLokiConfig `yaml:"loki"`
	DurationBuckets []float64         `yaml:"duration_buckets"`
	SizeBuckets     []float64         `yaml:"size_buckets"`
}

type MetricsRootConfig struct {
	Enabled bool `yaml:"enabled"`
}

type HealthzConfig struct {
	Root HealthzRootConfig `yaml:"root"`
}

type HealthzRootConfig struct {
	Enabled bool `yaml:"enabled"`
}

type MetricsAuthConfig struct {
	AllowUnauthenticated bool              `yaml:"allow_unauthenticated"`
	Tokens               MetricsAuthTokens `yaml:"-"`
}

type MetricsAuthTokens struct {
	Prometheus string `yaml:"prometheus"`
	Grafana    string `yaml:"grafana"`
	Loki       string `yaml:"loki"`
}

type MetricsLokiConfig struct {
	MaxEntries int           `yaml:"max_entries"`
	MaxAge     time.Duration `yaml:"max_age"`
}

type LoggingConfig struct {
	AccessFormat string `yaml:"access_format"`
	Level        string `yaml:"level"`
}

type WebUIConfig struct {
	Theme         string              `yaml:"theme"`
	Accent        string              `yaml:"-"`
	Notifications NotificationsConfig `yaml:"notifications"`
}

type NotificationsConfig struct {
	Enabled       bool     `yaml:"enabled"`
	Announcements []string `yaml:"announcements"`
}

type WebRobotsConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type WebSecurityConfig struct {
	CORS string `yaml:"cors"`
}

func defaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Timezone: "America/New_York", CatchUpWindow: "1h",
		Tasks: map[string]SchedulerTaskConfig{
			"ssl_renewal":      {Enabled: true, Schedule: "0 3 * * *"},
			"geoip_update":     {Enabled: true, Schedule: "0 3 * * 0"},
			"blocklist_update": {Enabled: true, Schedule: "0 4 * * *", RetryOnFail: true, RetryDelay: "1h"},
			"security_cleanup": {Enabled: true, Schedule: "30 3 * * *"},
			"cve_update":       {Enabled: true, Schedule: "0 5 * * *", RetryOnFail: true, RetryDelay: "1h"},
			"update_check":     {Enabled: true, Schedule: "0 6 * * *"},
			"token_cleanup":    {Enabled: true, Schedule: "@every 15m"},
			"log_rotation":     {Enabled: true, Schedule: "0 0 * * *"},
			"backup_daily":     {Enabled: true, Schedule: "0 2 * * *"},
			"backup_hourly":    {Enabled: false, Schedule: "@hourly"},
			"disk_check":       {Enabled: true, Schedule: "@every 5m"},
			"healthcheck_self": {Enabled: true, Schedule: "@every 5m"},
			"tor_health":       {Enabled: true, Schedule: "@every 10m"},
			"i2p_health":       {Enabled: true, Schedule: "@every 10m"},
		},
	}
}

func defaultPrivacyConfig() PrivacyConfig {
	return PrivacyConfig{
		Data: PrivacyDataConfig{
			Sold: false, StoredOnServer: true,
			Sharing: []PrivacySharingConfig{
				{Condition: "analytics", When: "Tracking configured (server.tracking.type set) AND user consents", Data: "Anonymized: page views, browser type, country"},
				{Condition: "email", When: "SMTP configured for sending emails", Data: "Email address, message content"},
				{Condition: "user_initiated", When: "User explicitly shares content (social buttons, exports)", Data: "Whatever user chooses to share"},
			},
		},
		Consent: PrivacyConsentConfig{
			ShowUntilAcknowledged: true,
			Message:               "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data is stored on our servers and is never sold.",
			MessageIfSold:         "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data may be shared with or sold to third parties as described in our Privacy Policy.",
			Policy:                PrivacyConsentPolicyConfig{URL: "/server/privacy", Text: "Privacy Policy"},
			Buttons:               PrivacyConsentButtonsConfig{Decline: "Decline", Accept: "I Agree"},
			Position:              "bottom", ShowPreferences: true, PreferencesText: "Manage Preferences",
		},
		Cookies: PrivacyCookiesConfig{
			Essential:   PrivacyCookieConfig{Enabled: true, Description: "Required for the site to function. Includes security tokens (CSRF) and site preferences. These cookies are strictly necessary and cannot be disabled."},
			Preferences: PrivacyCookieConfig{Enabled: true, Description: "Remember your settings such as theme (dark/light), language, and UI preferences. Disabling will reset to defaults on each visit."},
			Analytics:   PrivacyCookieConfig{Enabled: true, Description: "Help us understand how visitors use our site to improve the experience.", DescriptionSuffixNotSold: "Analytics data is anonymized and never sold.", DescriptionSuffixSold: "Analytics data may be shared with third parties."},
		},
		Retention: PrivacyRetentionConfig{
			Period:          "Resources you create are kept until they expire or you delete them with your owner token. Preferences are stored only in cookies in your browser. Operational logs are kept for up to 30 days. Anonymized analytics data may be retained for up to 12 months.",
			ExportAvailable: true, DeletionAvailable: true,
		},
		ThirdParty: PrivacyThirdPartyConfig{Services: []PrivacyThirdPartyService{}},
	}
}

func DefaultMCPHelpProjectContent() string {
	return "## Country data tools\nUse `getCountries` to list and filter country records, `getCountriesCode` to look up an ISO alpha-2, alpha-3, or numeric code, and `getCountriesSearch` to search supported fields. Use `getCoordinates` or `postCoordinates` to find the nearest country, `getNearby` to find country centroids within a radius, and `getStats` or `getCount` for dataset summaries. Follow each tool's OpenAPI input schema and pagination or radius limits.\n\n## Example prompts\n- Find countries in South America that use the peso.\n- Which country is nearest latitude 40.7 and longitude -74.0? Include its distance.\n- List the countries that border Japan and show their ISO codes.\n\nThis server exposes tools only; it has no MCP resources or prompts."
}

func DefaultConfig() *Config {
	return &Config{
		Data: DataConfig{
			SecurityDir: "{data_dir}/security",
			CVE:         CVEDataConfig{Source: "https://services.nvd.nist.gov/rest/json/cves/2.0"},
			Trivy:       TrivyDataConfig{Source: "ghcr.io/aquasecurity/trivy-db:2"},
		},
		CLI: CLIConfig{BinaryDownload: CLIBinaryDownloadConfig{RequireAuth: false}},
		Web: WebConfig{
			Headers: WebHeadersConfig{ClearSiteData: ClearSiteDataConfig{
				OnTokenRevocation: true, OnConsentWithdrawal: true,
			}},
			Announcements: WebAnnouncementsConfig{Enabled: true, Messages: []AnnouncementConfig{}},
			LLMS:          WebLLMSConfig{IncludeEndpoints: true, CustomSections: []string{}},
			Security:      WebDisclosureConfig{Keyservers: []string{}},
		},
		Server: ServerConfig{
			Port:     "8080",
			PIDFile:  true,
			Database: DatabaseConfig{Driver: "sqlite", URL: "{data_dir}/db/server.db"},
			FQDN:     "localhost",
			SSL: SSLConfig{MinVersion: "TLS1.2", LetsEncrypt: LetsEncryptSettings{
				Challenge: "http-01", DNSCredentials: map[string]string{},
			}},
			Tor: TorConfig{MaxCircuits: 32, StartupTimeout: 60, CircuitTimeout: 60, BootstrapTimeout: 180, SafeLogging: true, MaxStreamsPerCircuit: 100, CloseCircuitOnStreamLimit: true, BandwidthRate: "1 MB", BandwidthBurst: "2 MB", MaxMonthlyBandwidth: "100 GB", NumIntroPoints: 3, VirtualPort: 80},
			I2P: I2PConfig{Enabled: runningInContainer(), SAMAddress: "127.0.0.1:7656", VirtualPort: 80, InboundLength: 3, OutboundLength: 3, InboundQuantity: 5, OutboundQuantity: 5, SignatureType: 7, BootstrapTimeout: 300},
			Security: ServerSecurityConfig{
				Blocklists: BlocklistsConfig{Enabled: true, ExtraSources: []BlocklistSourceConfig{}},
				Reports:    SecurityReportsConfig{RetentionDays: 365},
			},
			Backup:  BackupConfig{Retention: BackupRetentionConfig{MaxBackups: 1, MaxTotalSize: "10%"}},
			Restore: RestoreConfig{MaxArchiveSize: "1G", MaxExpandedSize: "4G", MaxFiles: 10000, MaxFileSize: "512M", MaxRatio: 100},
			Pages: PagesConfig{
				Contact: ContactPageConfig{Enabled: true, Captcha: "simple", Antispam: ContactAntispamConfig{Honeypot: true, MinFillSeconds: 3, MaxAgeSeconds: 43200, PerIP: 5, PerEmail: 3, Global: 60, Window: "1h", MaxLinks: 10, RejectMarkup: true, BlockedWords: []string{}, BlockedEmailDomains: []string{}, Strikes: 3, BanHours: 24, ReviewMinSeconds: 2, ReviewMaxSeconds: 900, DuplicateWindowHours: 24}, SuccessMessage: "Thank you for your report."},
				Help:    HelpPagesConfig{MCP: DefaultMCPHelpProjectContent()},
			},
			Address:        "::",
			TrustedProxies: TrustedProxiesConfig{Additional: []string{}},
			Mode:           "production",
			I18N:           I18NConfig{DefaultLanguage: "en", AvailableLanguages: i18n.Languages()},
			Notifications: ServerNotificationsConfig{
				WebUI:   NotificationWebUIConfig{Position: "top-right", Duration: 3, WarningDuration: 5},
				Email:   ServerNotificationEmailConfig{SMTP: SMTPConfig{Port: 587, TLS: "auto"}, Events: NotificationEmailEventsConfig{BackupFailed: true, SSLExpiring: true, SSLRenewalFailed: true, SecurityAlert: true, SchedulerError: true, UpdateInstalled: true}},
				DiskLow: DiskLowNotificationConfig{Enabled: true, ThresholdPercent: 85, CheckInterval: "5m", Repeat: "24h"},
			},
			Tokens:   TokensConfig{MaxLinked: 25, CookieMaxAge: "1y", IdlePurgeDays: 0},
			Privacy:  defaultPrivacyConfig(),
			Branding: BrandingConfig{Title: "Countries API", Tagline: "Country reference data for applications and developers", Description: "A full-stack country reference service with 249 country records, REST, GraphQL and a web interface.", Content: map[string]PageContentConfig{}},
			SEO:      SEOConfig{TitleSeparator: " - ", MetaTags: map[string]string{}, OpenGraph: SEOOpenGraphConfig{Enabled: true, Type: "website"}, Twitter: SEOTwitterConfig{Enabled: true, Card: "summary_large_image"}},
			Update:   UpdateConfig{Branch: "stable"},
			Metrics: MetricsConfig{
				Enabled:         true,
				Root:            MetricsRootConfig{Enabled: true},
				IncludeSystem:   true,
				IncludeRuntime:  true,
				Loki:            MetricsLokiConfig{MaxEntries: 1000, MaxAge: time.Hour},
				DurationBuckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
				SizeBuckets:     []float64{100, 1000, 10000, 100000, 1000000, 10000000},
			},
			Healthz:   HealthzConfig{Root: HealthzRootConfig{Enabled: false}},
			GeoIP:     GeoIPConfig{Enabled: true, DenyCountries: []string{}, AllowCountries: []string{}, Presets: map[string][]string{}},
			Scheduler: defaultSchedulerConfig(),
			RateLimit: RateLimitConfig{
				Read:         RateLimitRuleConfig{Requests: 120, Window: 60},
				Write:        RateLimitRuleConfig{Requests: 10, Window: 60},
				Health:       RateLimitRuleConfig{Requests: 120, Window: 60},
				Strict:       RateLimitRuleConfig{Requests: 10, Window: 900},
				AuthFailures: RateLimitRuleConfig{Requests: 5, Window: 900},
				GlobalBurst:  240,
			},
			Logging: LoggingConfig{
				AccessFormat: "apache",
				Level:        "info",
			},
		},
		WebUI: WebUIConfig{
			Theme: "dark",
			Notifications: NotificationsConfig{
				Enabled:       true,
				Announcements: []string{},
			},
		},
		WebRobots: WebRobotsConfig{
			Allow: []string{"/", "/api"},
			Deny:  []string{"/debug"},
		},
		WebSecurity: WebSecurityConfig{
			CORS: "*",
		},
	}
}

// migrateYamlToYml migrates config from .yaml to .yml extension
func migrateYamlToYml(ymlPath string) {
	if !strings.HasSuffix(ymlPath, ".yml") {
		return
	}

	yamlPath := ymlPath[:len(ymlPath)-4] + ".yaml" // Replace .yml with .yaml

	// Check for regular files without following an operator-controlled symlink.
	if yamlInfo, err := os.Lstat(yamlPath); err == nil && yamlInfo.Mode().IsRegular() {
		if _, err := os.Lstat(ymlPath); os.IsNotExist(err) {
			// Migrate .yaml to .yml
			if err := os.Rename(yamlPath, ymlPath); err != nil {
				log.Printf("Warning: failed to migrate %s to %s: %v", yamlPath, ymlPath, err)
			} else {
				log.Printf("Migrated config from %s to %s", yamlPath, ymlPath)
			}
		}
	}
}

func Load(path string) (*Config, error) {
	loadPath := path
	if _, statErr := os.Stat(loadPath); os.IsNotExist(statErr) && strings.HasSuffix(loadPath, ".yml") {
		legacyPath := strings.TrimSuffix(loadPath, ".yml") + ".yaml"
		if _, legacyErr := os.Stat(legacyPath); legacyErr == nil {
			loadPath = legacyPath
		}
	}

	cfg := DefaultConfig()
	cfg.ConfigDir = filepath.Dir(path)
	if _, err := os.Stat(loadPath); os.IsNotExist(err) {
		// Select a stable first-run listener before persisting the config.
		cfg.Server.Port, err = firstRunServerPort()
		if err != nil {
			return cfg, fmt.Errorf("select default server port: %w", err)
		}
		if err := saveNodeConfiguration(path, cfg); err != nil {
			return cfg, fmt.Errorf("create default configuration: %w", err)
		}
	} else if err != nil {
		log.Printf("WARN: cannot inspect server configuration %s: %v", path, err)
	}

	defaultNode, err := configYAMLNode(cfg)
	if err != nil {
		return cfg, fmt.Errorf("encode default configuration: %w", err)
	}
	mergedNode := cloneYAMLConfigNode(defaultNode)
	sources, discoveryIssues := readYAMLConfigSources(loadPath)
	portConfigured := false
	for _, issue := range discoveryIssues {
		log.Printf("WARN: %s", issue.Error())
	}
	for _, source := range sources {
		sourceNode := source.Node
		if source.Err != nil {
			if cached := cachedYAMLConfigSource(source.Path); cached != nil {
				log.Printf("WARN: ignoring invalid configuration source %s and using its last good copy: %v", source.Path, source.Err)
				sourceNode = cached
			} else {
				log.Printf("WARN: ignoring invalid configuration source %s: %v", source.Path, source.Err)
				continue
			}
		}
		if sourceNode == nil {
			continue
		}
		sourceNode = normalizeYAMLConfigSource(sourceNode)
		serverSource := yamlConfigMappingValues(sourceNode)["server"]
		portSource := yamlConfigMappingValues(serverSource)["port"]
		if portSource != nil && portSource.Tag != "!!null" && strings.TrimSpace(portSource.Value) != "" {
			portConfigured = true
		}
		candidate := cloneYAMLConfigNode(mergedNode)
		for _, issue := range mergeYAMLConfigNodes(candidate, defaultNode, sourceNode, source.Path) {
			log.Printf("WARN: %s", issue.Error())
		}
		candidateData, marshalErr := yaml.Marshal(candidate)
		if marshalErr != nil {
			log.Printf("WARN: ignoring configuration source %s: %v", source.Path, marshalErr)
			continue
		}
		validated := DefaultConfig()
		if unmarshalErr := unmarshalConfigYAML(candidateData, validated); unmarshalErr != nil {
			if keyPath := configErrorPath(unmarshalErr); keyPath != "" {
				line := yamlConfigLineAtPath(sourceNode, strings.Split(keyPath, "."))
				resetYAMLConfigPath(candidate, defaultNode, strings.Split(keyPath, "."))
				log.Printf("WARN: %s:%d: %s has an invalid value; using the default", source.Path, line, keyPath)
				candidateData, marshalErr = yaml.Marshal(candidate)
				if marshalErr == nil {
					validated = DefaultConfig()
					unmarshalErr = unmarshalConfigYAML(candidateData, validated)
				}
			}
			if unmarshalErr == nil {
				rememberYAMLConfigSource(source.Path, source.Node)
			} else if cached := cachedYAMLConfigSource(source.Path); cached != nil && source.Err == nil {
				log.Printf("WARN: ignoring invalid values in configuration source %s and using its last good copy: %v", source.Path, unmarshalErr)
				candidate = cloneYAMLConfigNode(mergedNode)
				for _, issue := range mergeYAMLConfigNodes(candidate, defaultNode, normalizeYAMLConfigSource(cached), source.Path) {
					log.Printf("WARN: %s", issue.Error())
				}
			} else {
				log.Printf("WARN: ignoring configuration source %s with invalid values: %v", source.Path, unmarshalErr)
				continue
			}
		} else if source.Err == nil {
			rememberYAMLConfigSource(source.Path, source.Node)
		}
		mergedNode = candidate
		log.Printf("INFO: loaded configuration source %s", source.Path)
	}

	data, err := yaml.Marshal(mergedNode)
	if err != nil {
		return cfg, fmt.Errorf("encode merged configuration: %w", err)
	}
	if err := unmarshalConfigYAML(data, cfg); err != nil {
		log.Printf("WARN: merged configuration is invalid; using built-in defaults: %v", err)
		cfg = DefaultConfig()
		cfg.ConfigDir = filepath.Dir(path)
	}
	if !portConfigured {
		cfg.Server.Port, err = firstRunServerPort()
		if err != nil {
			return cfg, fmt.Errorf("select default server port: %w", err)
		}
		if err := persistServerPort(loadPath, cfg.Server.Port); err != nil {
			log.Printf("WARN: cannot persist generated server port in %s: %v", loadPath, err)
		}
	}
	if strings.TrimSpace(cfg.Server.Port) == "" {
		cfg.Server.Port, err = firstRunServerPort()
		if err != nil {
			return cfg, fmt.Errorf("select default server port: %w", err)
		}
		if err := persistServerPort(loadPath, cfg.Server.Port); err != nil {
			log.Printf("WARN: cannot persist generated server port in %s: %v", loadPath, err)
		}
	}
	if err := loadSecretSettings(loadPath, cfg); err != nil {
		log.Printf("WARN: cannot load secrets from %s: %v", filepath.Join(filepath.Dir(path), "secrets.yml"), err)
	}
	for name, entry := range cfg.Server.Branding.Content {
		if contentErr := validateBrandingContent(name, entry); contentErr != nil {
			log.Printf("WARN: ignoring invalid server.branding.content.%s: %v", name, contentErr)
			delete(cfg.Server.Branding.Content, name)
		}
	}
	validateRuntimeDataConfig(cfg)
	validateRateLimitConfig(cfg)
	validateOverlayConfig(cfg)
	validateTokensConfig(cfg)
	validateUpdateConfig(cfg)
	validateGeoIPConfig(cfg)
	if err := ValidateTracking(&cfg.Server.Tracking); err != nil {
		log.Printf("WARN: invalid server.tracking configuration; using defaults: %v", err)
		cfg.Server.Tracking = DefaultConfig().Server.Tracking
	}
	if err := validateSecurityConfig(cfg); err != nil {
		log.Printf("WARN: invalid server.security configuration; using default allowlist: %v", err)
		cfg.Server.Security.Allowlist = DefaultConfig().Server.Security.Allowlist
	}
	validateSchedulerConfig(cfg)
	validateI18NConfig(cfg)
	if err := validateNotificationConfig(cfg); err != nil {
		log.Printf("WARN: invalid server.notifications configuration; resetting invalid values: %v", err)
		resetInvalidNotificationConfig(cfg, err)
		if retryErr := validateNotificationConfig(cfg); retryErr != nil {
			log.Printf("WARN: invalid server.notifications configuration after reset; using defaults: %v", retryErr)
			defaults := DefaultConfig().Server.Notifications
			defaults.Email.SMTP.Password = cfg.Server.Notifications.Email.SMTP.Password
			cfg.Server.Notifications = defaults
		}
	}
	if err := validateAnnouncementsConfig(cfg); err != nil {
		log.Printf("WARN: invalid web.announcements configuration; using defaults: %v", err)
		cfg.Web.Announcements = DefaultConfig().Web.Announcements
	}
	if err := validateContactConfig(cfg); err != nil {
		log.Printf("WARN: invalid server.pages.contact CAPTCHA configuration; using defaults: %v", err)
		defaults := DefaultConfig().Server.Pages.Contact
		cfg.Server.Pages.Contact.Captcha = defaults.Captcha
		cfg.Server.Pages.Contact.CaptchaSiteKey = defaults.CaptchaSiteKey
		cfg.Server.Pages.Contact.CaptchaSecretKey = defaults.CaptchaSecretKey
	}
	return cfg, nil
}

var yamlConfigSourceCache = struct {
	sync.RWMutex
	sources map[string]*yaml.Node
}{sources: make(map[string]*yaml.Node)}

func cachedYAMLConfigSource(path string) *yaml.Node {
	yamlConfigSourceCache.RLock()
	defer yamlConfigSourceCache.RUnlock()
	return cloneYAMLConfigNode(yamlConfigSourceCache.sources[path])
}

func rememberYAMLConfigSource(path string, node *yaml.Node) {
	yamlConfigSourceCache.Lock()
	defer yamlConfigSourceCache.Unlock()
	yamlConfigSourceCache.sources[path] = cloneYAMLConfigNode(node)
}

func configYAMLNode(cfg *Config) (*yaml.Node, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("configuration must encode as a YAML mapping")
	}
	root := document.Content[0]
	server := yamlConfigMappingValues(root)["server"]
	if server == nil || server.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("server configuration must encode as a YAML mapping")
	}
	branding, err := yamlConfigNodeForValue(cfg.Server.Branding)
	if err != nil {
		return nil, err
	}
	seo, err := yamlConfigNodeForValue(cfg.Server.SEO)
	if err != nil {
		return nil, err
	}
	setYAMLConfigKey(server, "branding", branding)
	setYAMLConfigKey(server, "seo", seo)
	webUI, err := yamlConfigNodeForValue(struct {
		Theme         string              `yaml:"theme"`
		Accent        string              `yaml:"accent"`
		Notifications NotificationsConfig `yaml:"notifications"`
	}{Theme: cfg.WebUI.Theme, Accent: cfg.WebUI.Accent, Notifications: cfg.WebUI.Notifications})
	if err != nil {
		return nil, err
	}
	web := yamlConfigMappingValues(root)["web"]
	if web == nil || web.Kind != yaml.MappingNode {
		web = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setYAMLConfigKey(root, "web", web)
	}
	setYAMLConfigKey(web, "ui", webUI)
	removeYAMLConfigKey(root, "web-ui")
	return root, nil
}

func yamlConfigNodeForValue(value any) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("value must encode as one YAML document")
	}
	return document.Content[0], nil
}

func normalizeYAMLConfigSource(node *yaml.Node) *yaml.Node {
	result := cloneYAMLConfigNode(node)
	if result == nil || result.Kind != yaml.MappingNode {
		return result
	}
	legacy := yamlConfigMappingValues(result)["web-ui"]
	if legacy != nil {
		web := yamlConfigMappingValues(result)["web"]
		if web == nil || web.Kind != yaml.MappingNode {
			web = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setYAMLConfigKey(result, "web", web)
		}
		if yamlConfigMappingValues(web)["ui"] == nil {
			setYAMLConfigKey(web, "ui", cloneYAMLConfigNode(legacy))
		}
		removeYAMLConfigKey(result, "web-ui")
	}
	return result
}

func unmarshalConfigYAML(data []byte, cfg *Config) error {
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return err
	}
	var extra struct {
		Server struct {
			Branding BrandingConfig `yaml:"branding"`
			SEO      SEOConfig      `yaml:"seo"`
		} `yaml:"server"`
		Web struct {
			UI struct {
				Theme         string              `yaml:"theme"`
				Accent        string              `yaml:"accent"`
				Notifications NotificationsConfig `yaml:"notifications"`
			} `yaml:"ui"`
		} `yaml:"web"`
	}
	if err := yaml.Unmarshal(data, &extra); err != nil {
		return err
	}
	cfg.Server.Branding = extra.Server.Branding
	cfg.Server.SEO = extra.Server.SEO
	cfg.WebUI = WebUIConfig{Theme: extra.Web.UI.Theme, Accent: extra.Web.UI.Accent, Notifications: extra.Web.UI.Notifications}
	return nil
}

func randomUnusedServerPort() (string, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		value, err := rand.Int(rand.Reader, big.NewInt(1000))
		if err != nil {
			return "", err
		}
		port := int(value.Int64()) + 64000
		listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(port)))
		if err == nil {
			_ = listener.Close()
			return strconv.Itoa(port), nil
		}
	}
	return "", fmt.Errorf("no unused port found in 64000-64999")
}

func firstRunServerPort() (string, error) {
	return SelectInitialServerPort(os.Getenv("PORT"))
}

// SelectInitialServerPort chooses the configured first-run port before the full config is loaded.
func SelectInitialServerPort(portEnv string) (string, error) {
	return selectInitialServerPort(portEnv, runningInContainer())
}

func selectInitialServerPort(portEnv string, inContainer bool) (string, error) {
	if port := strings.TrimSpace(portEnv); port != "" {
		return port, nil
	}
	if inContainer {
		return "80", nil
	}
	return randomUnusedServerPort()
}

func persistServerPort(path, port string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("server configuration must be a YAML mapping")
	}
	root := document.Content[0]
	server := mappingNodeValue(root, "server")
	if server == nil {
		server = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "server"}, server)
	}
	if server.Kind != yaml.MappingNode {
		return errors.New("server configuration must be a YAML mapping")
	}
	portNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: port}
	for index := 0; index+1 < len(server.Content); index += 2 {
		if server.Content[index].Value == "port" {
			server.Content[index+1] = portNode
			encoded, err := yaml.Marshal(&document)
			if err != nil {
				return err
			}
			return writeAtomicPrivateFile(path, encoded)
		}
	}
	server.Content = append(server.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "port"}, portNode)
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	return writeAtomicPrivateFile(path, encoded)
}

func validateOverlayConfig(cfg *Config) {
	torDefaults := DefaultConfig().Server.Tor
	setInt := func(key string, value *int, fallback, minimum, maximum int) {
		if *value < minimum || *value > maximum {
			log.Printf("Warning: invalid server.%s; using %d", key, fallback)
			*value = fallback
		}
	}
	setInt("tor.max_circuits", &cfg.Server.Tor.MaxCircuits, torDefaults.MaxCircuits, 1, 1000)
	setInt("tor.startup_timeout", &cfg.Server.Tor.StartupTimeout, torDefaults.StartupTimeout, 10, 300)
	setInt("tor.circuit_timeout", &cfg.Server.Tor.CircuitTimeout, torDefaults.CircuitTimeout, 10, 600)
	setInt("tor.bootstrap_timeout", &cfg.Server.Tor.BootstrapTimeout, torDefaults.BootstrapTimeout, 30, 600)
	setInt("tor.max_streams_per_circuit", &cfg.Server.Tor.MaxStreamsPerCircuit, torDefaults.MaxStreamsPerCircuit, 1, 65535)
	setInt("tor.num_intro_points", &cfg.Server.Tor.NumIntroPoints, torDefaults.NumIntroPoints, 3, 10)
	setInt("tor.virtual_port", &cfg.Server.Tor.VirtualPort, torDefaults.VirtualPort, 1, 65535)
	for key, value := range map[string]*string{"tor.bandwidth_rate": &cfg.Server.Tor.BandwidthRate, "tor.bandwidth_burst": &cfg.Server.Tor.BandwidthBurst} {
		if !validOverlayBandwidth(*value, false) {
			fallback := torDefaults.BandwidthRate
			if key == "tor.bandwidth_burst" {
				fallback = torDefaults.BandwidthBurst
			}
			log.Printf("Warning: invalid server.%s; using %s", key, fallback)
			*value = fallback
		}
	}
	if !validOverlayBandwidth(cfg.Server.Tor.MaxMonthlyBandwidth, true) {
		log.Printf("Warning: invalid server.tor.max_monthly_bandwidth; using %s", torDefaults.MaxMonthlyBandwidth)
		cfg.Server.Tor.MaxMonthlyBandwidth = torDefaults.MaxMonthlyBandwidth
	}
	i2pDefaults := DefaultConfig().Server.I2P
	setInt("i2p.virtual_port", &cfg.Server.I2P.VirtualPort, i2pDefaults.VirtualPort, 1, 65535)
	setInt("i2p.inbound_length", &cfg.Server.I2P.InboundLength, i2pDefaults.InboundLength, 0, 7)
	setInt("i2p.outbound_length", &cfg.Server.I2P.OutboundLength, i2pDefaults.OutboundLength, 0, 7)
	setInt("i2p.inbound_quantity", &cfg.Server.I2P.InboundQuantity, i2pDefaults.InboundQuantity, 1, 16)
	setInt("i2p.outbound_quantity", &cfg.Server.I2P.OutboundQuantity, i2pDefaults.OutboundQuantity, 1, 16)
	setInt("i2p.signature_type", &cfg.Server.I2P.SignatureType, i2pDefaults.SignatureType, 0, 255)
	setInt("i2p.bootstrap_timeout", &cfg.Server.I2P.BootstrapTimeout, i2pDefaults.BootstrapTimeout, 30, 600)
	if _, _, err := net.SplitHostPort(cfg.Server.I2P.SAMAddress); err != nil {
		log.Printf("Warning: invalid server.i2p.sam_address; using %s", i2pDefaults.SAMAddress)
		cfg.Server.I2P.SAMAddress = i2pDefaults.SAMAddress
	}
}

func validateRuntimeDataConfig(cfg *Config) {
	defaults := DefaultConfig()
	if strings.TrimSpace(cfg.Data.SecurityDir) == "" {
		log.Printf("Warning: invalid data.security_dir; using %s", defaults.Data.SecurityDir)
		cfg.Data.SecurityDir = defaults.Data.SecurityDir
	}
	if !validHTTPSURL(cfg.Data.CVE.Source) {
		log.Printf("Warning: invalid data.cve.source; using the NVD default")
		cfg.Data.CVE.Source = defaults.Data.CVE.Source
	}
	if strings.TrimSpace(cfg.Data.Trivy.Source) == "" {
		log.Printf("Warning: invalid data.trivy.source; using the default OCI reference")
		cfg.Data.Trivy.Source = defaults.Data.Trivy.Source
	}
	if cfg.Server.Security.Reports.RetentionDays <= 0 {
		log.Printf("Warning: invalid server.security.reports.retention_days; using %d", defaults.Server.Security.Reports.RetentionDays)
		cfg.Server.Security.Reports.RetentionDays = defaults.Server.Security.Reports.RetentionDays
	}
	seen := map[string]bool{"firehol_level1": true}
	validSources := make([]BlocklistSourceConfig, 0, len(cfg.Server.Security.Blocklists.ExtraSources))
	for _, source := range cfg.Server.Security.Blocklists.ExtraSources {
		idValid := regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(source.ID) && !seen[source.ID]
		if !idValid || (source.Type != "ip" && source.Type != "domain") || !validHTTPSURL(source.URL) {
			log.Printf("Warning: invalid server.security.blocklists source %q; skipping", source.ID)
			continue
		}
		seen[source.ID] = true
		validSources = append(validSources, source)
	}
	cfg.Server.Security.Blocklists.ExtraSources = validSources
	retention := &cfg.Server.Backup.Retention
	if retention.MaxBackups < 1 {
		log.Printf("Warning: invalid server.backup.retention.max_backups; using %d", defaults.Server.Backup.Retention.MaxBackups)
		retention.MaxBackups = defaults.Server.Backup.Retention.MaxBackups
	}
	if retention.KeepWeekly < 0 {
		retention.KeepWeekly = 0
	}
	if retention.KeepMonthly < 0 {
		retention.KeepMonthly = 0
	}
	if retention.KeepYearly < 0 {
		retention.KeepYearly = 0
	}
	if strings.TrimSpace(retention.MaxTotalSize) == "" {
		retention.MaxTotalSize = defaults.Server.Backup.Retention.MaxTotalSize
	}
	if !validBackupSizeCap(retention.MaxTotalSize) {
		log.Printf("Warning: invalid server.backup.retention.max_total_size; using %s", defaults.Server.Backup.Retention.MaxTotalSize)
		retention.MaxTotalSize = defaults.Server.Backup.Retention.MaxTotalSize
	}
	restore := &cfg.Server.Restore
	if !validConfigSize(restore.MaxArchiveSize) {
		restore.MaxArchiveSize = defaults.Server.Restore.MaxArchiveSize
	}
	if !validConfigSize(restore.MaxExpandedSize) {
		restore.MaxExpandedSize = defaults.Server.Restore.MaxExpandedSize
	}
	if restore.MaxFiles < 1 {
		restore.MaxFiles = defaults.Server.Restore.MaxFiles
	}
	if !validConfigSize(restore.MaxFileSize) {
		restore.MaxFileSize = defaults.Server.Restore.MaxFileSize
	}
	if restore.MaxRatio < 1 {
		restore.MaxRatio = defaults.Server.Restore.MaxRatio
	}
	cfg.Server.Backup.Encryption.Enabled = cfg.Server.Backup.Password != ""
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == ""
}

func validBackupSizeCap(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "0", "false", "no", "none", "disable", "disabled", "off":
		return true
	}
	if strings.HasSuffix(value, "%") {
		percent, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
		return err == nil && percent > 0 && percent <= 100
	}
	return validConfigSize(value)
}

func validConfigSize(value string) bool {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return false
	}
	unit := 1
	for _, suffix := range []struct {
		name string
		base int
	}{
		{"KIB", 1024}, {"MIB", 1024 * 1024}, {"GIB", 1024 * 1024 * 1024},
		{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
		{"K", 1024}, {"M", 1024 * 1024}, {"G", 1024 * 1024 * 1024}, {"B", 1},
	} {
		if strings.HasSuffix(value, suffix.name) {
			unit = suffix.base
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix.name))
			break
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	return err == nil && amount > 0 && amount <= int64(^uint64(0)>>1)/int64(unit)
}

func validOverlayBandwidth(value string, allowUnlimited bool) bool {
	if allowUnlimited && strings.EqualFold(strings.TrimSpace(value), "unlimited") {
		return true
	}
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(value)))
	if len(fields) != 2 {
		return false
	}
	amount, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || amount <= 0 {
		return false
	}
	switch fields[1] {
	case "KB", "KIB", "MB", "MIB", "GB", "GIB", "TB", "TIB":
		return true
	}
	return false
}

var (
	googleTrackingID = regexp.MustCompile(`^(UA-\d+-\d+|G-[A-Z0-9]+)$`)
	umamiTrackingID  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

func validTrackingDomain(value string) bool {
	if len(value) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(value, "."), ".")
	if len(labels) < 2 || len(labels[len(labels)-1]) < 2 || len(labels[len(labels)-1]) > 63 {
		return false
	}
	for _, char := range labels[len(labels)-1] {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') {
			return false
		}
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func ValidateTracking(cfg *TrackingConfig) error {
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.Type == "" || cfg.Type == "none" {
		return nil
	}
	switch cfg.Type {
	case "google":
		if !googleTrackingID.MatchString(cfg.ID) {
			return fmt.Errorf("invalid Google Analytics ID format")
		}
	case "matomo", "piwik":
		if _, err := strconv.Atoi(cfg.ID); err != nil || cfg.ID == "" {
			return fmt.Errorf("matomo/piwik ID must be an integer")
		}
		if cfg.URL == "" {
			return fmt.Errorf("%s requires URL", cfg.Type)
		}
	case "owa":
		if cfg.ID == "" || cfg.URL == "" {
			return fmt.Errorf("OWA requires site ID and URL")
		}
	case "fathom":
		if cfg.ID == "" {
			return fmt.Errorf("fathom requires site ID")
		}
	case "plausible":
		if !validTrackingDomain(cfg.ID) {
			return fmt.Errorf("plausible requires a valid domain")
		}
	case "umami":
		if !umamiTrackingID.MatchString(cfg.ID) {
			return fmt.Errorf("umami ID must be a valid UUID")
		}
		if cfg.URL == "" {
			return fmt.Errorf("umami requires URL")
		}
	case "simple":
		cfg.ID, cfg.URL = "", ""
	case "cloudflare":
		if cfg.ID == "" {
			return fmt.Errorf("cloudflare requires beacon token")
		}
	default:
		return fmt.Errorf("unknown tracking type: %s", cfg.Type)
	}
	if cfg.URL != "" {
		parsed, err := url.ParseRequestURI(cfg.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("invalid tracking URL")
		}
	}
	return nil
}

type ServerSecurityConfig struct {
	Allowlist     []AllowlistEntry      `yaml:"allowlist"`
	Blocklists    BlocklistsConfig      `yaml:"blocklists"`
	Reports       SecurityReportsConfig `yaml:"reports"`
	EncryptionKey string                `yaml:"-"`
}

type BlocklistsConfig struct {
	Enabled      bool                    `yaml:"enabled"`
	ExtraSources []BlocklistSourceConfig `yaml:"extra_sources"`
}

type BlocklistSourceConfig struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	URL     string `yaml:"url"`
	Enabled *bool  `yaml:"enabled,omitempty"`
}

type SecurityReportsConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

type AllowlistEntry struct {
	CIDR        string `yaml:"cidr" json:"cidr"`
	Description string `yaml:"description" json:"description"`
}

func loadSecretSettings(configPath string, cfg *Config) error {
	path := filepath.Join(filepath.Dir(configPath), "secrets.yml")
	values := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := yaml.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("read secrets file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	configNode, configData, err := extractMetricsTokens(configPath)
	if err != nil {
		return err
	}
	server, _ := values["server"].(map[string]interface{})
	if server == nil {
		server = map[string]interface{}{}
		values["server"] = server
	}
	security, _ := server["security"].(map[string]interface{})
	if security == nil {
		security = map[string]interface{}{}
		server["security"] = security
	}
	operatorToken, _ := server["token"].(string)
	if operatorToken == "" {
		operatorToken = configNode["token"]
	}
	generatedOperatorToken := false
	if operatorToken == "" {
		var tokenErr error
		operatorToken, tokenErr = generateBearerToken("adm_")
		if tokenErr != nil {
			return fmt.Errorf("generate operator token: %w", tokenErr)
		}
		generatedOperatorToken = true
	}
	if !validBearerToken(operatorToken, "adm_") {
		return fmt.Errorf("server.token in secrets.yml must use adm_ followed by 32 base62 characters")
	}
	server["token"] = operatorToken
	cfg.Server.Token = operatorToken
	backupSettings, _ := server["backup"].(map[string]interface{})
	if backupSettings == nil {
		backupSettings = map[string]interface{}{}
		server["backup"] = backupSettings
	}
	if secret, ok := backupSettings["encryption_password"].(string); ok {
		cfg.Server.Backup.Password = secret
	}
	dataSettings, _ := values["data"].(map[string]interface{})
	if dataSettings == nil {
		dataSettings = map[string]interface{}{}
		values["data"] = dataSettings
	}
	cveSettings, _ := dataSettings["cve"].(map[string]interface{})
	if cveSettings == nil {
		cveSettings = map[string]interface{}{}
		dataSettings["cve"] = cveSettings
	}
	if secret, ok := cveSettings["api_key"].(string); ok {
		cfg.Data.CVE.APIKey = secret
	}
	key, _ := security["encryption_key"].(string)
	if key == "" {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return fmt.Errorf("generate encryption key: %w", err)
		}
		key = base64.StdEncoding.EncodeToString(bytes)
		security["encryption_key"] = key
		if _, ok := security["encryption_key_version"]; !ok {
			security["encryption_key_version"] = 1
		}
	}
	decodedKey, decodeErr := base64.StdEncoding.DecodeString(key)
	if decodeErr != nil || len(decodedKey) != 32 {
		return fmt.Errorf("server.security.encryption_key in secrets.yml must be base64 encoding of 32 bytes")
	}
	cfg.Server.Security.EncryptionKey = key
	sslSettings, _ := server["ssl"].(map[string]interface{})
	if sslSettings == nil {
		sslSettings = map[string]interface{}{}
	}
	letsencryptSettings, _ := sslSettings["letsencrypt"].(map[string]interface{})
	if letsencryptSettings == nil {
		letsencryptSettings = map[string]interface{}{}
	}
	storedDNSCredentials := stringMap(letsencryptSettings["dns_credentials"])
	for configKey, value := range configNode {
		const prefix = "dns_credentials."
		if strings.HasPrefix(configKey, prefix) {
			name := strings.TrimPrefix(configKey, prefix)
			storedDNSCredentials[name] = value
		}
	}
	dnsCredentials := make(map[string]string, len(storedDNSCredentials))
	var previousKey []byte
	previousKeyLoaded := false
	previousKeyFound := false
	for name, value := range storedDNSCredentials {
		plaintext, err := decryptDNSCredential(decodedKey, value)
		if err != nil {
			if !previousKeyLoaded {
				previousKey, previousKeyFound, err = loadPreviousEncryptionKey(configPath, cfg, decodedKey)
				previousKeyLoaded = true
				if err != nil {
					return fmt.Errorf("load previous encryption key: %w", err)
				}
			}
			if !previousKeyFound {
				return fmt.Errorf("server.ssl.letsencrypt.dns_credentials.%s could not be decrypted", name)
			}
			plaintext, err = decryptDNSCredential(previousKey, value)
			if err != nil {
				return fmt.Errorf("server.ssl.letsencrypt.dns_credentials.%s could not be decrypted with either active key", name)
			}
			value, err = encryptDNSCredential(decodedKey, plaintext)
			if err != nil {
				return fmt.Errorf("reencrypt server.ssl.letsencrypt.dns_credentials.%s: %w", name, err)
			}
		}
		if !strings.HasPrefix(value, "enc:v1:") {
			value, err = encryptDNSCredential(decodedKey, plaintext)
			if err != nil {
				return fmt.Errorf("encrypt server.ssl.letsencrypt.dns_credentials.%s: %w", name, err)
			}
		}
		dnsCredentials[name] = plaintext
		storedDNSCredentials[name] = value
	}
	if len(storedDNSCredentials) > 0 {
		letsencryptSettings["dns_credentials"] = storedDNSCredentials
		sslSettings["letsencrypt"] = letsencryptSettings
		server["ssl"] = sslSettings
	}
	cfg.Server.SSL.LetsEncrypt.DNSCredentials = dnsCredentials
	notifications, _ := server["notifications"].(map[string]interface{})
	if notifications == nil {
		notifications = map[string]interface{}{}
		server["notifications"] = notifications
	}
	email, _ := notifications["email"].(map[string]interface{})
	if email == nil {
		email = map[string]interface{}{}
		notifications["email"] = email
	}
	smtp, _ := email["smtp"].(map[string]interface{})
	if smtp == nil {
		smtp = map[string]interface{}{}
		email["smtp"] = smtp
	}
	if password, ok := smtp["password"].(string); ok {
		cfg.Server.Notifications.Email.SMTP.Password = password
	}
	pages, _ := server["pages"].(map[string]interface{})
	if pages == nil {
		pages = map[string]interface{}{}
		server["pages"] = pages
	}
	contact, _ := pages["contact"].(map[string]interface{})
	if contact == nil {
		contact = map[string]interface{}{}
		pages["contact"] = contact
	}
	if secret, ok := contact["captcha_secret_key"].(string); ok {
		cfg.Server.Pages.Contact.CaptchaSecretKey = secret
	}
	metrics, _ := server["metrics"].(map[string]interface{})
	if metrics == nil {
		metrics = map[string]interface{}{}
		server["metrics"] = metrics
	}
	metricsAuth, _ := metrics["auth"].(map[string]interface{})
	if metricsAuth == nil {
		metricsAuth = map[string]interface{}{}
		metrics["auth"] = metricsAuth
	}
	tokens, _ := metricsAuth["tokens"].(map[string]interface{})
	if tokens == nil {
		tokens = map[string]interface{}{}
		metricsAuth["tokens"] = tokens
	}
	metricToken := func(service string) (string, error) {
		secret, _ := tokens[service].(string)
		if secret == "" {
			secret = configNode[service]
		}
		if secret == "" {
			var err error
			secret, err = generateMetricToken()
			if err != nil {
				return "", err
			}
		}
		tokens[service] = secret
		return secret, nil
	}
	prometheusToken, err := metricToken("prometheus")
	if err != nil {
		return fmt.Errorf("generate Prometheus metrics token: %w", err)
	}
	grafanaToken, err := metricToken("grafana")
	if err != nil {
		return fmt.Errorf("generate Grafana metrics token: %w", err)
	}
	lokiToken, err := metricToken("loki")
	if err != nil {
		return fmt.Errorf("generate Loki metrics token: %w", err)
	}
	cfg.Server.Metrics.Auth.Tokens = MetricsAuthTokens{Prometheus: prometheusToken, Grafana: grafanaToken, Loki: lokiToken}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return err
	}
	encoded, err := yaml.Marshal(values)
	if err != nil {
		return err
	}
	if err := writeAtomicPrivateFile(path, encoded); err != nil {
		return err
	}
	if configData != nil {
		if err := writeAtomicPrivateFile(configPath, configData); err != nil {
			return fmt.Errorf("remove generated metrics tokens from server.yml: %w", err)
		}
	}
	if generatedOperatorToken {
		log.Print("INFO: operator token generated and stored in secrets.yml")
	}
	return nil
}

func loadPreviousEncryptionKey(configPath string, cfg *Config, currentKey []byte) ([]byte, bool, error) {
	databaseURL := strings.TrimSpace(cfg.Server.Database.URL)
	dataDir := paths.GetDataDir("countries")
	if override := strings.TrimSpace(os.Getenv("DATA_DIR")); override != "" {
		dataDir = override
	}
	databasePath := strings.ReplaceAll(databaseURL, "{data_dir}", dataDir)
	if databaseURL == "" || databaseURL == "{data_dir}/db/server.db" {
		databasePath = filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	if !filepath.IsAbs(databasePath) {
		databasePath = filepath.Join(filepath.Dir(configPath), databasePath)
	}
	if _, err := os.Stat(databasePath); os.IsNotExist(err) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("inspect database for previous encryption key: %w", err)
	}
	db, err := database.Open(databasePath)
	if err != nil {
		return nil, false, fmt.Errorf("open database for previous encryption key: %w", err)
	}
	defer func() { _ = db.Close() }()
	encoded, found, err := database.GetAppSecret(context.Background(), db, "encryption_key_previous")
	if err != nil || !found {
		return nil, found, err
	}
	expires, expiresFound, err := database.GetAppSecret(context.Background(), db, "encryption_key_previous_until")
	if err != nil {
		return nil, false, err
	}
	if !expiresFound {
		return nil, false, errors.New("previous encryption key has no expiry")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return nil, false, errors.New("previous encryption key has an invalid expiry")
	}
	if time.Now().UTC().After(expiresAt) {
		return nil, false, nil
	}
	wrapped, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("decode previous encryption key: %w", err)
	}
	previous, err := unwrapPreviousEncryptionKey(currentKey, wrapped)
	if err != nil {
		return nil, false, fmt.Errorf("unwrap previous encryption key: %w", err)
	}
	return previous, true, nil
}

func unwrapPreviousEncryptionKey(currentKey, wrapped []byte) ([]byte, error) {
	if len(currentKey) != 32 {
		return nil, errors.New("current encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(currentKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(wrapped) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("wrapped previous encryption key is truncated")
	}
	nonce, ciphertext := wrapped[:gcm.NonceSize()], wrapped[gcm.NonceSize():]
	previous, err := gcm.Open(nil, nonce, ciphertext, []byte("countries-encryption-key-previous-v1"))
	if err != nil {
		return nil, errors.New("wrapped previous encryption key could not be authenticated")
	}
	if len(previous) != 32 {
		return nil, errors.New("previous encryption key must be 32 bytes")
	}
	return previous, nil
}

func generateMetricToken() (string, error) {
	return generateBearerToken("mtr_")
}

func generateBearerToken(prefix string) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const maxByte = 248
	token := make([]byte, 0, len(prefix)+32)
	token = append(token, prefix...)
	buffer := make([]byte, 64)
	for len(token) < len(prefix)+32 {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		for _, value := range buffer {
			if int(value) >= maxByte {
				continue
			}
			token = append(token, alphabet[int(value)%len(alphabet)])
			if len(token) == len(prefix)+32 {
				break
			}
		}
	}
	return string(token), nil
}

func validBearerToken(token, prefix string) bool {
	if !strings.HasPrefix(token, prefix) || len(token) != len(prefix)+32 {
		return false
	}
	for _, value := range token[len(prefix):] {
		if (value < '0' || value > '9') && (value < 'A' || value > 'Z') && (value < 'a' || value > 'z') {
			return false
		}
	}
	return true
}

func writeAtomicPrivateFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return fmt.Errorf("sync config directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close config directory: %w", closeErr)
	}
	return nil
}

func stringMap(value interface{}) map[string]string {
	result := make(map[string]string)
	switch values := value.(type) {
	case map[string]interface{}:
		for key, raw := range values {
			if text, ok := raw.(string); ok {
				result[key] = text
			}
		}
	case map[string]string:
		for key, text := range values {
			result[key] = text
		}
	}
	return result
}

func encryptDNSCredential(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:v1:" + base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptDNSCredential(key []byte, stored string) (string, error) {
	if !strings.HasPrefix(stored, "enc:v1:") {
		return stored, nil
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, "enc:v1:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return "", fmt.Errorf("encrypted value is truncated")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func extractMetricsTokens(configPath string) (map[string]string, []byte, error) {
	values := map[string]string{}
	changed := false
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil, nil
		}
		return nil, nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, nil, err
	}
	if len(document.Content) == 0 {
		return values, nil, nil
	}
	root := document.Content[0]
	server := mappingNodeValue(root, "server")
	if token := mappingNodeValue(server, "token"); token != nil {
		if token.Kind != yaml.ScalarNode || token.Tag != "!!str" && token.Tag != "!!null" {
			return nil, nil, fmt.Errorf("server.token must be a string")
		}
		if token.Tag != "!!null" {
			values["token"] = token.Value
		}
		removeMappingNodeValue(server, "token")
		changed = true
	}
	ssl := mappingNodeValue(server, "ssl")
	letsencrypt := mappingNodeValue(ssl, "letsencrypt")
	dnsCredentials := mappingNodeValue(letsencrypt, "dns_credentials")
	if dnsCredentials != nil {
		if dnsCredentials.Kind != yaml.MappingNode {
			return nil, nil, fmt.Errorf("server.ssl.letsencrypt.dns_credentials must be a flat mapping")
		}
		for index := 0; index+1 < len(dnsCredentials.Content); index += 2 {
			key, value := dnsCredentials.Content[index], dnsCredentials.Content[index+1]
			if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode || value.Tag != "!!str" && value.Tag != "!!null" {
				return nil, nil, fmt.Errorf("server.ssl.letsencrypt.dns_credentials values must be strings")
			}
			if value.Tag != "!!null" {
				values["dns_credentials."+key.Value] = value.Value
			}
		}
		removeMappingNodeValue(letsencrypt, "dns_credentials")
		changed = true
	}
	metrics := mappingNodeValue(server, "metrics")
	auth := mappingNodeValue(metrics, "auth")
	tokens := mappingNodeValue(auth, "tokens")
	if tokens == nil {
		if !changed {
			return values, nil, nil
		}
		updated, err := yaml.Marshal(&document)
		return values, updated, err
	}
	if tokens.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("server.metrics.auth.tokens must be a mapping")
	}
	for index := 0; index+1 < len(tokens.Content); index += 2 {
		key, value := tokens.Content[index], tokens.Content[index+1]
		if key.Value == "prometheus" || key.Value == "grafana" || key.Value == "loki" {
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" && value.Tag != "!!null" {
				return nil, nil, fmt.Errorf("server.metrics.auth.tokens.%s must be a string", key.Value)
			}
			if value.Tag != "!!null" {
				values[key.Value] = value.Value
			}
		}
	}
	removeMappingNodeValue(auth, "tokens")
	updated, err := yaml.Marshal(&document)
	if err != nil {
		return nil, nil, err
	}
	return values, updated, nil
}

func mappingNodeValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func removeMappingNodeValue(node *yaml.Node, key string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			node.Content = append(node.Content[:index], node.Content[index+2:]...)
			return
		}
	}
}

func (c *MetricsLokiConfig) UnmarshalYAML(node *yaml.Node) error {
	var fields struct {
		MaxEntries int    `yaml:"max_entries"`
		MaxAge     string `yaml:"max_age"`
	}
	if err := node.Decode(&fields); err != nil {
		return err
	}
	maxAge := c.MaxAge
	if raw := mappingValue(node, "max_age"); raw != nil {
		if raw.Tag == "!!str" {
			parsed, err := time.ParseDuration(raw.Value)
			if err != nil {
				return fmt.Errorf("server.metrics.loki.max_age: %w", err)
			}
			maxAge = parsed
		} else {
			parsed, err := strconv.ParseInt(raw.Value, 10, 64)
			if err != nil {
				return fmt.Errorf("server.metrics.loki.max_age must be a duration such as 1h")
			}
			maxAge = time.Duration(parsed)
		}
	}
	c.MaxEntries = fields.MaxEntries
	if fields.MaxEntries == 0 {
		c.MaxEntries = 1000
	}
	c.MaxAge = maxAge
	if c.MaxAge <= 0 {
		c.MaxAge = time.Hour
	}
	return nil
}

func (c MetricsLokiConfig) MarshalYAML() (interface{}, error) {
	return struct {
		MaxEntries int    `yaml:"max_entries"`
		MaxAge     string `yaml:"max_age"`
	}{MaxEntries: c.MaxEntries, MaxAge: c.MaxAge.String()}, nil
}

func validateContactConfig(cfg *Config) error {
	contact := &cfg.Server.Pages.Contact
	contact.Captcha = strings.ToLower(strings.TrimSpace(contact.Captcha))
	if contact.Captcha == "" {
		contact.Captcha = "simple"
	}
	switch contact.Captcha {
	case "simple":
	case "recaptcha", "hcaptcha":
		if strings.TrimSpace(contact.CaptchaSiteKey) == "" || strings.TrimSpace(contact.CaptchaSecretKey) == "" {
			return fmt.Errorf("server.pages.contact.captcha_site_key and captcha_secret_key are required for %s", contact.Captcha)
		}
	default:
		return fmt.Errorf("server.pages.contact.captcha must be simple, recaptcha, or hcaptcha")
	}
	return nil
}

func resetInvalidNotificationConfig(cfg *Config, validationErr error) {
	defaults := DefaultConfig().Server.Notifications
	message := validationErr.Error()
	switch {
	case strings.Contains(message, "notifications.webui.position"):
		cfg.Server.Notifications.WebUI.Position = defaults.WebUI.Position
	case strings.Contains(message, "notifications.webui toast durations"):
		cfg.Server.Notifications.WebUI.Duration = defaults.WebUI.Duration
		cfg.Server.Notifications.WebUI.WarningDuration = defaults.WebUI.WarningDuration
	case strings.Contains(message, "disk_low.threshold_percent"):
		cfg.Server.Notifications.DiskLow.ThresholdPercent = defaults.DiskLow.ThresholdPercent
	case strings.Contains(message, "disk_low.check_interval"):
		cfg.Server.Notifications.DiskLow.CheckInterval = defaults.DiskLow.CheckInterval
	case strings.Contains(message, "disk_low.repeat"):
		cfg.Server.Notifications.DiskLow.Repeat = defaults.DiskLow.Repeat
	case strings.Contains(message, "email.from.email"):
		cfg.Server.Notifications.Email.From.Email = defaults.Email.From.Email
	case strings.Contains(message, "email.reply_to"):
		cfg.Server.Notifications.Email.ReplyTo = defaults.Email.ReplyTo
	default:
		cfg.Server.Notifications = defaults
	}
}

func validateNotificationConfig(cfg *Config) error {
	notifications := &cfg.Server.Notifications
	webui := &notifications.WebUI
	if webui.Position == "" {
		webui.Position = "top-right"
	}
	switch webui.Position {
	case "top-right", "top-left", "bottom-right", "bottom-left":
	default:
		return fmt.Errorf("server.notifications.webui.position must be top-right, top-left, bottom-right, or bottom-left")
	}
	if webui.Duration < 0 || webui.WarningDuration < 0 {
		return fmt.Errorf("server.notifications.webui toast durations cannot be negative")
	}
	disk := &notifications.DiskLow
	if disk.ThresholdPercent < 1 || disk.ThresholdPercent > 100 {
		return fmt.Errorf("server.notifications.disk_low.threshold_percent must be between 1 and 100")
	}
	if _, err := time.ParseDuration(disk.CheckInterval); err != nil || disk.CheckInterval == "" {
		return fmt.Errorf("server.notifications.disk_low.check_interval must be a valid duration")
	}
	if _, err := time.ParseDuration(disk.Repeat); err != nil || disk.Repeat == "" {
		return fmt.Errorf("server.notifications.disk_low.repeat must be a valid duration")
	}
	smtp := &notifications.Email.SMTP
	smtp.Host = strings.TrimSpace(smtp.Host)
	if value, ok := os.LookupEnv("SMTP_HOST"); ok {
		smtp.Host = strings.TrimSpace(value)
	}
	if raw := strings.TrimSpace(os.Getenv("SMTP_PORT")); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			log.Printf("WARN: invalid SMTP_PORT; using the configured SMTP port")
		} else {
			smtp.Port = port
		}
	}
	if smtp.Port == 0 {
		smtp.Port = 587
	}
	smtp.TLS = strings.ToLower(strings.TrimSpace(smtp.TLS))
	if value := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS"))); value != "" {
		switch value {
		case "auto", "tls", "starttls", "none":
			smtp.TLS = value
		default:
			log.Printf("WARN: invalid SMTP_TLS; using the configured SMTP TLS mode")
		}
	}
	if smtp.TLS == "" {
		smtp.TLS = "auto"
	}
	switch smtp.TLS {
	case "auto", "tls", "starttls", "none":
	default:
		log.Printf("WARN: invalid server.notifications.email.smtp.tls; using auto")
		smtp.TLS = "auto"
	}
	if value, ok := os.LookupEnv("SMTP_USERNAME"); ok {
		smtp.Username = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv("SMTP_PASSWORD"); ok {
		smtp.Password = value
	}
	if value, ok := os.LookupEnv("SMTP_FROM_NAME"); ok {
		notifications.Email.From.Name = strings.TrimSpace(value)
	}
	if value, ok := os.LookupEnv("SMTP_FROM_EMAIL"); ok {
		notifications.Email.From.Email = strings.TrimSpace(value)
	}
	if notifications.Email.From.Email != "" {
		parsed, err := mail.ParseAddress(notifications.Email.From.Email)
		if err != nil || parsed.Address != notifications.Email.From.Email {
			if _, fromEnv := os.LookupEnv("SMTP_FROM_EMAIL"); fromEnv {
				log.Printf("WARN: invalid SMTP_FROM_EMAIL; ignoring the environment override")
				notifications.Email.From.Email = ""
			} else {
				return fmt.Errorf("server.notifications.email.from.email must be a valid email address")
			}
		}
	}
	if notifications.Email.ReplyTo != "" {
		parsed, err := mail.ParseAddress(notifications.Email.ReplyTo)
		if err != nil || parsed.Address != notifications.Email.ReplyTo {
			notifications.Email.ReplyTo = ""
			return fmt.Errorf("server.notifications.email.reply_to must be a valid email address")
		}
	}
	notifications.Email.Enabled = false
	return nil
}

func validateI18NConfig(cfg *Config) {
	i18nConfig := &cfg.Server.I18N
	defaultLanguage := strings.ToLower(strings.TrimSpace(i18nConfig.DefaultLanguage))
	if !i18n.IsSupported(defaultLanguage) {
		log.Printf("Warning: invalid server.i18n.default_language; using en")
		defaultLanguage = "en"
	}
	i18nConfig.DefaultLanguage = defaultLanguage

	available := make([]string, 0, len(i18nConfig.AvailableLanguages))
	seen := make(map[string]bool, len(i18nConfig.AvailableLanguages))
	valid := len(i18nConfig.AvailableLanguages) > 0
	for _, language := range i18nConfig.AvailableLanguages {
		language = strings.ToLower(strings.TrimSpace(language))
		if !i18n.IsSupported(language) || seen[language] {
			valid = false
			break
		}
		seen[language] = true
		available = append(available, language)
	}
	for _, required := range []string{"en", "es", "fr", "de", "it", "ar"} {
		if !seen[required] {
			valid = false
			break
		}
	}
	if !valid {
		log.Printf("Warning: invalid server.i18n.available_languages; using all registered languages")
		available = i18n.Languages()
		seen = make(map[string]bool, len(available))
		for _, language := range available {
			seen[language] = true
		}
	}
	if !seen[defaultLanguage] {
		log.Printf("Warning: server.i18n.default_language is not in available_languages; using en")
		defaultLanguage = "en"
		i18nConfig.DefaultLanguage = defaultLanguage
	}
	i18nConfig.AvailableLanguages = available
}

func validateAnnouncementsConfig(cfg *Config) error {
	seen := make(map[string]struct{}, len(cfg.Web.Announcements.Messages))
	for index := range cfg.Web.Announcements.Messages {
		announcement := &cfg.Web.Announcements.Messages[index]
		announcement.ID = strings.TrimSpace(announcement.ID)
		if announcement.ID == "" || strings.ContainsAny(announcement.ID, ",\r\n") {
			return fmt.Errorf("web.announcements.messages[%d].id is required and cannot contain commas or newlines", index)
		}
		if _, exists := seen[announcement.ID]; exists {
			return fmt.Errorf("web.announcements.messages[%d].id is duplicated", index)
		}
		seen[announcement.ID] = struct{}{}
		announcement.Type = strings.ToLower(strings.TrimSpace(announcement.Type))
		if announcement.Type == "" {
			announcement.Type = "info"
		}
		switch announcement.Type {
		case "info", "warning", "error", "success":
		default:
			return fmt.Errorf("web.announcements.messages[%d].type must be info, warning, error, or success", index)
		}
		if strings.TrimSpace(announcement.Title) == "" || strings.TrimSpace(announcement.Message) == "" {
			return fmt.Errorf("web.announcements.messages[%d] requires title and message", index)
		}
		var start, end time.Time
		if announcement.Start != "" {
			parsed, err := time.Parse(time.RFC3339, announcement.Start)
			if err != nil {
				return fmt.Errorf("web.announcements.messages[%d].start must be ISO 8601 UTC", index)
			}
			start = parsed
		}
		if announcement.End != "" {
			parsed, err := time.Parse(time.RFC3339, announcement.End)
			if err != nil {
				return fmt.Errorf("web.announcements.messages[%d].end must be ISO 8601 UTC", index)
			}
			end = parsed
		}
		if !start.IsZero() && !end.IsZero() && !start.Before(end) {
			return fmt.Errorf("web.announcements.messages[%d].end must be after start", index)
		}
	}
	return nil
}

func validateGeoIPConfig(cfg *Config) {
	normalizeCodes := func(key string, values []string) []string {
		valid := make([]string, 0, len(values))
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			code := strings.ToUpper(strings.TrimSpace(value))
			if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
				log.Printf("Warning: invalid %s country code; ignoring value", key)
				continue
			}
			if _, exists := seen[code]; exists {
				continue
			}
			seen[code] = struct{}{}
			valid = append(valid, code)
		}
		return valid
	}
	geoip := &cfg.Server.GeoIP
	geoip.DenyCountries = normalizeCodes("server.geoip.deny_countries", geoip.DenyCountries)
	geoip.AllowCountries = normalizeCodes("server.geoip.allow_countries", geoip.AllowCountries)
	if geoip.Presets == nil {
		geoip.Presets = map[string][]string{}
	}
	for name, codes := range geoip.Presets {
		geoip.Presets[name] = normalizeCodes("server.geoip.presets."+name, codes)
	}
}

func validateSecurityConfig(cfg *Config) error {
	for index := range cfg.Server.Security.Allowlist {
		entry := &cfg.Server.Security.Allowlist[index]
		value := strings.TrimSpace(entry.CIDR)
		if prefix, err := netip.ParsePrefix(value); err == nil {
			entry.CIDR = prefix.Masked().String()
		} else if address, addressErr := netip.ParseAddr(value); addressErr == nil {
			entry.CIDR = netip.PrefixFrom(address.Unmap(), address.Unmap().BitLen()).String()
		} else {
			return fmt.Errorf("server.security.allowlist[%d].cidr must be a valid IP address or CIDR", index)
		}
		prefix, _ := netip.ParsePrefix(entry.CIDR)
		minimumBits := 8
		if prefix.Addr().Is6() {
			minimumBits = 16
		}
		if prefix.Bits() < minimumBits {
			return fmt.Errorf("server.security.allowlist[%d].cidr is too broad", index)
		}
		if strings.TrimSpace(entry.Description) == "" {
			return fmt.Errorf("server.security.allowlist[%d].description is required", index)
		}
	}
	return nil
}

func validateSchedulerConfig(cfg *Config) {
	defaults := defaultSchedulerConfig()
	if _, err := time.LoadLocation(cfg.Server.Scheduler.Timezone); err != nil {
		log.Printf("Warning: invalid server.scheduler.timezone; using %s", defaults.Timezone)
		cfg.Server.Scheduler.Timezone = defaults.Timezone
	}
	window, err := time.ParseDuration(cfg.Server.Scheduler.CatchUpWindow)
	if err != nil || window <= 0 {
		log.Printf("Warning: invalid server.scheduler.catch_up_window; using %s", defaults.CatchUpWindow)
		cfg.Server.Scheduler.CatchUpWindow = defaults.CatchUpWindow
	}
	if cfg.Server.Scheduler.Tasks == nil {
		cfg.Server.Scheduler.Tasks = defaults.Tasks
		return
	}
	location, locationErr := time.LoadLocation(cfg.Server.Scheduler.Timezone)
	if locationErr != nil {
		location = time.Local
	}
	for name, fallback := range defaults.Tasks {
		task, exists := cfg.Server.Scheduler.Tasks[name]
		if !exists {
			cfg.Server.Scheduler.Tasks[name] = fallback
			continue
		}
		if strings.TrimSpace(task.Schedule) == "" || !isSchedulerScheduleValid(task.Schedule, location) {
			log.Printf("Warning: invalid server.scheduler.tasks.%s.schedule; using default", name)
			task.Schedule = fallback.Schedule
		}
		if !schedulerTaskMayBeDisabled(name) && !task.Enabled {
			log.Printf("Warning: server.scheduler.tasks.%s.enabled cannot be disabled; using true", name)
			task.Enabled = true
		}
		if task.RetryDelay != "" {
			if delay, err := time.ParseDuration(task.RetryDelay); err != nil || delay < 0 {
				log.Printf("Warning: invalid server.scheduler.tasks.%s.retry_delay; using default", name)
				task.RetryDelay = fallback.RetryDelay
			}
		}
		cfg.Server.Scheduler.Tasks[name] = task
	}
}

func isSchedulerScheduleValid(schedule string, location *time.Location) bool {
	_, err := backgroundscheduler.NextTime(schedule, time.Now(), location)
	return err == nil
}

func schedulerTaskMayBeDisabled(name string) bool {
	switch name {
	case "geoip_update", "blocklist_update", "cve_update", "update_check", "backup_daily", "backup_hourly", "disk_check":
		return true
	default:
		return false
	}
}

func validateUpdateConfig(cfg *Config) {
	branch := strings.ToLower(strings.TrimSpace(cfg.Server.Update.Branch))
	switch branch {
	case "daily", "rolling", "dev", "development":
		branch = "devel"
	case "", "stable", "beta", "devel":
	default:
		log.Printf("Warning: invalid server.update.branch value; using stable")
		branch = "stable"
	}
	cfg.Server.Update.Branch = branch
	if cfg.Server.Update.DeferDays < 0 || cfg.Server.Update.DeferDays > 365 {
		log.Printf("Warning: invalid server.update.defer_days value; using default")
		cfg.Server.Update.DeferDays = 0
	}
	cfg.Server.Update.CLIMinVersion = strings.TrimSpace(cfg.Server.Update.CLIMinVersion)
}

func validateRateLimitConfig(cfg *Config) {
	defaults := DefaultConfig().Server.RateLimit
	validateRule := func(name string, rule *RateLimitRuleConfig, fallback RateLimitRuleConfig) {
		if rule.Requests < 1 || rule.Window < 1 {
			log.Printf("Warning: invalid server.rate_limit.%s values; using defaults", name)
			*rule = fallback
		}
	}
	validateRule("read", &cfg.Server.RateLimit.Read, defaults.Read)
	validateRule("write", &cfg.Server.RateLimit.Write, defaults.Write)
	validateRule("health", &cfg.Server.RateLimit.Health, defaults.Health)
	validateRule("strict", &cfg.Server.RateLimit.Strict, defaults.Strict)
	validateRule("auth_failures", &cfg.Server.RateLimit.AuthFailures, defaults.AuthFailures)
	if cfg.Server.RateLimit.GlobalBurst < 1 {
		log.Printf("Warning: invalid server.rate_limit.global_burst value; using default")
		cfg.Server.RateLimit.GlobalBurst = defaults.GlobalBurst
	}
}

func validateTokensConfig(cfg *Config) {
	defaults := DefaultConfig().Server.Tokens
	if cfg.Server.Tokens.MaxLinked < 1 {
		log.Printf("Warning: invalid server.tokens.max_linked value; using default")
		cfg.Server.Tokens.MaxLinked = defaults.MaxLinked
	}
	if _, err := parseTokenCookieMaxAge(cfg.Server.Tokens.CookieMaxAge); err != nil {
		log.Printf("Warning: invalid server.tokens.cookie_max_age value; using default")
		cfg.Server.Tokens.CookieMaxAge = defaults.CookieMaxAge
	}
	if cfg.Server.Tokens.IdlePurgeDays < 0 {
		log.Printf("Warning: invalid server.tokens.idle_purge_days value; using default")
		cfg.Server.Tokens.IdlePurgeDays = defaults.IdlePurgeDays
	}
}

func parseTokenCookieMaxAge(value string) (int, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		seconds := duration.Seconds()
		if seconds < 1 || seconds > float64(^uint(0)>>1) {
			return 0, fmt.Errorf("duration is outside the supported range")
		}
		return int(seconds), nil
	}
	if len(value) < 2 {
		return 0, fmt.Errorf("duration must include a positive value and unit")
	}
	unit := value[len(value)-1]
	amount, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || amount < 1 {
		return 0, fmt.Errorf("duration must be positive")
	}
	var unitSeconds int64
	switch unit {
	case 'd':
		unitSeconds = int64((24 * time.Hour).Seconds())
	case 'y':
		unitSeconds = int64((365 * 24 * time.Hour).Seconds())
	default:
		return 0, fmt.Errorf("unsupported duration unit")
	}
	if int64(amount) > (int64(^uint64(0)>>1))/unitSeconds {
		return 0, fmt.Errorf("duration is too large")
	}
	seconds := int64(amount) * unitSeconds
	if seconds > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("duration is too large")
	}
	return int(seconds), nil
}

func Save(path string, cfg *Config) error {
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return err
	}
	root, err := configYAMLNode(cfg)
	if err != nil {
		return err
	}
	root.HeadComment = serverConfigHeader
	data, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), ".config-save-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporaryFile.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
		_ = os.Remove(temporaryPath + ".bak")
	}()
	if err := temporaryFile.Chmod(0600); err != nil {
		_ = temporaryFile.Close()
		return err
	}
	if _, err := temporaryFile.Write(data); err != nil {
		_ = temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Sync(); err != nil {
		_ = temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Close(); err != nil {
		return err
	}
	if err := migrateConfigLayout(temporaryPath); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
