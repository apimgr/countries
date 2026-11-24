package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// EnsureGeneratedSecrets creates missing installation and signing secrets in
// one transaction without replacing values already restored from a backup.
func EnsureGeneratedSecrets(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin generated-secret initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, name := range []string{"installation_secret", "cookie_signing_key", "csrf_token_secret"} {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return fmt.Errorf("generate %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets (name, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`, name, base64.StdEncoding.EncodeToString(secret), now); err != nil {
			return fmt.Errorf("initialize %s: %w", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit generated-secret initialization: %w", err)
	}
	return nil
}

// GetAppSecret reads a server-generated value from app_secrets.
func GetAppSecret(ctx context.Context, db *sql.DB, name string) (string, bool, error) {
	if strings.TrimSpace(name) == "" {
		return "", false, fmt.Errorf("application secret name is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var value string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name = ?`, name).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read application secret: %w", err)
	}
	return value, true, nil
}

// SetAppSecret upserts a server-generated value with an RFC 3339 UTC timestamp.
func SetAppSecret(ctx context.Context, db *sql.DB, name, value string, updatedAt time.Time) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("application secret name is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `INSERT INTO app_secrets (name, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, name, value, updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write application secret: %w", err)
	}
	return nil
}

// InsertAppSecretIfAbsent imports a legacy secret without replacing a live value.
func InsertAppSecretIfAbsent(ctx context.Context, db *sql.DB, name, value string, updatedAt time.Time) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, fmt.Errorf("application secret name is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := db.ExecContext(ctx, `INSERT INTO app_secrets (name, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`, name, value, updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("import application secret: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check imported application secret: %w", err)
	}
	return rows == 1, nil
}

// DeleteAppSecret removes a server-generated value from app_secrets.
func DeleteAppSecret(ctx context.Context, db *sql.DB, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("application secret name is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DELETE FROM app_secrets WHERE name = ?`, name); err != nil {
		return fmt.Errorf("delete application secret: %w", err)
	}
	return nil
}
