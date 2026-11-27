package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
)

func TestOpenCreatesSecureSchema(t *testing.T) {
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
	var rateLimitCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rate_limits`).Scan(&rateLimitCount); err != nil {
		t.Fatalf("count initial rate limits: %v", err)
	}
	if rateLimitCount != 0 {
		t.Fatalf("initial rate limit rows = %d, want 0", rateLimitCount)
	}

	var triggerCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'config_version_bump_%'`).Scan(&triggerCount); err != nil {
		t.Fatalf("count config triggers: %v", err)
	}
	if triggerCount != 3 {
		t.Fatalf("config trigger count = %d, want 3", triggerCount)
	}
	var tokenIDType string
	if err := db.QueryRowContext(ctx, `SELECT type FROM pragma_table_info('api_tokens') WHERE name = 'id'`).Scan(&tokenIDType); err != nil {
		t.Fatalf("inspect token identifier type: %v", err)
	}
	if tokenIDType != "TEXT" {
		t.Fatalf("fresh api_tokens.id type = %q, want TEXT", tokenIDType)
	}
	var publicIDColumnCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('api_tokens') WHERE name = 'public_id'`).Scan(&publicIDColumnCount); err != nil {
		t.Fatalf("inspect public token identifier column: %v", err)
	}
	if publicIDColumnCount != 0 {
		t.Fatalf("fresh schema has public_id column count %d, want 0", publicIDColumnCount)
	}
}

func TestEnsureTokenPublicIDMigratesLegacyIntegerKeys(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(testutil.TempDir(t), "legacy-tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_id TEXT NOT NULL, kind TEXT NOT NULL, name TEXT, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL, token_last4 TEXT NOT NULL DEFAULT '', token_enc TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT 0, rotated_at INTEGER, expires_at INTEGER, last_used_at INTEGER, revoked_at INTEGER, revoked_reason TEXT)`,
		`INSERT INTO api_tokens(id,owner_id,kind,token_hash,token_prefix,token_enc) VALUES(4,'legacy-owner','primary','hash-a','prefix-a','cipher-a'),(9,'legacy-owner','linked','hash-b','prefix-b','cipher-b')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("prepare legacy token schema: %v", err)
		}
	}
	ctx := context.Background()
	if err := ensureTokenPublicID(ctx, db); err != nil {
		t.Fatalf("ensureTokenPublicID(): %v", err)
	}
	ids := make(map[int64]string)
	rows, err := db.QueryContext(ctx, `SELECT id, public_id FROM api_tokens ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var internalID int64
		var publicID string
		if err := rows.Scan(&internalID, &publicID); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if !ValidTokenID(publicID) {
			t.Errorf("legacy row %d public_id = %q, want UUIDv7", internalID, publicID)
		}
		ids[internalID] = publicID
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[4] == "" || ids[9] == "" || ids[4] == ids[9] {
		t.Fatalf("backfilled public IDs = %#v, want unique IDs for internal rows 4 and 9", ids)
	}
	var internalIDType string
	if err := db.QueryRowContext(ctx, `SELECT type FROM pragma_table_info('api_tokens') WHERE name = 'id'`).Scan(&internalIDType); err != nil {
		t.Fatal(err)
	}
	if internalIDType != "INTEGER" {
		t.Fatalf("legacy api_tokens.id type = %q, want unchanged INTEGER", internalIDType)
	}
	if err := ensureTokenPublicID(ctx, db); err != nil {
		t.Fatalf("repeat ensureTokenPublicID(): %v", err)
	}
	var afterRepeat string
	if err := db.QueryRowContext(ctx, `SELECT public_id FROM api_tokens WHERE id = 4`).Scan(&afterRepeat); err != nil {
		t.Fatal(err)
	}
	if afterRepeat != ids[4] {
		t.Fatalf("repeat migration changed a public ID from %q to %q", ids[4], afterRepeat)
	}
	if err := Write(ctx, db, func(tx *sql.Tx) error {
		return insertTokenRow(ctx, tx, "019b3e57-4f80-7000-8000-000000000001", "legacy-owner", "linked", "new", "hash-c", "prefix-c", "last4", "cipher-c", nil)
	}); err != nil {
		t.Fatalf("insert into legacy schema: %v", err)
	}
	var insertedID string
	if err := db.QueryRowContext(ctx, `SELECT public_id FROM api_tokens WHERE token_hash = 'hash-c'`).Scan(&insertedID); err != nil {
		t.Fatal(err)
	}
	if insertedID != "019b3e57-4f80-7000-8000-000000000001" {
		t.Fatalf("new legacy-schema public_id = %q", insertedID)
	}
	var insertedInternalID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM api_tokens WHERE token_hash = 'hash-c'`).Scan(&insertedInternalID); err != nil {
		t.Fatal(err)
	}
	if insertedInternalID != 10 {
		t.Fatalf("new legacy internal key = %d, want next internal key 10", insertedInternalID)
	}
	if _, err := db.ExecContext(ctx, `UPDATE api_tokens SET public_id = ? WHERE id = 9`, ids[4]); err == nil {
		t.Fatal("legacy public_id unique index accepted a duplicate")
	}
}

func TestTokenPublicIDColumnDoesNotFallBackToLegacyInteger(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(testutil.TempDir(t), "unmigrated-tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if column, err := PublicTokenIDColumn(context.Background(), db); err == nil {
		t.Fatalf("unmigrated legacy public token column = %q, want error", column)
	}
}

func TestEnsureTokenOwnerMigratesExistingRowsWithoutRebuildingTable(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(testutil.TempDir(t), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE owners (id TEXT PRIMARY KEY)`,
		`CREATE TABLE api_tokens (id INTEGER PRIMARY KEY, kind TEXT NOT NULL, token_hash TEXT NOT NULL, token_prefix TEXT NOT NULL, revoked_at INTEGER)`,
		`INSERT INTO api_tokens(id,kind,token_hash,token_prefix) VALUES(1,'primary','hash','prefix')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("execute legacy schema: %v", err)
		}
	}
	if err := ensureTokenOwner(context.Background(), db); err != nil {
		t.Fatalf("ensureTokenOwner() error = %v", err)
	}
	var ownerID string
	if err := db.QueryRow(`SELECT owner_id FROM api_tokens WHERE id=1`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerID != "system" {
		t.Fatalf("migrated owner_id = %q, want system", ownerID)
	}
	var nullable int
	if err := db.QueryRow(`SELECT "notnull" FROM pragma_table_info('api_tokens') WHERE name='owner_id'`).Scan(&nullable); err != nil {
		t.Fatal(err)
	}
	if nullable != 0 {
		t.Fatalf("migrated owner_id notnull = %d, want nullable compatibility column", nullable)
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
