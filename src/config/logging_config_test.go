package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoggingKindsDecodeDocumentedOptions(t *testing.T) {
	cfg := DefaultConfig()
	input := []byte("server:\n  logs:\n    server:\n      filename: app-events.log\n      format: json\n      rotate: daily\n      keep: 4\n    error:\n      filename: failures.log\n      format: text\n    security:\n      filename: security-events.log\n      format: cef\n    debug:\n      enabled: true\n      format: json\n    audit:\n      group: operators\n      token_usage: true\n")
	if err := yaml.Unmarshal(input, cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Logging.Server.Filename != "app-events.log" || cfg.Server.Logging.Server.Format != "json" || cfg.Server.Logging.Server.Rotate != "daily" || cfg.Server.Logging.Server.Keep != "4" {
		t.Fatalf("server logging config = %+v", cfg.Server.Logging.Server)
	}
	if cfg.Server.Logging.Error.Filename != "failures.log" || cfg.Server.Logging.Security.Format != "cef" || !cfg.Server.Logging.Debug.Enabled || cfg.Server.Logging.Debug.Format != "json" || cfg.Server.Logging.Audit.Group != "operators" || !cfg.Server.Logging.Audit.TokenUsage {
		t.Fatalf("per-kind logging config = %+v", cfg.Server.Logging)
	}
}
