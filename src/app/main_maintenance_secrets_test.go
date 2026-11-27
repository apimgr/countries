package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/securitykey"
	"github.com/apimgr/countries/src/testutil"
	"gopkg.in/yaml.v3"
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
	if err := secretDecryptFailure("", "api_tokens", "019b3e57-4f80-7000-8000-000000000001", os.ErrPermission, time.Now().UTC()); err == nil {
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

func TestSecretRotationWorkflows(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	t.Run("installation secret", func(t *testing.T) {
		db, root := maintenanceTestDB(t)
		oldSecret := bytes.Repeat([]byte{6}, 32)
		oldText := base64.StdEncoding.EncodeToString(oldSecret)
		if err := database.SetAppSecret(ctx, db, "installation_secret", oldText, now); err != nil {
			t.Fatal(err)
		}
		configDir := filepath.Join(root, "config")
		if err := rotateInstallationSecret(ctx, db, configDir, now); err != nil {
			t.Fatalf("rotateInstallationSecret(): %v", err)
		}
		newText, found, err := database.GetAppSecret(ctx, db, "installation_secret")
		if err != nil || !found || newText == oldText {
			t.Fatalf("new installation secret = (%q, %t, %v)", newText, found, err)
		}
		newSecret, err := base64.StdEncoding.DecodeString(newText)
		if err != nil || len(newSecret) != 32 {
			t.Fatalf("decode new installation secret: %v", err)
		}
		if got, found, err := database.GetAppSecret(ctx, db, "installation_secret_previous"); err != nil || !found || got != oldText {
			t.Fatalf("previous installation secret = (%q, %t, %v)", got, found, err)
		}
		if err := recoverInstallationSecret(ctx, db, configDir, filepath.Join(root, "logs"), now); err != nil {
			t.Fatalf("recoverInstallationSecret() without optional PGP key: %v", err)
		}
	})

	t.Run("encryption key", func(t *testing.T) {
		db, root := maintenanceTestDB(t)
		oldKey := bytes.Repeat([]byte{8}, 32)
		cfg := config.DefaultConfig()
		cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(oldKey)
		configDir := filepath.Join(root, "config")
		if err := rotateEncryptionKey(ctx, db, cfg, configDir, filepath.Join(configDir, "server.yml"), filepath.Join(root, "logs"), now); err != nil {
			t.Fatalf("rotateEncryptionKey(): %v", err)
		}
		secretsYAML, err := os.ReadFile(filepath.Join(configDir, "secrets.yml"))
		if err != nil {
			t.Fatal(err)
		}
		var secrets struct {
			Server struct {
				Security struct {
					EncryptionKey string `yaml:"encryption_key"`
				} `yaml:"security"`
			} `yaml:"server"`
		}
		if err := yaml.Unmarshal(secretsYAML, &secrets); err != nil {
			t.Fatal(err)
		}
		cfg.Server.Security.EncryptionKey = secrets.Server.Security.EncryptionKey
		newKey, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
		if err != nil || len(newKey) != 32 || bytes.Equal(newKey, oldKey) {
			t.Fatalf("new encryption key is invalid or unchanged: %v", err)
		}
		for _, name := range []string{"encryption_key_previous", "encryption_key_previous_until"} {
			if _, found, err := database.GetAppSecret(ctx, db, name); err != nil || !found {
				t.Fatalf("rotation state %q missing: found=%t err=%v", name, found, err)
			}
		}
		if _, found, err := database.GetAppSecret(ctx, db, "encryption_key_pending"); err != nil || found {
			t.Fatalf("completed rotation retained pending state: found=%t err=%v", found, err)
		}
		if err := recoverEncryptionKey(ctx, db, cfg, configDir, filepath.Join(root, "logs"), now); err != nil {
			t.Fatalf("recoverEncryptionKey(): %v", err)
		}
		if err := CleanupExpiredSecretRotationRows(ctx, db, filepath.Join(root, "logs"), now.Add(31*24*time.Hour)); err != nil {
			t.Fatalf("CleanupExpiredSecretRotationRows(): %v", err)
		}
		if _, found, err := database.GetAppSecret(ctx, db, "encryption_key_previous"); err != nil || found {
			t.Fatalf("expired previous encryption key remains: found=%t err=%v", found, err)
		}
	})
}

func TestRecoverPendingSecretRotations(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	t.Run("installation key rewraps PGP private material", func(t *testing.T) {
		db, root := maintenanceTestDB(t)
		oldSecret := bytes.Repeat([]byte{3}, 32)
		currentSecret := bytes.Repeat([]byte{4}, 32)
		oldText := base64.StdEncoding.EncodeToString(oldSecret)
		currentText := base64.StdEncoding.EncodeToString(currentSecret)
		if err := database.SetAppSecret(ctx, db, "installation_secret", currentText, now); err != nil {
			t.Fatal(err)
		}
		if err := database.SetAppSecret(ctx, db, "installation_secret_previous", oldText, now); err != nil {
			t.Fatal(err)
		}
		if err := database.SetAppSecret(ctx, db, "installation_secret_previous_until", now.Add(24*time.Hour).Format(time.RFC3339Nano), now); err != nil {
			t.Fatal(err)
		}
		pair, err := securitykey.Generate("Countries Test", "security@example.test", now)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := securitykey.SealPrivate(oldSecret, pair.Private)
		if err != nil {
			t.Fatal(err)
		}
		privatePath := filepath.Join(root, "config", "security", "pgp.priv.asc.enc")
		if err := writePrivateAtomic(privatePath, sealed); err != nil {
			t.Fatal(err)
		}
		if err := recoverInstallationSecret(ctx, db, filepath.Dir(filepath.Dir(privatePath)), filepath.Join(root, "logs"), now); err != nil {
			t.Fatalf("recoverInstallationSecret(): %v", err)
		}
		updated, err := os.ReadFile(privatePath)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := securitykey.OpenPrivate(currentSecret, updated)
		if err != nil || !bytes.Equal(opened, pair.Private) {
			t.Fatalf("recovered private key = (%q, %v)", opened, err)
		}
	})

	t.Run("staged encryption key activates after restart", func(t *testing.T) {
		db, root := maintenanceTestDB(t)
		oldKey := bytes.Repeat([]byte{5}, 32)
		newKey := bytes.Repeat([]byte{6}, 32)
		cfg := config.DefaultConfig()
		cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(oldKey)
		if err := database.SetAppSecret(ctx, db, "installation_secret", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), now); err != nil {
			t.Fatal(err)
		}
		pending, err := sealAESValueAAD(oldKey, newKey, []byte("countries-encryption-key-pending-v1"))
		if err != nil {
			t.Fatal(err)
		}
		wrappedPrevious, err := sealPreviousEncryptionKey(newKey, oldKey)
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{
			"encryption_key_pending":        base64.StdEncoding.EncodeToString(pending),
			"encryption_key_previous":       base64.StdEncoding.EncodeToString(wrappedPrevious),
			"encryption_key_previous_until": now.Add(30 * 24 * time.Hour).Format(time.RFC3339Nano),
		} {
			if err := database.SetAppSecret(ctx, db, name, value, now); err != nil {
				t.Fatal(err)
			}
		}
		configDir := filepath.Join(root, "config")
		if err := recoverPendingSecretRotations(ctx, db, cfg, configDir, filepath.Join(root, "logs"), now); err != nil {
			t.Fatalf("recoverPendingSecretRotations(): %v", err)
		}
		if cfg.Server.Security.EncryptionKey != base64.StdEncoding.EncodeToString(newKey) {
			t.Fatal("recovery did not activate the staged encryption key in memory")
		}
		if _, found, err := database.GetAppSecret(ctx, db, "encryption_key_pending"); err != nil || found {
			t.Fatalf("recovery retained pending key: found=%t err=%v", found, err)
		}
	})
}

func TestSecretMaintenanceInputAndAuthorization(t *testing.T) {
	t.Run("rejects malformed maintenance commands before config access", func(t *testing.T) {
		for _, args := range [][]string{nil, {"rotate"}, {"rotate", "operator_token"}, {"reset", "encryption_key"}} {
			if err := handleSecretMaintenance(args, "", "", "", filepath.Join(testutil.TempDir(t), "missing.yml")); err == nil {
				t.Errorf("handleSecretMaintenance(%q) unexpectedly succeeded", args)
			}
		}
	})

	t.Run("requires an interactive terminal even for root", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Server.Token = "test-operator-token"
		if err := authorizeSecretRotation(cfg); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
			t.Fatalf("authorizeSecretRotation() = %v, want interactive-terminal error", err)
		}
	})

	t.Run("confirmation and reason input", func(t *testing.T) {
		originalStdin := os.Stdin
		originalStderr := os.Stderr
		inputPath := filepath.Join(testutil.TempDir(t), "stdin")
		input, err := os.Create(inputPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := input.WriteString("ROTATE encryption_key\r\nRoutine key rollover\n"); err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		if _, err := input.Seek(0, 0); err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		output, err := os.Create(filepath.Join(filepath.Dir(inputPath), "stderr"))
		if err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		os.Stdin, os.Stderr = input, output
		t.Cleanup(func() {
			os.Stdin, os.Stderr = originalStdin, originalStderr
			if err := input.Close(); err != nil {
				t.Errorf("close test stdin: %v", err)
			}
			if err := output.Close(); err != nil {
				t.Errorf("close test stderr: %v", err)
			}
		})
		if err := confirmSecretRotation("encryption_key"); err != nil {
			t.Fatalf("confirmSecretRotation(): %v", err)
		}
		reason, err := readSecretRotationReason()
		if err != nil || reason != "Routine key rollover" {
			t.Fatalf("readSecretRotationReason() = (%q, %v)", reason, err)
		}
	})

	t.Run("rejects mismatched confirmation and invalid reason", func(t *testing.T) {
		originalStdin := os.Stdin
		originalStderr := os.Stderr
		dir := testutil.TempDir(t)
		input, err := os.Create(filepath.Join(dir, "stdin"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := input.WriteString("ROTATE another_key\n\n"); err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		if _, err := input.Seek(0, 0); err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		output, err := os.Create(filepath.Join(dir, "stderr"))
		if err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		os.Stdin, os.Stderr = input, output
		t.Cleanup(func() {
			os.Stdin, os.Stderr = originalStdin, originalStderr
			_ = input.Close()
			_ = output.Close()
		})
		if err := confirmSecretRotation("encryption_key"); err == nil {
			t.Fatal("confirmSecretRotation() accepted a mismatched value")
		}
		if _, err := readSecretRotationReason(); err == nil {
			t.Fatal("readSecretRotationReason() accepted an empty reason")
		}
	})

	t.Run("database path honors default and explicit URL", func(t *testing.T) {
		cfg := config.DefaultConfig()
		if got, want := maintenanceDatabasePath(cfg, "/data/countries"), filepath.Join(paths.GetDatabaseDir("/data/countries"), "server.db"); got != want {
			t.Errorf("default database path = %q, want %q", got, want)
		}
		cfg.Server.Database.URL = "postgres://db/{data_dir}/server"
		if got, want := maintenanceDatabasePath(cfg, "/data/countries"), "postgres://db//data/countries/server"; got != want {
			t.Errorf("explicit database URL = %q, want %q", got, want)
		}
	})
}
