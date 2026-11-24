package database

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PGPKeyMetadata contains non-secret metadata for a project security key.
type PGPKeyMetadata struct {
	Fingerprint         string            `json:"fingerprint"`
	CreatedAt           time.Time         `json:"created_at"`
	ExpiresAt           time.Time         `json:"expires_at"`
	LastRotatedAt       *time.Time        `json:"last_rotated_at,omitempty"`
	KeyserversPublished map[string]string `json:"keyservers_published"`
	Revoked             bool              `json:"revoked"`
}

// SavePGPKeyMetadata stores the public metadata for a generated security key.
func SavePGPKeyMetadata(ctx context.Context, db *sql.DB, metadata PGPKeyMetadata) error {
	decodedFingerprint, err := hex.DecodeString(metadata.Fingerprint)
	if err != nil || len(decodedFingerprint) != 20 || metadata.Fingerprint != strings.ToUpper(metadata.Fingerprint) {
		return fmt.Errorf("OpenPGP fingerprint must be 40 uppercase hexadecimal characters")
	}
	if metadata.CreatedAt.IsZero() || metadata.ExpiresAt.IsZero() {
		return fmt.Errorf("OpenPGP key creation and expiry times are required")
	}
	if metadata.KeyserversPublished == nil {
		metadata.KeyserversPublished = map[string]string{}
	}
	published, err := json.Marshal(metadata.KeyserversPublished)
	if err != nil {
		return fmt.Errorf("encode OpenPGP keyserver metadata: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, `INSERT INTO pgp_keys (fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(fingerprint) DO UPDATE SET created_at = excluded.created_at, expires_at = excluded.expires_at, last_rotated_at = excluded.last_rotated_at, keyservers_published = excluded.keyservers_published, revoked = excluded.revoked`, metadata.Fingerprint, metadata.CreatedAt.UTC().Format(time.RFC3339Nano), metadata.ExpiresAt.UTC().Format(time.RFC3339Nano), nullableTime(metadata.LastRotatedAt), string(published), metadata.Revoked)
	if err != nil {
		return fmt.Errorf("save OpenPGP key metadata: %w", err)
	}
	return nil
}

// RecordPGPKeyserverPublication adds a successful publication timestamp to the active key metadata.
func RecordPGPKeyserverPublication(ctx context.Context, db *sql.DB, fingerprint, keyserver string, publishedAt time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OpenPGP publication update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var encoded string
	if err := tx.QueryRowContext(ctx, `SELECT keyservers_published FROM pgp_keys WHERE fingerprint = ? AND revoked = FALSE`, fingerprint).Scan(&encoded); err != nil {
		return fmt.Errorf("read active OpenPGP key metadata: %w", err)
	}
	published := map[string]string{}
	if err := json.Unmarshal([]byte(encoded), &published); err != nil {
		return fmt.Errorf("decode OpenPGP publication metadata: %w", err)
	}
	published[keyserver] = publishedAt.UTC().Format(time.RFC3339Nano)
	encodedBytes, err := json.Marshal(published)
	if err != nil {
		return fmt.Errorf("encode OpenPGP publication metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pgp_keys SET keyservers_published = ? WHERE fingerprint = ? AND revoked = FALSE`, string(encodedBytes), fingerprint); err != nil {
		return fmt.Errorf("save OpenPGP publication metadata: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit OpenPGP publication metadata: %w", err)
	}
	return nil
}

// RevokePGPKeyMetadata retains the historical fingerprint after key deletion.
func RevokePGPKeyMetadata(ctx context.Context, db *sql.DB, fingerprint string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := db.ExecContext(ctx, `UPDATE pgp_keys SET revoked = TRUE WHERE fingerprint = ? AND revoked = FALSE`, fingerprint)
	if err != nil {
		return fmt.Errorf("revoke OpenPGP key metadata: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check revoked OpenPGP metadata: %w", err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ClaimPGPPrivateExport atomically enforces one private-key export per operator per hour.
func ClaimPGPPrivateExport(ctx context.Context, db *sql.DB, operator string, now time.Time) (bool, time.Duration, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return false, 0, fmt.Errorf("PGP export operator is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin PGP export rate limit: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	key := "pgp:private-export:" + operator
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO rate_limits (key, count, window_start, updated_at) VALUES (?, 1, ?, ?)`, key, now.Unix(), now.Unix())
	if err != nil {
		return false, 0, fmt.Errorf("claim PGP export rate limit: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, 0, fmt.Errorf("inspect PGP export rate limit: %w", err)
	}
	if inserted > 0 {
		if err := tx.Commit(); err != nil {
			return false, 0, fmt.Errorf("commit PGP export rate limit: %w", err)
		}
		return true, 0, nil
	}
	var windowStart int64
	if err := tx.QueryRowContext(ctx, `SELECT window_start FROM rate_limits WHERE key = ?`, key).Scan(&windowStart); err != nil {
		return false, 0, fmt.Errorf("read PGP export rate limit: %w", err)
	}
	remaining := time.Hour - now.Sub(time.Unix(windowStart, 0))
	if remaining > 0 {
		return false, remaining, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rate_limits SET count = 1, window_start = ?, updated_at = ? WHERE key = ?`, now.Unix(), now.Unix(), key); err != nil {
		return false, 0, fmt.Errorf("update PGP export rate limit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit PGP export rate limit: %w", err)
	}
	return true, 0, nil
}

// GetPGPKeyMetadata returns metadata for the active key, or false when none exists.
func GetPGPKeyMetadata(ctx context.Context, db *sql.DB) (PGPKeyMetadata, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var metadata PGPKeyMetadata
	var createdAt, expiresAt string
	var rotatedAt sql.NullString
	var published string
	err := db.QueryRowContext(ctx, `SELECT fingerprint, created_at, expires_at, last_rotated_at, keyservers_published, revoked FROM pgp_keys WHERE revoked = FALSE ORDER BY created_at DESC LIMIT 1`).Scan(&metadata.Fingerprint, &createdAt, &expiresAt, &rotatedAt, &published, &metadata.Revoked)
	if err == sql.ErrNoRows {
		return PGPKeyMetadata{}, false, nil
	}
	if err != nil {
		return PGPKeyMetadata{}, false, fmt.Errorf("read OpenPGP key metadata: %w", err)
	}
	if metadata.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return PGPKeyMetadata{}, false, fmt.Errorf("parse OpenPGP creation time: %w", err)
	}
	if metadata.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return PGPKeyMetadata{}, false, fmt.Errorf("parse OpenPGP expiry time: %w", err)
	}
	if rotatedAt.Valid {
		rotated, parseErr := time.Parse(time.RFC3339Nano, rotatedAt.String)
		if parseErr != nil {
			return PGPKeyMetadata{}, false, fmt.Errorf("parse OpenPGP rotation time: %w", parseErr)
		}
		metadata.LastRotatedAt = &rotated
	}
	if err := json.Unmarshal([]byte(published), &metadata.KeyserversPublished); err != nil {
		return PGPKeyMetadata{}, false, fmt.Errorf("decode OpenPGP keyserver metadata: %w", err)
	}
	return metadata, true, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
