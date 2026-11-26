package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/countries/src/auditlog"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/privilege"
	"github.com/apimgr/countries/src/securitykey"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const encryptionKeyPreviousAAD = "countries-encryption-key-previous-v1"

func handleSecretMaintenance(args []string, configDir, dataDir, logsDir, configPath string) error {
	if len(args) != 2 || args[0] != "rotate" {
		return errors.New("usage: countries --maintenance secret rotate <installation_secret|encryption_key>")
	}
	name := strings.TrimSpace(args[1])
	if name != "installation_secret" && name != "encryption_key" {
		return fmt.Errorf("secret %q cannot be manually rotated", name)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if err := authorizeSecretRotation(cfg); err != nil {
		return err
	}
	if err := confirmSecretRotation(name); err != nil {
		return err
	}
	databasePath := maintenanceDatabasePath(cfg, dataDir)
	db, err := database.Open(databasePath)
	if err != nil {
		return fmt.Errorf("open server database: %w", err)
	}
	defer func() { _ = database.Close(db) }()
	ctx := context.Background()
	if err := database.EnsureGeneratedSecrets(ctx, db); err != nil {
		return fmt.Errorf("initialize generated secrets: %w", err)
	}
	if err := recoverPendingSecretRotations(ctx, db, cfg, configDir, logsDir, time.Now().UTC()); err != nil {
		return fmt.Errorf("finish pending secret rotation: %w", err)
	}
	reason, err := readSecretRotationReason()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var eventName string
	switch name {
	case "installation_secret":
		err = rotateInstallationSecret(ctx, db, configDir, now)
		eventName = "security.installation_secret_rotated"
	case "encryption_key":
		err = rotateEncryptionKey(ctx, db, cfg, configDir, configPath, logsDir, now)
		eventName = "security.encryption_key_rotated"
	}
	if err != nil {
		return err
	}
	if err := writeSecretRotationAudit(logsDir, eventName, reason, now); err != nil {
		return fmt.Errorf("rotation completed but its audit event could not be written: %w", err)
	}
	fmt.Printf("Rotated %s. The previous value remains valid for its configured grace period.\n", name)
	return nil
}

func authorizeSecretRotation(cfg *config.Config) error {
	if !privilege.IsElevated() {
		if strings.TrimSpace(cfg.Server.Token) == "" {
			return errors.New("secret rotation requires root or the operator token")
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("secret rotation requires an interactive terminal")
		}
		if _, err := fmt.Fprint(os.Stderr, "Re-enter operator token: "); err != nil {
			return err
		}
		provided, err := term.ReadPassword(int(os.Stdin.Fd()))
		_, writeErr := fmt.Fprintln(os.Stderr)
		if err == nil {
			err = writeErr
		}
		if err != nil {
			return fmt.Errorf("read operator token: %w", err)
		}
		if len(provided) != len(cfg.Server.Token) || subtle.ConstantTimeCompare(provided, []byte(cfg.Server.Token)) != 1 {
			return errors.New("operator token is invalid")
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("secret rotation requires an interactive terminal for typed confirmation")
	}
	return nil
}

func confirmSecretRotation(name string) error {
	want := "ROTATE " + name
	if _, err := fmt.Fprintf(os.Stderr, "Type %q to confirm: ", want); err != nil {
		return err
	}
	line, err := readTerminalLine(os.Stdin)
	if err != nil {
		return fmt.Errorf("read typed confirmation: %w", err)
	}
	if strings.TrimSpace(line) != want {
		return errors.New("secret rotation confirmation did not match")
	}
	return nil
}

func readSecretRotationReason() (string, error) {
	if _, err := fmt.Fprint(os.Stderr, "Reason for secret rotation: "); err != nil {
		return "", err
	}
	line, err := readTerminalLine(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read rotation reason: %w", err)
	}
	reason := strings.TrimSpace(line)
	if reason == "" || len([]rune(reason)) > 500 {
		return "", errors.New("rotation reason must be 1–500 characters")
	}
	return reason, nil
}

func readTerminalLine(input *os.File) (string, error) {
	var line strings.Builder
	for {
		var one [1]byte
		if _, err := input.Read(one[:]); err != nil {
			return "", err
		}
		if one[0] == '\n' {
			return line.String(), nil
		}
		if one[0] != '\r' {
			line.WriteByte(one[0])
		}
		if line.Len() > 4096 {
			return "", errors.New("rotation reason is too long")
		}
	}
}

func maintenanceDatabasePath(cfg *config.Config, dataDir string) string {
	value := strings.TrimSpace(cfg.Server.Database.URL)
	if value == "" || value == "{data_dir}/db/server.db" {
		return filepath.Join(paths.GetDatabaseDir(dataDir), "server.db")
	}
	return strings.ReplaceAll(value, "{data_dir}", dataDir)
}

func rotateInstallationSecret(ctx context.Context, db *sql.DB, configDir string, now time.Time) error {
	oldText, found, err := database.GetAppSecret(ctx, db, "installation_secret")
	if err != nil || !found {
		return errors.Join(errors.New("installation secret is unavailable"), err)
	}
	oldSecret, err := base64.StdEncoding.DecodeString(oldText)
	if err != nil || len(oldSecret) != 32 {
		return errors.New("installation secret is invalid")
	}
	if err := refuseActivePreviousSecret(ctx, db, "installation_secret", now); err != nil {
		return err
	}
	newSecret := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newSecret); err != nil {
		return fmt.Errorf("generate installation secret: %w", err)
	}
	privatePath := filepath.Join(configDir, "security", "pgp.priv.asc.enc")
	var sealedPrivate []byte
	private, readErr := os.ReadFile(privatePath)
	if readErr == nil {
		decrypted, openErr := securitykey.OpenPrivate(oldSecret, private)
		if openErr != nil {
			return fmt.Errorf("decrypt PGP private key for rotation: %w", openErr)
		}
		sealedPrivate, err = securitykey.SealPrivate(newSecret, decrypted)
		if err != nil {
			return fmt.Errorf("encrypt PGP private key with new installation secret: %w", err)
		}
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("read encrypted PGP private key: %w", readErr)
	}
	newText := base64.StdEncoding.EncodeToString(newSecret)
	previousUntil := now.Add(7 * 24 * time.Hour)
	if err := storeSecretRotation(ctx, db, "installation_secret", oldText, newText, previousUntil, now); err != nil {
		return err
	}
	if sealedPrivate != nil {
		if err := writePrivateAtomic(privatePath, sealedPrivate); err != nil {
			return fmt.Errorf("installation secret changed; PGP private-key recovery is required: %w", err)
		}
	}
	return nil
}

func rotateEncryptionKey(ctx context.Context, db *sql.DB, cfg *config.Config, configDir, configPath, logsDir string, now time.Time) error {
	oldKey, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if err != nil || len(oldKey) != 32 {
		return errors.New("current encryption key is invalid")
	}
	if err := refuseActivePreviousSecret(ctx, db, "encryption_key", now); err != nil {
		return err
	}
	newKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, newKey); err != nil {
		return fmt.Errorf("generate encryption key: %w", err)
	}
	newText := base64.StdEncoding.EncodeToString(newKey)
	wrappedPrevious, err := sealPreviousEncryptionKey(newKey, oldKey)
	if err != nil {
		return fmt.Errorf("wrap previous encryption key: %w", err)
	}
	previousText := base64.StdEncoding.EncodeToString(wrappedPrevious)
	pendingWrapped, err := sealAESValueAAD(oldKey, newKey, []byte("countries-encryption-key-pending-v1"))
	if err != nil {
		return fmt.Errorf("stage new encryption key for crash recovery: %w", err)
	}
	previousUntil := now.Add(30 * 24 * time.Hour)
	if err := stageEncryptionRotation(ctx, db, previousText, base64.StdEncoding.EncodeToString(pendingWrapped), previousUntil, now); err != nil {
		return err
	}
	if err := updateSecretsFile(configDir, newText, true, nil); err != nil {
		return fmt.Errorf("encryption key is staged in the database; retry rotation recovery: %w", err)
	}
	if err := reencryptDatabaseValues(ctx, db, oldKey, newKey, logsDir, now); err != nil {
		return err
	}
	if err := reencryptDNSCredentials(configDir, configPath, cfg.Server.SSL.LetsEncrypt.DNSCredentials, newKey); err != nil {
		return err
	}
	if err := database.DeleteAppSecret(ctx, db, "encryption_key_pending"); err != nil {
		return fmt.Errorf("remove completed encryption-key recovery record: %w", err)
	}
	return nil
}

func stageEncryptionRotation(ctx context.Context, db *sql.DB, previous, pending string, previousUntil, now time.Time) error {
	updated := now.UTC().Format(time.RFC3339Nano)
	err := database.Write(ctx, db, func(tx *sql.Tx) error {
		for name, value := range map[string]string{
			"encryption_key_previous":       previous,
			"encryption_key_previous_until": previousUntil.UTC().Format(time.RFC3339Nano),
			"encryption_key_pending":        pending,
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, name, value, updated); err != nil {
				return fmt.Errorf("stage %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("stage encryption-key rotation: %w", err)
	}
	return nil
}

func recoverPendingSecretRotations(ctx context.Context, db *sql.DB, cfg *config.Config, configDir, logsDir string, now time.Time) error {
	if err := recoverInstallationSecret(ctx, db, configDir, logsDir, now); err != nil {
		return err
	}
	if err := recoverEncryptionKey(ctx, db, cfg, configDir, logsDir, now); err != nil {
		return err
	}
	return nil
}

// RecoverPendingSecretRotations completes interrupted secret migrations before services start.
func RecoverPendingSecretRotations(ctx context.Context, db *sql.DB, cfg *config.Config, configDir, logsDir string, now time.Time) error {
	return recoverPendingSecretRotations(ctx, db, cfg, configDir, logsDir, now)
}

// CleanupExpiredSecretRotationRows retires previous values after their grace periods.
func CleanupExpiredSecretRotationRows(ctx context.Context, db *sql.DB, logsDir string, now time.Time) error {
	for _, name := range []string{"installation_secret", "encryption_key", "cookie_signing_key", "csrf_token_secret"} {
		untilText, found, err := database.GetAppSecret(ctx, db, name+"_previous_until")
		if err != nil || !found {
			if err != nil {
				return fmt.Errorf("read %s rotation expiry: %w", name, err)
			}
			continue
		}
		until, err := time.Parse(time.RFC3339Nano, untilText)
		if err != nil {
			return fmt.Errorf("parse %s rotation expiry: %w", name, err)
		}
		if now.Before(until) {
			continue
		}
		if err := database.Write(ctx, db, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM app_secrets WHERE name IN (?,?)`, name+"_previous", name+"_previous_until")
			return err
		}); err != nil {
			return fmt.Errorf("commit %s key retirement: %w", name, err)
		}
	}
	_ = logsDir
	return nil
}

func recoverInstallationSecret(ctx context.Context, db *sql.DB, configDir, logsDir string, now time.Time) error {
	currentText, found, err := database.GetAppSecret(ctx, db, "installation_secret")
	if err != nil || !found {
		return errors.Join(errors.New("installation secret is unavailable during recovery"), err)
	}
	current, err := base64.StdEncoding.DecodeString(currentText)
	if err != nil || len(current) != 32 {
		return errors.New("installation secret is invalid during recovery")
	}
	privatePath := filepath.Join(configDir, "security", "pgp.priv.asc.enc")
	encrypted, err := os.ReadFile(privatePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read encrypted PGP private key: %w", err)
	}
	if _, err := securitykey.OpenPrivate(current, encrypted); err == nil {
		return nil
	}
	previousText, found, err := database.GetAppSecret(ctx, db, "installation_secret_previous")
	if err != nil || !found {
		return secretDecryptFailure(logsDir, "pgp_private_key", filepath.Base(privatePath), errors.Join(errors.New("previous installation secret is unavailable"), err), now)
	}
	expires, expiresFound, err := database.GetAppSecret(ctx, db, "installation_secret_previous_until")
	if err != nil || !expiresFound {
		return secretDecryptFailure(logsDir, "pgp_private_key", filepath.Base(privatePath), errors.Join(errors.New("previous installation secret expiry is unavailable"), err), now)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || !now.Before(expiresAt) {
		return secretDecryptFailure(logsDir, "pgp_private_key", filepath.Base(privatePath), errors.New("previous installation secret has expired or an invalid expiry"), now)
	}
	previous, err := base64.StdEncoding.DecodeString(previousText)
	if err != nil || len(previous) != 32 {
		return secretDecryptFailure(logsDir, "pgp_private_key", filepath.Base(privatePath), errors.New("previous installation secret is invalid"), now)
	}
	private, err := securitykey.OpenPrivate(previous, encrypted)
	if err != nil {
		return secretDecryptFailure(logsDir, "pgp_private_key", filepath.Base(privatePath), err, now)
	}
	sealed, err := securitykey.SealPrivate(current, private)
	if err != nil {
		return fmt.Errorf("reencrypt PGP private key: %w", err)
	}
	if err := writePrivateAtomic(privatePath, sealed); err != nil {
		return fmt.Errorf("reencrypt PGP private key: %w", err)
	}
	return nil
}

func recoverEncryptionKey(ctx context.Context, db *sql.DB, cfg *config.Config, configDir, logsDir string, now time.Time) error {
	encodedCurrent := cfg.Server.Security.EncryptionKey
	current, err := base64.StdEncoding.DecodeString(encodedCurrent)
	if err != nil || len(current) != 32 {
		return errors.New("current encryption key is invalid during recovery")
	}
	pendingText, pendingFound, err := database.GetAppSecret(ctx, db, "encryption_key_pending")
	if err != nil {
		return err
	}
	activatedFromPending := false
	if pendingFound {
		pending, err := base64.StdEncoding.DecodeString(pendingText)
		if err != nil {
			return errors.New("staged encryption-key recovery value is invalid")
		}
		newKey, pendingErr := openAESValueAAD(current, pending, []byte("countries-encryption-key-pending-v1"))
		if pendingErr == nil {
			previousText, found, err := database.GetAppSecret(ctx, db, "encryption_key_previous")
			if err != nil || !found {
				return errors.Join(errors.New("previous encryption key is unavailable during recovery"), err)
			}
			wrappedPrevious, err := base64.StdEncoding.DecodeString(previousText)
			if err != nil {
				return fmt.Errorf("decode previous encryption key: %w", err)
			}
			previous, err := openPreviousEncryptionKey(newKey, wrappedPrevious)
			if err != nil || subtle.ConstantTimeCompare(previous, current) != 1 {
				return errors.New("staged encryption-key rotation does not match the active key")
			}
			newText := base64.StdEncoding.EncodeToString(newKey)
			if err := updateSecretsFile(configDir, newText, true, nil); err != nil {
				return fmt.Errorf("recover staged encryption key: %w", err)
			}
			cfg.Server.Security.EncryptionKey = newText
			current = newKey
			activatedFromPending = true
		}
	}
	previousText, found, err := database.GetAppSecret(ctx, db, "encryption_key_previous")
	if err != nil || !found {
		return err
	}
	wrapped, err := base64.StdEncoding.DecodeString(previousText)
	if err != nil {
		return fmt.Errorf("decode previous encryption key: %w", err)
	}
	previous, err := openPreviousEncryptionKey(current, wrapped)
	if err != nil {
		return fmt.Errorf("unwrap previous encryption key: %w", err)
	}
	if err := reencryptDatabaseValues(ctx, db, previous, current, logsDir, now); err != nil {
		return err
	}
	if activatedFromPending {
		if err := reencryptDNSCredentials(configDir, filepath.Join(configDir, "server.yml"), cfg.Server.SSL.LetsEncrypt.DNSCredentials, current); err != nil {
			return err
		}
	}
	if _, found, err := database.GetAppSecret(ctx, db, "encryption_key_previous_until"); err != nil {
		return err
	} else if !found {
		return errors.New("previous encryption key has no grace-period expiry")
	}
	if pendingFound {
		pending, _ := base64.StdEncoding.DecodeString(pendingText)
		pendingKey, err := openAESValueAAD(previous, pending, []byte("countries-encryption-key-pending-v1"))
		if err != nil || subtle.ConstantTimeCompare(pendingKey, current) != 1 {
			return errors.New("staged encryption-key recovery value does not match the current key")
		}
		if err := database.DeleteAppSecret(ctx, db, "encryption_key_pending"); err != nil {
			return err
		}
	}
	return nil
}

func refuseActivePreviousSecret(ctx context.Context, db *sql.DB, name string, now time.Time) error {
	_, found, err := database.GetAppSecret(ctx, db, name+"_previous")
	if err != nil || !found {
		return err
	}
	untilText, untilFound, err := database.GetAppSecret(ctx, db, name+"_previous_until")
	if err != nil {
		return err
	}
	if !untilFound {
		return fmt.Errorf("previous %s exists without an expiry; repair the rotation state before rotating again", name)
	}
	until, err := time.Parse(time.RFC3339Nano, untilText)
	if err != nil {
		return fmt.Errorf("previous %s has an invalid expiry; repair the rotation state before rotating again", name)
	}
	if now.Before(until) {
		return fmt.Errorf("previous %s remains in its grace period until %s", name, until.UTC().Format(time.RFC3339))
	}
	if err := database.DeleteAppSecret(ctx, db, name+"_previous"); err != nil {
		return err
	}
	if err := database.DeleteAppSecret(ctx, db, name+"_previous_until"); err != nil {
		return err
	}
	return nil
}

func storeSecretRotation(ctx context.Context, db *sql.DB, name, previous, newCurrent string, previousUntil, now time.Time) error {
	updated := now.UTC().Format(time.RFC3339Nano)
	err := database.Write(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, name+"_previous", previous, updated); err != nil {
			return fmt.Errorf("store previous %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, name+"_previous_until", previousUntil.UTC().Format(time.RFC3339Nano), updated); err != nil {
			return fmt.Errorf("store previous %s expiry: %w", name, err)
		}
		if newCurrent != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_secrets(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, name, newCurrent, updated); err != nil {
				return fmt.Errorf("store new %s: %w", name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("commit %s rotation state: %w", name, err)
	}
	return nil
}

func reencryptDatabaseValues(ctx context.Context, db *sql.DB, previous, current []byte, logsDir string, now time.Time) error {
	if err := reencryptTokenValues(ctx, db, previous, current, logsDir, now); err != nil {
		return err
	}
	return reencryptSecurityReportValues(ctx, db, previous, current, logsDir, now)
}

func reencryptTokenValues(ctx context.Context, db *sql.DB, previous, current []byte, logsDir string, now time.Time) error {
	var lastID int64
	for {
		rows, err := db.QueryContext(ctx, `SELECT id, token_enc FROM api_tokens WHERE id > ? ORDER BY id LIMIT 100`, lastID)
		if err != nil {
			return fmt.Errorf("read encrypted API tokens: %w", err)
		}
		items := make([]tokenCipherRow, 0, 100)
		for rows.Next() {
			var item tokenCipherRow
			if err := rows.Scan(&item.id, &item.value); err != nil {
				_ = rows.Close()
				return fmt.Errorf("read encrypted API token metadata: %w", err)
			}
			items = append(items, item)
		}
		rowsErr, closeErr := rows.Err(), rows.Close()
		if err := errors.Join(rowsErr, closeErr); err != nil {
			return fmt.Errorf("finish reading encrypted API tokens: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		for _, item := range items {
			lastID = item.id
			encoded, err := base64.StdEncoding.DecodeString(item.value)
			if err != nil {
				return secretDecryptFailure(logsDir, "api_tokens", fmt.Sprint(item.id), err, now)
			}
			replacement, changed, err := reencryptCiphertext(encoded, previous, current)
			if err != nil {
				return secretDecryptFailure(logsDir, "api_tokens", fmt.Sprint(item.id), err, now)
			}
			if !changed {
				continue
			}
			if err := updateEncryptedRow(ctx, db, `UPDATE api_tokens SET token_enc = ? WHERE id = ? AND token_enc = ?`, base64.StdEncoding.EncodeToString(replacement), item.id, item.value); err != nil {
				return fmt.Errorf("reencrypt api_tokens row %d: %w", item.id, err)
			}
		}
	}
}

type tokenCipherRow struct {
	id    int64
	value string
}

type reportCipherRow struct {
	id        string
	body      []byte
	algorithm string
	contact   []byte
}

func reencryptSecurityReportValues(ctx context.Context, db *sql.DB, previous, current []byte, logsDir string, now time.Time) error {
	var lastID string
	for {
		rows, err := db.QueryContext(ctx, `SELECT tracking_id, body_enc, body_alg, contact_enc FROM security_reports WHERE tracking_id > ? ORDER BY tracking_id LIMIT 100`, lastID)
		if err != nil {
			return fmt.Errorf("read encrypted security reports: %w", err)
		}
		items := make([]reportCipherRow, 0, 100)
		for rows.Next() {
			var item reportCipherRow
			if err := rows.Scan(&item.id, &item.body, &item.algorithm, &item.contact); err != nil {
				_ = rows.Close()
				return fmt.Errorf("read encrypted security report metadata: %w", err)
			}
			items = append(items, item)
		}
		rowsErr, closeErr := rows.Err(), rows.Close()
		if err := errors.Join(rowsErr, closeErr); err != nil {
			return fmt.Errorf("finish reading encrypted security reports: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		for _, item := range items {
			lastID = item.id
			var body []byte
			bodyChanged := false
			if item.algorithm == "aes-256-gcm" {
				body, bodyChanged, err = reencryptCiphertext(item.body, previous, current)
				if err != nil {
					return secretDecryptFailure(logsDir, "security_reports", item.id, err, now)
				}
			} else if item.algorithm != "pgp" {
				return fmt.Errorf("security_reports row %s has an unsupported encryption algorithm", item.id)
			}
			contact := item.contact
			contactChanged := false
			if len(item.contact) > 0 {
				contact, contactChanged, err = reencryptCiphertext(item.contact, previous, current)
				if err != nil {
					return secretDecryptFailure(logsDir, "security_reports", item.id, err, now)
				}
			}
			if !bodyChanged && !contactChanged {
				continue
			}
			if item.algorithm == "aes-256-gcm" {
				if err := updateEncryptedRow(ctx, db, `UPDATE security_reports SET body_enc = ?, contact_enc = ? WHERE tracking_id = ?`, body, contact, item.id); err != nil {
					return fmt.Errorf("reencrypt security_reports row %s: %w", item.id, err)
				}
			} else if err := updateEncryptedRow(ctx, db, `UPDATE security_reports SET contact_enc = ? WHERE tracking_id = ?`, contact, item.id); err != nil {
				return fmt.Errorf("reencrypt security_reports contact row %s: %w", item.id, err)
			}
		}
	}
}

func reencryptCiphertext(ciphertext, previous, current []byte) ([]byte, bool, error) {
	_, err := openAESValue(current, ciphertext)
	if err == nil {
		return ciphertext, false, nil
	}
	plain, err := openAESValue(previous, ciphertext)
	if err != nil {
		return nil, false, err
	}
	replacement, err := sealAESValue(current, plain)
	return replacement, true, err
}

func updateEncryptedRow(ctx context.Context, db *sql.DB, query string, args ...any) error {
	return database.Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	})
}

func reencryptDNSCredentials(configDir, configPath string, credentials map[string]string, key []byte) error {
	if len(credentials) == 0 {
		return nil
	}
	updates := make(map[string]interface{}, len(credentials))
	for name, plaintext := range credentials {
		value, err := encryptDNSValue(key, plaintext)
		if err != nil {
			return fmt.Errorf("encrypt DNS credential %s: %w", name, err)
		}
		updates[name] = value
	}
	return updateSecretsFile(configDir, "", false, updates)
}

func updateSecretsFile(configDir, encryptionKey string, incrementVersion bool, dnsCredentials map[string]interface{}) error {
	path := filepath.Join(configDir, "secrets.yml")
	values := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read secrets.yml: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &values); err != nil {
			return fmt.Errorf("parse secrets.yml: %w", err)
		}
	}
	server := mapValue(values["server"])
	values["server"] = server
	security := mapValue(server["security"])
	server["security"] = security
	if encryptionKey != "" {
		security["encryption_key"] = encryptionKey
		if incrementVersion {
			version := 1
			if existing, ok := security["encryption_key_version"].(int); ok && existing > 0 {
				version = existing + 1
			}
			security["encryption_key_version"] = version
		}
	}
	if dnsCredentials != nil {
		ssl := mapValue(server["ssl"])
		server["ssl"] = ssl
		letsencrypt := mapValue(ssl["letsencrypt"])
		ssl["letsencrypt"] = letsencrypt
		stored := mapValue(letsencrypt["dns_credentials"])
		for name, value := range dnsCredentials {
			stored[name] = value
		}
		letsencrypt["dns_credentials"] = stored
	}
	encoded, err := yaml.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode secrets.yml: %w", err)
	}
	return writePrivateAtomic(path, encoded)
}

func mapValue(value interface{}) map[string]interface{} {
	if result, ok := value.(map[string]interface{}); ok && result != nil {
		return result
	}
	return map[string]interface{}{}
}

func sealPreviousEncryptionKey(newKey, previousKey []byte) ([]byte, error) {
	return sealAESValueAAD(newKey, previousKey, []byte(encryptionKeyPreviousAAD))
}

func openPreviousEncryptionKey(newKey, wrapped []byte) ([]byte, error) {
	previous, err := openAESValueAAD(newKey, wrapped, []byte(encryptionKeyPreviousAAD))
	if err == nil && len(previous) != 32 {
		err = errors.New("previous encryption key must be 32 bytes")
	}
	return previous, err
}

func sealAESValue(key, plaintext []byte) ([]byte, error) {
	return sealAESValueAAD(key, plaintext, nil)
}

func sealAESValueAAD(key, plaintext, additionalData []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("AES-256-GCM key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, gcm.Seal(nil, nonce, plaintext, additionalData)...), nil
}

func openAESValue(key, ciphertext []byte) ([]byte, error) {
	return openAESValueAAD(key, ciphertext, nil)
}

func openAESValueAAD(key, ciphertext, additionalData []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("AES-256-GCM key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("AES-256-GCM ciphertext is truncated")
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, additionalData)
}

func encryptDNSValue(key []byte, plaintext string) (string, error) {
	sealed, err := sealAESValue(key, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return "enc:v1:" + base64.StdEncoding.EncodeToString(sealed), nil
}

func writePrivateAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("private file path is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func writeSecretRotationAudit(logsDir, eventName, reason string, now time.Time) error {
	ip := "127.0.0.1"
	if fields := strings.Fields(os.Getenv("SSH_CONNECTION")); len(fields) > 0 {
		if parsed := net.ParseIP(fields[0]); parsed != nil {
			ip = parsed.String()
		}
	}
	return auditlog.Write(logsDir, auditlog.Event{
		Event: eventName, Category: "security", Severity: "high",
		Actor:   auditlog.Actor{Type: "operator", ID: "cli", IP: &ip},
		Details: map[string]any{"reason": reason}, Result: "success", Source: "cli",
	}, now)
}

func secretDecryptFailure(logsDir, table, rowID string, cause error, now time.Time) error {
	if logsDir != "" {
		_ = auditlog.Write(logsDir, auditlog.Event{
			Event: "security.decrypt_failed", Category: "security", Severity: "high",
			Actor:   auditlog.Actor{Type: "system"},
			Details: map[string]any{"table": table, "row_id": rowID}, Result: "failure", Source: "secret-rotation",
		}, now)
	}
	return fmt.Errorf("decryption failed for %s row %s: %w", table, rowID, cause)
}
