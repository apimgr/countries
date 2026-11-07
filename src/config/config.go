// Package config loads, validates, and persists application configuration.
package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	WebUI       WebUIConfig       `yaml:"web-ui"`
	WebRobots   WebRobotsConfig   `yaml:"web-robots"`
	WebSecurity WebSecurityConfig `yaml:"web-security"`
}

type ServerConfig struct {
	Port           string               `yaml:"port"`
	Database       DatabaseConfig       `yaml:"database"`
	FQDN           string               `yaml:"fqdn"`
	Address        string               `yaml:"address"`
	Mode           string               `yaml:"mode"`
	Update         UpdateConfig         `yaml:"update"`
	Metrics        MetricsConfig        `yaml:"metrics"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit"`
	Logging        LoggingConfig        `yaml:"logging"`
	TrustedProxies TrustedProxiesConfig `yaml:"trusted_proxies"`
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

type RateLimitConfig struct {
	Enabled     bool                `yaml:"enabled"`
	Read        RateLimitRuleConfig `yaml:"read"`
	Write       RateLimitRuleConfig `yaml:"write"`
	Health      RateLimitRuleConfig `yaml:"health"`
	GlobalBurst int                 `yaml:"global_burst"`
}

func (c *RateLimitConfig) UnmarshalYAML(node *yaml.Node) error {
	fields := rateLimitConfigFields{Read: c.Read, Write: c.Write, Health: c.Health, GlobalBurst: c.GlobalBurst}
	if err := node.Decode(&fields); err != nil {
		return err
	}
	enabled, err := parseYAMLBool(mappingValue(node, "enabled"), c.Enabled)
	if err != nil {
		return fmt.Errorf("server.rate_limit.enabled: %w", err)
	}
	c.Enabled = enabled
	c.Read = fields.Read
	c.Write = fields.Write
	c.Health = fields.Health
	c.GlobalBurst = fields.GlobalBurst
	return nil
}

type rateLimitConfigFields struct {
	Read        RateLimitRuleConfig `yaml:"read"`
	Write       RateLimitRuleConfig `yaml:"write"`
	Health      RateLimitRuleConfig `yaml:"health"`
	GlobalBurst int                 `yaml:"global_burst"`
}

func (c *MetricsConfig) UnmarshalYAML(node *yaml.Node) error {
	fields := metricsConfigFields{Endpoint: c.Endpoint}
	if err := node.Decode(&fields); err != nil {
		return err
	}
	enabled, err := parseYAMLBool(mappingValue(node, "enabled"), c.Enabled)
	if err != nil {
		return fmt.Errorf("server.metrics.enabled: %w", err)
	}
	c.Enabled = enabled
	c.Endpoint = fields.Endpoint
	return nil
}

type metricsConfigFields struct {
	Endpoint string `yaml:"endpoint"`
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
	Enabled  bool   `yaml:"enabled"`
	Endpoint string `yaml:"endpoint"`
}

type LoggingConfig struct {
	AccessFormat string `yaml:"access_format"`
	Level        string `yaml:"level"`
}

type WebUIConfig struct {
	Theme         string              `yaml:"theme"`
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

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:           "8080",
			Database:       DatabaseConfig{Driver: "sqlite", URL: "{data_dir}/db/server.db"},
			FQDN:           "localhost",
			Address:        "0.0.0.0",
			TrustedProxies: TrustedProxiesConfig{Additional: []string{}},
			Mode:           "production",
			Update:         UpdateConfig{Branch: "stable"},
			Metrics: MetricsConfig{
				Enabled:  false,
				Endpoint: "/metrics",
			},
			RateLimit: RateLimitConfig{
				Enabled:     true,
				Read:        RateLimitRuleConfig{Requests: 120, Window: 60},
				Write:       RateLimitRuleConfig{Requests: 10, Window: 60},
				Health:      RateLimitRuleConfig{Requests: 120, Window: 60},
				GlobalBurst: 240,
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

	// Check if .yaml exists and .yml doesn't
	if _, err := os.Stat(yamlPath); err == nil {
		if _, err := os.Stat(ymlPath); os.IsNotExist(err) {
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
	// Migrate from .yaml to .yml if needed
	migrateYamlToYml(path)

	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Save default config if it doesn't exist
			if err := Save(path, cfg); err != nil {
				return cfg, fmt.Errorf("create default configuration: %w", err)
			}
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return cfg, err
	}
	validateRateLimitConfig(cfg)
	validateUpdateConfig(cfg)

	return cfg, nil
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
	if cfg.Server.RateLimit.GlobalBurst < 1 {
		log.Printf("Warning: invalid server.rate_limit.global_burst value; using default")
		cfg.Server.RateLimit.GlobalBurst = defaults.GlobalBurst
	}
}

func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}
