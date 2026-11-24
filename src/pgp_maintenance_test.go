package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close database connection: %v", err)
		}
	})
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := reencryptPGPReports(context.Background(), conn, oldPair.Private, newPair.Public); err != nil {
		if _, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK"); rollbackErr != nil {
			t.Errorf("rollback report rotation: %v", rollbackErr)
		}
		t.Fatalf("reencryptPGPReports() error = %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	var updated []byte
	if err := conn.QueryRowContext(context.Background(), `SELECT body_enc FROM security_reports WHERE tracking_id = ?`, "sec_0123456789abcdef").Scan(&updated); err != nil {
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
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := reencryptPGPReports(context.Background(), conn, oldPair.Private, newPair.Public); err == nil {
		if _, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK"); rollbackErr != nil {
			t.Errorf("rollback report rotation: %v", rollbackErr)
		}
		t.Fatal("reencryptPGPReports() accepted invalid ciphertext")
	}
	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
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
