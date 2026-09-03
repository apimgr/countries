package config

import (
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
	Port         string        `yaml:"port"`
	FQDN         string        `yaml:"fqdn"`
	Address      string        `yaml:"address"`
	Mode         string        `yaml:"mode"`
	UpdateBranch string        `yaml:"update_branch"`
	Metrics      MetricsConfig `yaml:"metrics"`
	Logging      LoggingConfig `yaml:"logging"`
	Admin        AdminConfig   `yaml:"admin"`
	Session      SessionConfig `yaml:"session"`
}

type AdminConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	APIToken string `yaml:"api_token"`
}

type SessionConfig struct {
	Timeout int `yaml:"timeout"`
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
	Admin string `yaml:"admin"`
	CORS  string `yaml:"cors"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         "8080",
			FQDN:         "localhost",
			Address:      "0.0.0.0",
			Mode:         "production",
			UpdateBranch: "stable",
			Metrics: MetricsConfig{
				Enabled:  false,
				Endpoint: "/metrics",
			},
			Logging: LoggingConfig{
				AccessFormat: "apache",
				Level:        "info",
			},
			Admin: AdminConfig{
				Username: "admin",
				Password: "",
				APIToken: "",
			},
			Session: SessionConfig{
				Timeout: 3600,
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
			Admin: "admin@example.com",
			CORS:  "*",
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
			_ = Save(path, cfg)
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func Save(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
