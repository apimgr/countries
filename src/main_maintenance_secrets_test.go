package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
)

func TestSecretRotationAESHelpers(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plaintext := []byte("encrypted DNS credential")

	ciphertext, err := sealAESValue(key, plaintext)
	if err != nil {
		t.Fatalf("sealAESValue(): %v", err)
	}
	opened, err := openAESValue(key, ciphertext)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("openAESValue() = (%q, %v), want original plaintext", opened, err)
	}
	if _, err := sealAESValue(key[:16], plaintext); err == nil {
		t.Fatal("sealAESValue() accepted a non-AES-256 key")
	}
	if _, err := openAESValue(key, ciphertext[:4]); err == nil {
		t.Fatal("openAESValue() accepted truncated ciphertext")
	}
	if _, err := openAESValue(key, append([]byte(nil), ciphertext[:len(ciphertext)-1]...)); err == nil {
		t.Fatal("openAESValue() accepted modified ciphertext")
	}

	sealedAAD, err := sealAESValueAAD(key, plaintext, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openAESValueAAD(key, sealedAAD, []byte("row-2")); err == nil {
		t.Fatal("openAESValueAAD() accepted mismatched additional data")
	}
	previous, err := sealPreviousEncryptionKey(key, bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	openedPrevious, err := openPreviousEncryptionKey(key, previous)
	if err != nil || !bytes.Equal(openedPrevious, bytes.Repeat([]byte{3}, 32)) {
		t.Fatalf("openPreviousEncryptionKey() = (%x, %v)", openedPrevious, err)
	}
	if _, err := openPreviousEncryptionKey(key, sealedAAD); err == nil {
		t.Fatal("openPreviousEncryptionKey() accepted ciphertext with another purpose")
	}

	dnsValue, err := encryptDNSValue(key, string(plaintext))
	if err != nil || len(dnsValue) < len("enc:v1:") || dnsValue[:len("enc:v1:")] != "enc:v1:" {
		t.Fatalf("encryptDNSValue() = (%q, %v)", dnsValue, err)
	}
	encoded, err := base64.StdEncoding.DecodeString(dnsValue[len("enc:v1:"):])
	if err != nil {
		t.Fatal(err)
	}
	opened, err = openAESValue(key, encoded)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("encrypted DNS credential = (%q, %v)", opened, err)
	}
	if _, err := encryptDNSValue(key[:16], string(plaintext)); err == nil {
		t.Fatal("encryptDNSValue() accepted a non-AES-256 key")
	}
}

func TestSecretRotationPreviousStateLifecycle(t *testing.T) {
	db, _ := maintenanceTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	until := now.Add(7 * 24 * time.Hour)
	if err := storeSecretRotation(ctx, db, "installation_secret", "old-value", "new-value", until, now); err != nil {
		t.Fatalf("storeSecretRotation(): %v", err)
	}
	for name, want := range map[string]string{
		"installation_secret_previous":       "old-value",
		"installation_secret_previous_until": until.Format(time.RFC3339Nano),
		"installation_secret":                "new-value",
	} {
		got, found, err := database.GetAppSecret(ctx, db, name)
		if err != nil || !found || got != want {
			t.Errorf("GetAppSecret(%q) = (%q, %t, %v), want %q", name, got, found, err, want)
		}
	}
	if err := refuseActivePreviousSecret(ctx, db, "installation_secret", now); err == nil {
		t.Fatal("refuseActivePreviousSecret() allowed rotation during the grace period")
	}
	if err := database.SetAppSecret(ctx, db, "installation_secret_previous_until", now.Add(-time.Second).Format(time.RFC3339Nano), now); err != nil {
		t.Fatal(err)
	}
	if err := refuseActivePreviousSecret(ctx, db, "installation_secret", now); err != nil {
		t.Fatalf("refuseActivePreviousSecret(expired): %v", err)
	}
	for _, name := range []string{"installation_secret_previous", "installation_secret_previous_until"} {
		if _, found, err := database.GetAppSecret(ctx, db, name); err != nil || found {
			t.Errorf("expired rotation state %q remains: found=%t err=%v", name, found, err)
		}
	}

	if err := database.SetAppSecret(ctx, db, "encryption_key_previous", "old", now); err != nil {
		t.Fatal(err)
	}
	if err := refuseActivePreviousSecret(ctx, db, "encryption_key", now); err == nil {
		t.Fatal("refuseActivePreviousSecret() accepted missing expiry")
	}
	if err := database.SetAppSecret(ctx, db, "encryption_key_previous_until", "invalid", now); err != nil {
		t.Fatal(err)
	}
	if err := refuseActivePreviousSecret(ctx, db, "encryption_key", now); err == nil {
		t.Fatal("refuseActivePreviousSecret() accepted invalid expiry")
	}
	if err := database.DeleteAppSecret(ctx, db, "encryption_key_previous_until"); err != nil {
		t.Fatal(err)
	}
	if err := storeSecretRotation(ctx, db, "encryption_key", "wrapped-old", "", until, now); err != nil {
		t.Fatalf("storeSecretRotation(encryption key): %v", err)
	}
	if got, found, err := database.GetAppSecret(ctx, db, "encryption_key"); err != nil || found || got != "" {
		t.Fatalf("encryption key current value changed before activation: (%q, %t, %v)", got, found, err)
	}
}

func TestSecretRotationAtomicFilesAndSecretsYAML(t *testing.T) {
	root := testutil.TempDir(t)
	path := filepath.Join(root, "security", "rotation.key")
	if err := writePrivateAtomic(path, []byte("new secret")); err != nil {
		t.Fatalf("writePrivateAtomic(): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private file mode = %v, %v; want 0600", info, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "target"), path); err == nil {
		if err := writePrivateAtomic(path, []byte("must not follow")); err == nil {
			t.Fatal("writePrivateAtomic() followed a symlink")
		}
	}

	configDir := filepath.Join(root, "config")
	if err := updateSecretsFile(configDir, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), true, map[string]interface{}{"rfc2136": "enc:v1:credential"}); err != nil {
		t.Fatalf("updateSecretsFile(): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("encryption_key_version: 1")) || !bytes.Contains(data, []byte("rfc2136: enc:v1:credential")) {
		t.Fatalf("secrets.yml missing rotation data: %s", data)
	}
	if err := updateSecretsFile(configDir, "", false, map[string]interface{}{"rfc2136": "enc:v1:replacement"}); err != nil {
		t.Fatal(err)
	}
	if err := updateSecretsFile(configDir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := mapValue(nil)["missing"]; err {
		t.Fatal("mapValue(nil) unexpectedly contained a key")
	}
	if got := mapValue(map[string]interface{}{"existing": true}); got["existing"] != true {
		t.Fatal("mapValue() did not preserve an existing map")
	}
	if err := updateSecretsFile(filepath.Join(root, "bad"), "", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "broken", "secrets.yml"), []byte("not created"), 0600); err == nil {
		t.Fatal("unexpected successful write into missing parent")
	}
	if err := os.WriteFile(filepath.Join(configDir, "secrets.yml"), []byte("server: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := updateSecretsFile(configDir, "", false, nil); err == nil {
		t.Fatal("updateSecretsFile() accepted malformed YAML")
	}
}

func TestSecretRotationReencryptAndAuditFailures(t *testing.T) {
	previous := bytes.Repeat([]byte{1}, 32)
	current := bytes.Repeat([]byte{2}, 32)
	old, err := sealAESValue(previous, []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, changed, err := reencryptCiphertext(old, previous, current)
	if err != nil || !changed {
		t.Fatalf("reencryptCiphertext(old): changed=%t err=%v", changed, err)
	}
	if plain, err := openAESValue(current, rotated); err != nil || string(plain) != "value" {
		t.Fatalf("rotated ciphertext = (%q, %v)", plain, err)
	}
	unchanged, changed, err := reencryptCiphertext(rotated, previous, current)
	if err != nil || changed || !bytes.Equal(unchanged, rotated) {
		t.Fatalf("reencryptCiphertext(current): changed=%t err=%v", changed, err)
	}
	if _, _, err := reencryptCiphertext([]byte("bad"), previous, current); err == nil {
		t.Fatal("reencryptCiphertext() accepted invalid ciphertext")
	}

	if err := writeSecretRotationAudit(filepath.Join(testutil.TempDir(t), "logs"), "security.test", "test reason", time.Now().UTC()); err != nil {
		t.Fatalf("writeSecretRotationAudit(): %v", err)
	}
	if err := secretDecryptFailure("", "api_tokens", "42", os.ErrPermission, time.Now().UTC()); err == nil {
		t.Fatal("secretDecryptFailure() returned nil")
	}
}

func TestSecretRotationReencryptsStoredValues(t *testing.T) {
	db, _ := maintenanceTestDB(t)
	ctx := context.Background()
	previous := bytes.Repeat([]byte{4}, 32)
	current := bytes.Repeat([]byte{5}, 32)
	owner, _ := createMaintenanceTestOwner(t, db, "")
	tokenCiphertext, err := sealAESValue(previous, []byte("token-copy"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE api_tokens SET token_enc=? WHERE owner_id=?`, base64.StdEncoding.EncodeToString(tokenCiphertext), owner.OwnerID); err != nil {
		t.Fatal(err)
	}
	bodyCiphertext, err := sealAESValue(previous, []byte("report-body"))
	if err != nil {
		t.Fatal(err)
	}
	contactCiphertext, err := sealAESValue(previous, []byte("contact"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO security_reports (tracking_id, security_id, severity, component, status, body_enc, body_alg, contact_enc, token_hash) VALUES ('SR-AES', '', 'low', 'api', 'received', ?, 'aes-256-gcm', ?, 'hash'), ('SR-PGP', '', 'low', 'api', 'received', ?, 'pgp', ?, 'hash')`, bodyCiphertext, contactCiphertext, []byte("pgp protected"), contactCiphertext); err != nil {
		t.Fatal(err)
	}
	if err := reencryptDatabaseValues(ctx, db, previous, current, "", time.Now().UTC()); err != nil {
		t.Fatalf("reencryptDatabaseValues(): %v", err)
	}
	var tokenEncoded string
	if err := db.QueryRowContext(ctx, `SELECT token_enc FROM api_tokens WHERE owner_id=?`, owner.OwnerID).Scan(&tokenEncoded); err != nil {
		t.Fatal(err)
	}
	tokenCiphertext, err = base64.StdEncoding.DecodeString(tokenEncoded)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext, err := openAESValue(current, tokenCiphertext); err != nil || string(plaintext) != "token-copy" {
		t.Fatalf("re-encrypted token = (%q, %v)", plaintext, err)
	}
	for _, test := range []struct {
		id        string
		algorithm string
		wantBody  string
	}{
		{id: "SR-AES", algorithm: "aes-256-gcm", wantBody: "report-body"},
		{id: "SR-PGP", algorithm: "pgp", wantBody: "pgp protected"},
	} {
		var body, contact []byte
		if err := db.QueryRowContext(ctx, `SELECT body_enc, contact_enc FROM security_reports WHERE tracking_id=?`, test.id).Scan(&body, &contact); err != nil {
			t.Fatal(err)
		}
		if test.algorithm == "aes-256-gcm" {
			plain, err := openAESValue(current, body)
			if err != nil || string(plain) != test.wantBody {
				t.Errorf("%s body = (%q, %v)", test.id, plain, err)
			}
		} else if string(body) != test.wantBody {
			t.Errorf("%s PGP body changed: %q", test.id, body)
		}
		plain, err := openAESValue(current, contact)
		if err != nil || string(plain) != "contact" {
			t.Errorf("%s contact = (%q, %v)", test.id, plain, err)
		}
	}
	if err := reencryptDatabaseValues(ctx, db, previous, current, "", time.Now().UTC()); err != nil {
		t.Fatalf("second re-encryption pass: %v", err)
	}
}
