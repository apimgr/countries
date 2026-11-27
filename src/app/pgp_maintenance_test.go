package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/securitykey"
	"github.com/apimgr/countries/src/testutil"
)

func TestReencryptPGPReports(t *testing.T) {
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	oldPair, err := securitykey.Generate("Countries Security", "security@example.test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	newPair, err := securitykey.Generate("Countries Security", "security@example.test", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("retained report contents")
	ciphertext, err := securitykey.EncryptMessage(oldPair.Public, plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO security_reports (tracking_id, severity, component, body_enc, body_alg, token_hash) VALUES (?, ?, ?, ?, 'pgp', ?)`, "sec_0123456789abcdef", "high", "api", ciphertext, "test-hash"); err != nil {
		t.Fatal(err)
	}
	if err := database.Write(context.Background(), db, func(tx *sql.Tx) error {
		return reencryptPGPReports(context.Background(), tx, oldPair.Private, newPair.Public)
	}); err != nil {
		t.Fatalf("reencryptPGPReports() error = %v", err)
	}
	var updated []byte
	if err := db.QueryRowContext(context.Background(), `SELECT body_enc FROM security_reports WHERE tracking_id = ?`, "sec_0123456789abcdef").Scan(&updated); err != nil {
		t.Fatal(err)
	}
	decrypted, err := securitykey.DecryptMessage(newPair.Private, updated)
	if err != nil || string(decrypted) != string(plain) {
		t.Fatalf("rotated report decrypt = (%q, %v), want (%q, nil)", decrypted, err, plain)
	}
	if _, err := securitykey.DecryptMessage(oldPair.Private, updated); err == nil {
		t.Fatal("rotated report remained decryptable with the retired key")
	}
}

func TestReencryptPGPReportsAbortsOnInvalidCiphertext(t *testing.T) {
	db, err := database.Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	oldPair, err := securitykey.Generate("Countries Security", "security@example.test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	newPair, err := securitykey.Generate("Countries Security", "security@example.test", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	badBody := []byte("invalid ciphertext")
	goodBody, err := securitykey.EncryptMessage(oldPair.Public, []byte("must roll back"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO security_reports (tracking_id, severity, component, body_enc, body_alg, token_hash) VALUES (?, ?, ?, ?, 'pgp', ?)`, "sec_aaaaaaaaaaaaaaaa", "high", "api", goodBody, "test-hash-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO security_reports (tracking_id, severity, component, body_enc, body_alg, token_hash) VALUES (?, ?, ?, ?, 'pgp', ?)`, "sec_zzzzzzzzzzzzzzzz", "high", "api", badBody, "test-hash-2"); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close database connection: %v", err)
		}
	})
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := reencryptPGPReports(context.Background(), tx, oldPair.Private, newPair.Public); err == nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			t.Errorf("rollback report rotation: %v", rollbackErr)
		}
		t.Fatal("reencryptPGPReports() accepted invalid ciphertext")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var persisted []byte
	if err := conn.QueryRowContext(context.Background(), `SELECT body_enc FROM security_reports WHERE tracking_id = ?`, "sec_aaaaaaaaaaaaaaaa").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if string(persisted) != string(goodBody) {
		t.Fatal("failed rotation did not roll back an earlier report rewrite")
	}
	if err := conn.QueryRowContext(context.Background(), `SELECT body_enc FROM security_reports WHERE tracking_id = ?`, "sec_zzzzzzzzzzzzzzzz").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if string(persisted) != string(badBody) {
		t.Fatalf("failed rotation changed invalid stored report = %q, want %q", persisted, badBody)
	}
}

func TestPGPIdentityStateAndPrivateInstallationSecret(t *testing.T) {
	cfg := config.DefaultConfig()
	if pgpIdentityName() != "Countries API Security" {
		t.Fatalf("PGP identity name = %q", pgpIdentityName())
	}
	cfg.Server.FQDN = "countries.example"
	if got := pgpSecurityEmail(cfg); got != "security@countries.example" {
		t.Fatalf("fallback PGP email = %q", got)
	}
	cfg.Server.Contact.Admin.Email = "admin@example.test"
	if got := pgpSecurityEmail(cfg); got != "admin@example.test" {
		t.Fatalf("admin PGP email = %q", got)
	}
	cfg.Server.Contact.Security.Email = " security@example.test "
	if got := pgpSecurityEmail(cfg); got != "security@example.test" {
		t.Fatalf("security PGP email = %q", got)
	}

	statePath := filepath.Join(testutil.TempDir(t), "keyservers.state")
	state, err := readPGPState(statePath)
	if err != nil || state.Keyservers == nil || state.LastExports == nil {
		t.Fatalf("read missing PGP state = %+v, %v", state, err)
	}
	state.Fingerprint = "fingerprint"
	state.Keyservers["keys.example"] = pgpPublishAttempt{Attempts: 2, Done: true}
	state.LastExports["root"] = "2026-11-27T00:00:00Z"
	if err := writePGPState(statePath, state); err != nil {
		t.Fatalf("write PGP state: %v", err)
	}
	loaded, err := readPGPState(statePath)
	if err != nil || loaded.Fingerprint != state.Fingerprint || !loaded.Keyservers["keys.example"].Done || loaded.LastExports["root"] == "" {
		t.Fatalf("round-trip PGP state = %+v, %v", loaded, err)
	}
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPGPState(statePath); err == nil {
		t.Fatal("malformed PGP state was accepted")
	}

	filePath := filepath.Join(filepath.Dir(statePath), "key.asc")
	if err := writePGPFile(filePath, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writePGPFile(filePath, []byte("second")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil || string(data) != "second" {
		t.Fatalf("atomically replaced PGP file = %q, %v", data, err)
	}
	info, err := os.Stat(filePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("PGP private file mode = %v, %v", info, err)
	}

	db, err := database.Open(filepath.Join(filepath.Dir(statePath), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close(db) })
	if _, err := installationSecret(context.Background(), db); err == nil {
		t.Fatal("missing installation secret was accepted")
	}
	if err := database.EnsureGeneratedSecrets(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	secret, err := installationSecret(context.Background(), db)
	if err != nil || len(secret) < 32 {
		t.Fatalf("generated installation secret length=%d err=%v", len(secret), err)
	}
	if err := database.SetAppSecret(context.Background(), db, "installation_secret", base64.StdEncoding.EncodeToString([]byte("short")), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := installationSecret(context.Background(), db); err == nil {
		t.Fatal("short installation secret was accepted")
	}
	if err := database.SetAppSecret(context.Background(), db, "installation_secret", "%%%", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := installationSecret(context.Background(), db); err == nil {
		t.Fatal("invalid base64 installation secret was accepted")
	}
}

func TestPGPMaintenanceActionValidationAndRecoveryHelpers(t *testing.T) {
	if err := handlePGPMaintenance(nil, "", "", "", ""); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("empty PGP action error = %v", err)
	}
	for _, action := range []string{"unknown", "rotate", "publish", "export"} {
		t.Run(action, func(t *testing.T) {
			root := testutil.TempDir(t)
			configDir, dataDir, logsDir := filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "logs")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			cfg := config.DefaultConfig()
			cfg.Server.Database.URL = filepath.Join(dataDir, "db", "server.db")
			configPath := filepath.Join(configDir, "server.yml")
			if err := config.Save(configPath, cfg); err != nil {
				t.Fatal(err)
			}
			err := handlePGPMaintenance([]string{action}, configDir, dataDir, logsDir, configPath)
			if err == nil {
				t.Fatalf("PGP action %q unexpectedly succeeded", action)
			}
			want := map[string]string{"unknown": "unknown PGP action", "rotate": "no active PGP key", "publish": "no public PGP key", "export": "usage:"}[action]
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("PGP action %q error = %v, want %q", action, err, want)
			}
		})
	}

	root := testutil.TempDir(t)
	staged := filepath.Join(root, "staged")
	if found, err := anyFileExists(staged); err != nil || found {
		t.Fatalf("anyFileExists(missing) = (%t, %v)", found, err)
	}
	if err := os.WriteFile(staged, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := anyFileExists(staged); err != nil || !found {
		t.Fatalf("anyFileExists(existing) = (%t, %v)", found, err)
	}
	if err := removeStagedPGPFiles(staged, filepath.Join(root, "absent")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("staged PGP file remains after cleanup: %v", err)
	}
}
