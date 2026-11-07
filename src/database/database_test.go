package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenCreatesSecureSchemaAndPersistsRateLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db", "server.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database permissions = %04o, want 0600", info.Mode().Perm())
	}

	db, err = Open(dbPath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	allowed, _, err := AllowRateLimit(ctx, db, "192.0.2.1", "read", now, 1, time.Minute, 10)
	if err != nil || !allowed {
		t.Fatalf("first request = (%v, %v), want allowed", allowed, err)
	}
	allowed, retry, err := AllowRateLimit(ctx, db, "192.0.2.1", "read", now.Add(time.Second), 1, time.Minute, 10)
	if err != nil || allowed || retry != 59*time.Second {
		t.Fatalf("second request = (%v, %v, %v), want denied for 59s", allowed, retry, err)
	}

	var triggerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'config_version_bump_%'`).Scan(&triggerCount); err != nil {
		t.Fatalf("count config triggers: %v", err)
	}
	if triggerCount != 3 {
		t.Fatalf("config trigger count = %d, want 3", triggerCount)
	}
}
