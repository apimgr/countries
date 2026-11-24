// Package database opens and initializes the server's SQLite state database.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

var eventSequence atomic.Uint64

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
	`CREATE TABLE IF NOT EXISTS api_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE, kind TEXT NOT NULL CHECK (kind IN ('primary', 'linked')), name TEXT, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL, token_last4 TEXT NOT NULL DEFAULT '', token_enc TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')), rotated_at INTEGER, expires_at INTEGER, last_used_at INTEGER, revoked_at INTEGER, revoked_reason TEXT)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_api_tokens_primary ON api_tokens(owner_id) WHERE kind = 'primary' AND revoked_at IS NULL`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_hash ON api_tokens(token_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(token_prefix)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_id)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(revoked_at) WHERE revoked_at IS NULL`,
}

// Open initializes the configured SQLite file and applies the idempotent schema.
func Open(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: absPath}).String() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("countries-sqlite-instrumented", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.PingContext(pingCtx)
	cancelPing()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("verify SQLite database: %w", err)
	}
	if err := EnsureSchema(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(absPath, 0600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure database file: %w", err)
	}
	return db, nil
}

// EnsureSchema applies all schema statements safely on every startup.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for _, statement := range schema {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize server database schema: %w", err)
		}
	}
	if err := ensureTokenLast4(ctx, db); err != nil {
		return fmt.Errorf("migrate API token metadata schema: %w", err)
	}
	// Schema update 7107920 adds the canonical report window and erases legacy rotating identifiers.
	if _, err := db.ExecContext(ctx, `ALTER TABLE security_reports ADD COLUMN id_window BIGINT NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("add security report window column: %w", err)
	}
	legacySecurityID, err := hasColumn(ctx, db, "security_reports", "security_id")
	if err != nil {
		return fmt.Errorf("inspect legacy security report metadata: %w", err)
	}
	if legacySecurityID {
		if _, err := db.ExecContext(ctx, `UPDATE security_reports SET id_window = COALESCE(CAST(strftime('%s', created_at) / 172800 AS INTEGER), 0), security_id = '' WHERE id_window = 0 AND security_id <> ''`); err != nil {
			return fmt.Errorf("migrate security report window metadata: %w", err)
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

// AllowRateLimit persists a request event and checks the class and global sliding windows.
func AllowRateLimit(ctx context.Context, db *sql.DB, client, class string, now time.Time, classRequests int, classWindow time.Duration, globalBurst int) (bool, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	classPrefix := "ip:" + client + ":" + class + ":"
	globalPrefix := "ip:" + client + ":global:"
	classCount, classFirst, err := countEvents(ctx, tx, classPrefix, now.Add(-classWindow))
	if err != nil {
		return false, 0, err
	}
	globalWindow := time.Minute
	globalCount, globalFirst, err := countEvents(ctx, tx, globalPrefix, now.Add(-globalWindow))
	if err != nil {
		return false, 0, err
	}
	if classCount >= classRequests {
		return false, maxRetry(classFirst.Add(classWindow).Sub(now)), nil
	}
	if globalCount >= globalBurst {
		return false, maxRetry(globalFirst.Add(globalWindow).Sub(now)), nil
	}
	nowUnix := now.Unix()
	key := fmt.Sprintf("%s%d-%d", classPrefix, now.UnixNano(), eventSequence.Add(1))
	if _, err := tx.ExecContext(ctx, `INSERT INTO rate_limits (key, count, window_start, updated_at) VALUES (?, 1, ?, ?)`, key, nowUnix, nowUnix); err != nil {
		return false, 0, err
	}
	globalKey := fmt.Sprintf("%s%d-%d", globalPrefix, now.UnixNano(), eventSequence.Add(1))
	if _, err := tx.ExecContext(ctx, `INSERT INTO rate_limits (key, count, window_start, updated_at) VALUES (?, 1, ?, ?)`, globalKey, nowUnix, nowUnix); err != nil {
		return false, 0, err
	}
	if now.Second() == 0 {
		_, _ = tx.ExecContext(ctx, `DELETE FROM rate_limits WHERE window_start < ?`, now.Add(-time.Hour).Unix())
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, 0, nil
}

// CheckRateLimit reports whether a class-specific event limit has been reached.
func CheckRateLimit(ctx context.Context, db *sql.DB, client, class string, now time.Time, requests int, window time.Duration) (bool, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var count int
	var first int64
	prefix := "ip:" + client + ":" + class + ":"
	cutoff := now.Add(-window)
	err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(window_start), 0) FROM rate_limits WHERE key LIKE ? AND window_start >= ?`, prefix+"%", cutoff.Unix()).Scan(&count, &first)
	if err != nil {
		return false, 0, err
	}
	if count < requests {
		return false, 0, nil
	}
	return true, maxRetry(time.Unix(first, 0).Add(window).Sub(now)), nil
}

// RecordRateLimitClass stores one event without consuming the global request bucket.
func RecordRateLimitClass(ctx context.Context, db *sql.DB, client, class string, now time.Time, requests int, window time.Duration) (bool, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	prefix := "ip:" + client + ":" + class + ":"
	var count int
	var first int64
	cutoff := now.Add(-window)
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(window_start), 0) FROM rate_limits WHERE key LIKE ? AND window_start >= ?`, prefix+"%", cutoff.Unix()).Scan(&count, &first); err != nil {
		return false, 0, err
	}
	if count >= requests {
		return false, maxRetry(time.Unix(first, 0).Add(window).Sub(now)), nil
	}
	nowUnix := now.Unix()
	key := fmt.Sprintf("%s%d-%d", prefix, now.UnixNano(), eventSequence.Add(1))
	if _, err := tx.ExecContext(ctx, `INSERT INTO rate_limits (key, count, window_start, updated_at) VALUES (?, 1, ?, ?)`, key, nowUnix, nowUnix); err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	if count+1 >= requests {
		return false, maxRetry(time.Unix(first, 0).Add(window).Sub(now)), nil
	}
	return true, 0, nil
}

func countEvents(ctx context.Context, tx *sql.Tx, prefix string, cutoff time.Time) (int, time.Time, error) {
	var count int
	var first int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(window_start), 0) FROM rate_limits WHERE key LIKE ? AND window_start >= ?`, prefix+"%", cutoff.Unix()).Scan(&count, &first)
	return count, time.Unix(first, 0), err
}

func maxRetry(duration time.Duration) time.Duration {
	if duration <= 0 {
		return time.Second
	}
	return duration
}
