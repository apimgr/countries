package database

import "testing"

func TestDatabaseIdentifier(t *testing.T) {
	for _, test := range []struct {
		driver, name, want string
	}{
		{"sqlite", "api_tokens", "api_tokens"},
		{"libsql", "api_tokens", "srv_api_tokens"},
		{"turso", "idx_api_tokens_hash", "srv_idx_api_tokens_hash"},
		{"libsql", "srv_api_tokens", "srv_api_tokens"},
		{"libsql", "token_hash", "token_hash"},
	} {
		if got := DatabaseIdentifier(test.driver, test.name); got != test.want {
			t.Errorf("DatabaseIdentifier(%q, %q) = %q, want %q", test.driver, test.name, got, test.want)
		}
	}
}

func TestRewriteRemoteSQL(t *testing.T) {
	for _, test := range []struct{ query, want string }{
		{"SELECT * FROM api_tokens JOIN owners ON api_tokens.owner_id = owners.id", "SELECT * FROM srv_api_tokens JOIN srv_owners ON api_tokens.owner_id = owners.id"},
		{"CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)", "CREATE INDEX IF NOT EXISTS srv_idx_api_tokens_hash ON srv_api_tokens(token_hash)"},
		{"PRAGMA table_info('api_tokens')", "PRAGMA table_info('srv_api_tokens')"},
		{"SELECT name FROM pragma_table_info('api_tokens')", "SELECT name FROM pragma_table_info('srv_api_tokens')"},
		{"SELECT 'api_tokens' AS value -- FROM owners\nFROM api_tokens", "SELECT 'api_tokens' AS value -- FROM owners\nFROM srv_api_tokens"},
		{"UPDATE api_tokens SET token_hash = ? WHERE id = ?", "UPDATE srv_api_tokens SET token_hash = ? WHERE id = ?"},
		{"CREATE TRIGGER cfg_insert AFTER INSERT ON config BEGIN UPDATE config_meta SET version = version + 1; END", "CREATE TRIGGER cfg_insert AFTER INSERT ON srv_config BEGIN UPDATE srv_config_meta SET version = version + 1; END"},
		{"SELECT token_hash FROM api_tokens ON CONFLICT DO NOTHING", "SELECT token_hash FROM srv_api_tokens ON CONFLICT DO NOTHING"},
	} {
		if got := rewriteRemoteSQL(test.query); got != test.want {
			t.Errorf("rewriteRemoteSQL(%q) = %q, want %q", test.query, got, test.want)
		}
	}
}
