package database

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/paths"
)

func TestEnsureGeneratedSecretsIsAtomicAndIdempotent(t *testing.T) {
	directory, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatalf("create project temp directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	db, err := Open(filepath.Join(directory, "server.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := EnsureGeneratedSecrets(ctx, db); err != nil {
		t.Fatalf("initialize generated secrets: %v", err)
	}
	values := make(map[string]string)
	for _, name := range []string{"installation_secret", "cookie_signing_key", "csrf_token_secret"} {
		value, found, err := GetAppSecret(ctx, db, name)
		if err != nil || !found {
			t.Fatalf("GetAppSecret(%q) = (%t, %v), want stored secret", name, found, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("secret %q is not a 32-byte base64 value", name)
		}
		values[name] = value
	}
	if err := EnsureGeneratedSecrets(ctx, db); err != nil {
		t.Fatalf("reinitialize generated secrets: %v", err)
	}
	for name, want := range values {
		got, found, err := GetAppSecret(ctx, db, name)
		if err != nil || !found || got != want {
			t.Errorf("secret %q changed across initialization: got (%q, %t, %v), want original value", name, got, found, err)
		}
	}
}

func TestAppSecretLifecycle(t *testing.T) {
	directory, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatalf("create project temp directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove project temp directory: %v", err)
		}
	})
	db, err := Open(filepath.Join(directory, "server.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	value, found, err := GetAppSecret(ctx, db, "dns_validated_at")
	if err != nil || found || value != "" {
		t.Fatalf("GetAppSecret() = (%q, %t, %v), want empty and not found", value, found, err)
	}
	updatedAt := time.Date(2026, time.November, 22, 15, 30, 0, 0, time.FixedZone("test", -5*60*60))
	if err := SetAppSecret(ctx, db, "dns_validated_at", "2026-11-22T20:30:00Z", updatedAt); err != nil {
		t.Fatalf("SetAppSecret() error = %v", err)
	}
	value, found, err = GetAppSecret(ctx, db, "dns_validated_at")
	if err != nil || !found || value != "2026-11-22T20:30:00Z" {
		t.Fatalf("GetAppSecret() = (%q, %t, %v), want stored value", value, found, err)
	}
	var storedUpdate string
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM app_secrets WHERE name = ?`, "dns_validated_at").Scan(&storedUpdate); err != nil {
		t.Fatalf("read update timestamp: %v", err)
	}
	if storedUpdate != "2026-11-22T20:30:00Z" {
		t.Errorf("stored timestamp = %q, want UTC RFC3339 value", storedUpdate)
	}
	if inserted, err := InsertAppSecretIfAbsent(ctx, db, "dns_validated_at", "replacement", updatedAt); err != nil || inserted {
		t.Fatalf("InsertAppSecretIfAbsent(existing) = (%t, %v), want no replacement", inserted, err)
	}
	if inserted, err := InsertAppSecretIfAbsent(ctx, db, "legacy_secret", "imported", updatedAt); err != nil || !inserted {
		t.Fatalf("InsertAppSecretIfAbsent(missing) = (%t, %v), want import", inserted, err)
	}
	if value, found, err := GetAppSecret(ctx, db, "legacy_secret"); err != nil || !found || value != "imported" {
		t.Fatalf("GetAppSecret(legacy_secret) = (%q, %t, %v), want imported value", value, found, err)
	}
	if err := SetAppSecret(ctx, db, "dns_validated_at", "2026-11-23T00:00:00Z", updatedAt.Add(time.Hour)); err != nil {
		t.Fatalf("update AppSecret() error = %v", err)
	}
	value, found, err = GetAppSecret(ctx, db, "dns_validated_at")
	if err != nil || !found || value != "2026-11-23T00:00:00Z" {
		t.Fatalf("updated GetAppSecret() = (%q, %t, %v), want updated value", value, found, err)
	}
	if err := DeleteAppSecret(ctx, db, "dns_validated_at"); err != nil {
		t.Fatalf("DeleteAppSecret() error = %v", err)
	}
	if _, found, err := GetAppSecret(ctx, db, "dns_validated_at"); err != nil || found {
		t.Fatalf("GetAppSecret() after delete = (found=%t, %v), want not found", found, err)
	}
}
