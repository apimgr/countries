// Package database opens and initializes the server's SQLite state database.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS app_secrets (name TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS captcha_challenges (id VARCHAR(32) PRIMARY KEY, answer_hash VARCHAR(64) NOT NULL, expires_at TIMESTAMP NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS idx_captcha_challenges_expires ON captcha_challenges(expires_at)`,
	`CREATE TABLE IF NOT EXISTS security_reports (tracking_id VARCHAR(32) PRIMARY KEY, security_id VARCHAR(64) NOT NULL DEFAULT '', id_window BIGINT NOT NULL DEFAULT 0, severity VARCHAR(16) NOT NULL, component VARCHAR(255) NOT NULL, status VARCHAR(16) NOT NULL DEFAULT 'received', body_enc BLOB NOT NULL, body_alg VARCHAR(16) NOT NULL, contact_enc BLOB, credit_pref VARCHAR(8) NOT NULL DEFAULT 'no', credit_name VARCHAR(100), researcher_comment TEXT NOT NULL DEFAULT '', disclosure_date DATE, cve_requested BOOLEAN NOT NULL DEFAULT FALSE, token_hash VARCHAR(64) NOT NULL, token_last_used DATE, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, closed_at TIMESTAMP)`,
	`CREATE INDEX IF NOT EXISTS idx_security_reports_status ON security_reports(status, created_at)`,
	`CREATE TABLE IF NOT EXISTS security_thanks (tracking_id VARCHAR(32) PRIMARY KEY, credit VARCHAR(120) NOT NULL, severity VARCHAR(16) NOT NULL, disclosed_on DATE NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS pgp_keys (fingerprint CHAR(40) PRIMARY KEY, created_at TIMESTAMP NOT NULL, expires_at TIMESTAMP NOT NULL, last_rotated_at TIMESTAMP, keyservers_published TEXT NOT NULL DEFAULT '[]', revoked BOOLEAN NOT NULL DEFAULT FALSE)`,
	`CREATE TABLE IF NOT EXISTS config (key TEXT PRIMARY KEY, value TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'string', updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')))`,
	`CREATE TABLE IF NOT EXISTS config_meta (id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL DEFAULT 1, updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')))`,
	`INSERT OR IGNORE INTO config_meta (id, version) VALUES (1, 1)`,
	`CREATE TRIGGER IF NOT EXISTS config_version_bump_insert AFTER INSERT ON config BEGIN UPDATE config_meta SET version = version + 1, updated_at = strftime('%s', 'now') WHERE id = 1; END`,
	`CREATE TRIGGER IF NOT EXISTS config_version_bump_update AFTER UPDATE ON config BEGIN UPDATE config_meta SET version = version + 1, updated_at = strftime('%s', 'now') WHERE id = 1; END`,
	`CREATE TRIGGER IF NOT EXISTS config_version_bump_delete AFTER DELETE ON config BEGIN UPDATE config_meta SET version = version + 1, updated_at = strftime('%s', 'now') WHERE id = 1; END`,
	`CREATE INDEX IF NOT EXISTS idx_config_key_prefix ON config(key)`,
	`CREATE TABLE IF NOT EXISTS rate_limits (key TEXT PRIMARY KEY, count INTEGER NOT NULL DEFAULT 1, window_start INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')))`,
	`CREATE INDEX IF NOT EXISTS idx_rate_limits_window ON rate_limits(window_start)`,
	`CREATE TABLE IF NOT EXISTS health_request_stats (id INTEGER PRIMARY KEY CHECK (id = 1), requests_total INTEGER NOT NULL DEFAULT 0)`,
	`INSERT OR IGNORE INTO health_request_stats (id, requests_total) VALUES (1, 0)`,
	`CREATE TABLE IF NOT EXISTS health_request_hours (hour INTEGER PRIMARY KEY, requests INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), level TEXT NOT NULL DEFAULT 'info', category TEXT NOT NULL, action TEXT NOT NULL, actor_ip TEXT, target_type TEXT, target_id TEXT, details TEXT, success INTEGER NOT NULL DEFAULT 1)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_category ON audit_log(category)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_actor_ip ON audit_log(actor_ip)`,
	`CREATE INDEX IF NOT EXISTS idx_audit_target ON audit_log(target_type, target_id)`,
	`CREATE TABLE IF NOT EXISTS scheduler_tasks (id TEXT PRIMARY KEY, name TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, schedule TEXT NOT NULL, last_run INTEGER, next_run INTEGER, last_status TEXT, last_error TEXT, run_count INTEGER NOT NULL DEFAULT 0, fail_count INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS scheduler_history (id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, started_at INTEGER NOT NULL, finished_at INTEGER, status TEXT NOT NULL, error TEXT, duration_ms INTEGER)`,
	`CREATE INDEX IF NOT EXISTS idx_scheduler_history_task ON scheduler_history(task_id)`,
	`CREATE INDEX IF NOT EXISTS idx_scheduler_history_started ON scheduler_history(started_at)`,
	`CREATE TABLE IF NOT EXISTS backups (id INTEGER PRIMARY KEY AUTOINCREMENT, filename TEXT NOT NULL UNIQUE, filepath TEXT NOT NULL, size_bytes INTEGER NOT NULL, type TEXT NOT NULL DEFAULT 'auto', created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), checksum TEXT, notes TEXT)`,
	`CREATE INDEX IF NOT EXISTS idx_backups_created ON backups(created_at)`,
	`CREATE TABLE IF NOT EXISTS owners (id TEXT PRIMARY KEY, created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')))`,
	`CREATE TABLE IF NOT EXISTS api_tokens (id TEXT PRIMARY KEY NOT NULL, owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE, kind TEXT NOT NULL CHECK (kind IN ('primary', 'linked')), name TEXT, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL, token_last4 TEXT NOT NULL DEFAULT '', token_enc TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), rotated_at INTEGER, expires_at INTEGER, last_used_at INTEGER, revoked_at INTEGER, revoked_reason TEXT)`,
}

// Open initializes the configured SQLite file and applies the idempotent schema.
func Open(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	db, err := OpenSQLiteDatabase(path, 25)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	return db, nil
}

// EnsureSchema applies all schema statements safely on every startup.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for _, statement := range schema {
		if err := Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement)
			return err
		}); err != nil {
			return fmt.Errorf("initialize server database schema: %w", err)
		}
	}
	if err := ensureTokenOwner(ctx, db); err != nil {
		return fmt.Errorf("migrate API token ownership schema: %w", err)
	}
	if err := ensureTokenPublicID(ctx, db); err != nil {
		return fmt.Errorf("migrate API token public identifiers: %w", err)
	}
	if err := ensureTokenLast4(ctx, db); err != nil {
		return fmt.Errorf("migrate API token metadata schema: %w", err)
	}
	// Schema update 7107920 adds the canonical report window and erases legacy rotating identifiers.
	if err := Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE security_reports ADD COLUMN id_window BIGINT NOT NULL DEFAULT 0`)
		return err
	}); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("add security report window column: %w", err)
	}
	legacySecurityID, err := hasColumn(ctx, db, "security_reports", "security_id")
	if err != nil {
		return fmt.Errorf("inspect legacy security report metadata: %w", err)
	}
	if legacySecurityID {
		if err := Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE security_reports SET id_window = COALESCE(CAST(strftime('%s', created_at) / 172800 AS INTEGER), 0), security_id = '' WHERE id_window = 0 AND security_id <> ''`)
			return err
		}); err != nil {
			return fmt.Errorf("migrate security report window metadata: %w", err)
		}
	}
	return nil
}

func ensureTokenPublicID(ctx context.Context, db *sql.DB) error {
	idType, err := columnType(ctx, db, "api_tokens", "id")
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToUpper(idType), "INT") {
		return nil
	}
	hasPublicID, err := hasColumn(ctx, db, "api_tokens", "public_id")
	if err != nil {
		return err
	}
	if !hasPublicID {
		if err := Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN public_id TEXT`)
			return err
		}); err != nil {
			return err
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM api_tokens WHERE public_id IS NULL OR public_id = '' ORDER BY id`)
	if err != nil {
		return err
	}
	var internalIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		internalIDs = append(internalIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, internalID := range internalIDs {
		publicID, err := newUUIDv7(time.Now())
		if err != nil {
			return err
		}
		if err := Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET public_id = ? WHERE id = ? AND (public_id IS NULL OR public_id = '')`, publicID, internalID)
			return err
		}); err != nil {
			return err
		}
	}
	return Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_public_id ON api_tokens(public_id)`)
		return err
	})
}

func columnType(ctx context.Context, db *sql.DB, table, column string) (string, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return "", err
		}
		if name == column {
			return dataType, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("column %s.%s does not exist", table, column)
}

func ensureTokenOwner(ctx context.Context, db *sql.DB) error {
	hasOwner, err := hasColumn(ctx, db, "api_tokens", "owner_id")
	if err != nil {
		return err
	}
	if !hasOwner {
		if err := Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN owner_id TEXT REFERENCES owners(id)`)
			return err
		}); err != nil {
			return err
		}
	}
	var rowsToBackfill int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id IS NULL OR owner_id = ''`).Scan(&rowsToBackfill); err != nil {
		return err
	}
	if rowsToBackfill > 0 {
		if err := Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO owners(id) VALUES('system')`)
			return err
		}); err != nil {
			return err
		}
	}
	if err := Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE api_tokens SET owner_id = 'system' WHERE owner_id IS NULL OR owner_id = ''`)
		return err
	}); err != nil {
		return err
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_primary ON api_tokens(owner_id) WHERE kind = 'primary' AND revoked_at IS NULL`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(token_prefix)`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(revoked_at) WHERE revoked_at IS NULL`,
	} {
		if err := Write(ctx, db, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, statement); return err }); err != nil {
			return err
		}
	}
	return nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	found := false
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&index, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, errors.Join(err, rows.Close())
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	return found, nil
}
