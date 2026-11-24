package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
)

func TestOpenCreatesSecureSchemaAndPersistsRateLimit(t *testing.T) {
	dbPath := filepath.Join(testutil.TempDir(t), "db", "server.db")
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
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

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

func TestOpenCreatesContactAndSecurityReportTables(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	for _, table := range []string{"app_secrets", "captcha_challenges", "security_reports", "security_thanks", "pgp_keys"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatalf("lookup %s table: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s exists = %t, want true", table, count == 1)
		}
	}
	var windowColumnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('security_reports') WHERE name = 'id_window' AND type = 'BIGINT' AND "notnull" = 1`).Scan(&windowColumnCount); err != nil {
		t.Fatalf("inspect security report window column: %v", err)
	}
	if windowColumnCount != 1 {
		t.Fatalf("security_reports.id_window canonical schema count = %d, want 1", windowColumnCount)
	}
}

func TestPGPKeyMetadataRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	rotated := now.Add(time.Hour)
	want := PGPKeyMetadata{
		Fingerprint:         "0123456789ABCDEF0123456789ABCDEF01234567",
		CreatedAt:           now,
		ExpiresAt:           now.Add(2 * 365 * 24 * time.Hour),
		LastRotatedAt:       &rotated,
		KeyserversPublished: map[string]string{"keys.openpgp.org": now.Format(time.RFC3339Nano)},
	}
	if err := SavePGPKeyMetadata(context.Background(), db, want); err != nil {
		t.Fatalf("SavePGPKeyMetadata() error = %v", err)
	}
	got, exists, err := GetPGPKeyMetadata(context.Background(), db)
	if err != nil || !exists {
		t.Fatalf("GetPGPKeyMetadata() = (%+v, %v, %v)", got, exists, err)
	}
	if got.Fingerprint != want.Fingerprint || !got.CreatedAt.Equal(want.CreatedAt) || !got.ExpiresAt.Equal(want.ExpiresAt) || got.LastRotatedAt == nil || !got.LastRotatedAt.Equal(rotated) || got.KeyserversPublished["keys.openpgp.org"] != want.KeyserversPublished["keys.openpgp.org"] {
		t.Fatalf("GetPGPKeyMetadata() = %+v, want %+v", got, want)
	}
	publishedAt := now.Add(2 * time.Hour)
	if err := RecordPGPKeyserverPublication(context.Background(), db, want.Fingerprint, "keyserver.ubuntu.com", publishedAt); err != nil {
		t.Fatalf("RecordPGPKeyserverPublication() error = %v", err)
	}
	got, exists, err = GetPGPKeyMetadata(context.Background(), db)
	if err != nil || !exists || got.KeyserversPublished["keyserver.ubuntu.com"] != publishedAt.Format(time.RFC3339Nano) {
		t.Fatalf("published key metadata = (%+v, %v, %v)", got, exists, err)
	}
	if err := RevokePGPKeyMetadata(context.Background(), db, want.Fingerprint); err != nil {
		t.Fatalf("RevokePGPKeyMetadata() error = %v", err)
	}
	if _, exists, err := GetPGPKeyMetadata(context.Background(), db); err != nil || exists {
		t.Fatalf("revoked key metadata lookup = (%v, %v), want missing", exists, err)
	}
}

func TestOpenMigratesLegacySecurityReportWindowAndErasesIdentifier(t *testing.T) {
	databasePath := filepath.Join(testutil.TempDir(t), "legacy-security.db")
	db, err := Open(databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	createdAt := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO security_reports(tracking_id,security_id,severity,component,body_enc,body_alg,token_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`, "sec_0123456789abcdef", "0123456789abcdef", "high", "api", []byte("ciphertext"), "aes-256-gcm", "token-hash", createdAt.Format(time.RFC3339)); err != nil {
		t.Fatalf("insert legacy security report: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO security_reports(tracking_id,security_id,severity,component,body_enc,body_alg,token_hash,created_at) VALUES(?,?,?,?,?,?,?,?)`, "sec_fedcba9876543210", "fedcba9876543210", "medium", "legacy", []byte("ciphertext"), "aes-256-gcm", "token-hash-2", "not a timestamp"); err != nil {
		t.Fatalf("insert malformed legacy security report: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE security_reports DROP COLUMN id_window`); err != nil {
		t.Fatalf("prepare legacy schema fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	db, err = Open(databasePath)
	if err != nil {
		t.Fatalf("Open() legacy database error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var gotWindow int64
	var storedIdentifier string
	if err := db.QueryRow(`SELECT id_window, security_id FROM security_reports WHERE tracking_id = ?`, "sec_0123456789abcdef").Scan(&gotWindow, &storedIdentifier); err != nil {
		t.Fatalf("read migrated security report: %v", err)
	}
	if gotWindow != createdAt.Unix()/172800 {
		t.Errorf("migrated id_window = %d, want %d", gotWindow, createdAt.Unix()/172800)
	}
	if storedIdentifier != "" {
		t.Errorf("legacy security identifier remains stored: %q", storedIdentifier)
	}
	var fallbackWindow int64
	var fallbackIdentifier string
	if err := db.QueryRow(`SELECT id_window, security_id FROM security_reports WHERE tracking_id = ?`, "sec_fedcba9876543210").Scan(&fallbackWindow, &fallbackIdentifier); err != nil {
		t.Fatalf("read malformed legacy report: %v", err)
	}
	if fallbackWindow != 0 || fallbackIdentifier != "" {
		t.Errorf("malformed legacy report migration = (window=%d, id=%q), want (0, empty)", fallbackWindow, fallbackIdentifier)
	}
}

func TestRecordAndCheckRateLimitClass(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	ctx := context.Background()
	now := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	for attempt := 0; attempt < 3; attempt++ {
		allowed, retry, err := RecordRateLimitClass(ctx, db, "192.0.2.8", "auth_failures", now, 3, 15*time.Minute)
		if err != nil {
			t.Fatalf("RecordRateLimitClass(%d): %v", attempt+1, err)
		}
		if attempt < 2 && !allowed {
			t.Fatalf("attempt %d unexpectedly blocked for %s", attempt+1, retry)
		}
		if attempt == 2 && (allowed || retry != 15*time.Minute) {
			t.Fatalf("threshold attempt = (%v, %s), want blocked for 15m", allowed, retry)
		}
	}
	blocked, retry, err := CheckRateLimit(ctx, db, "192.0.2.8", "auth_failures", now, 3, 15*time.Minute)
	if err != nil || !blocked || retry != 15*time.Minute {
		t.Fatalf("CheckRateLimit() = (%v, %s, %v), want blocked for 15m", blocked, retry, err)
	}
	blocked, _, err = CheckRateLimit(ctx, db, "192.0.2.8", "auth_failures", now.Add(16*time.Minute), 3, 15*time.Minute)
	if err != nil || blocked {
		t.Fatalf("expired failure window = (%v, %v), want not blocked", blocked, err)
	}
}
