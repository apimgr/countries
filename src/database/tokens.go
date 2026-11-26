package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var ErrTokenNotFound = errors.New("token not found")
var ErrTokenLimit = errors.New("active linked token limit reached")
var ErrTokenName = errors.New("linked token name already exists")

type TokenRecord struct {
	ID        int64      `json:"id"`
	OwnerID   string     `json:"owner_id,omitempty"`
	Kind      string     `json:"kind"`
	Name      string     `json:"name,omitempty"`
	Prefix    string     `json:"prefix"`
	Last4     string     `json:"last4,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RotatedAt *time.Time `json:"rotated_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	LastUsed  *time.Time `json:"last_used_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type TokenIdentity struct {
	OwnerID   string
	Kind      string
	TokenID   int64
	Prefix    string
	Active    bool
	ExpiresAt *time.Time
}

// CreateOwnerToken creates an anonymous owner and its primary token atomically.
func CreateOwnerToken(ctx context.Context, db *sql.DB, encryptionKey string) (TokenRecord, string, error) {
	if err := ensureTokenLast4(ctx, db); err != nil {
		return TokenRecord{}, "", err
	}
	raw, err := newOwnerToken()
	if err != nil {
		return TokenRecord{}, "", err
	}
	ownerID, err := randomID()
	if err != nil {
		return TokenRecord{}, "", err
	}
	sealed, err := encryptOwnerToken(encryptionKey, raw)
	if err != nil {
		return TokenRecord{}, "", err
	}
	hash := tokenHash(raw)
	var id int64
	if err := Write(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO owners (id) VALUES (?)`, ownerID); err != nil { return err }
		result, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (owner_id, kind, token_hash, token_prefix, token_last4, token_enc) VALUES (?, 'primary', ?, ?, ?, ?)`, ownerID, hash, raw[:12], raw[len(raw)-4:], sealed)
		if err != nil { return err }
		id, err = result.LastInsertId()
		return err
	}); err != nil {
		return TokenRecord{}, "", err
	}
	return TokenRecord{ID: id, OwnerID: ownerID, Kind: "primary", Prefix: raw[:12], Last4: raw[len(raw)-4:], CreatedAt: time.Now().UTC()}, raw, nil
}

// LookupOwnerToken validates an owner or linked bearer and refreshes its usage timestamps.
func LookupOwnerToken(ctx context.Context, db *sql.DB, raw string, now time.Time) (TokenIdentity, error) {
	if !validOwnerTokenFormat(raw) {
		return TokenIdentity{}, ErrTokenNotFound
	}
	var identity TokenIdentity
	err := Write(ctx, db, func(tx *sql.Tx) error {
		var expires, revoked sql.NullInt64
		var lastUsed sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT id, owner_id, kind, token_prefix, expires_at, revoked_at, last_used_at FROM api_tokens WHERE token_hash = ?`, tokenHash(raw)).Scan(&identity.TokenID, &identity.OwnerID, &identity.Kind, &identity.Prefix, &expires, &revoked, &lastUsed); err != nil {
			if errors.Is(err, sql.ErrNoRows) { return ErrTokenNotFound }
			return err
		}
		if revoked.Valid || expires.Valid && expires.Int64 <= now.Unix() { return ErrTokenNotFound }
		if expires.Valid { expiresAt := time.Unix(expires.Int64, 0).UTC(); identity.ExpiresAt = &expiresAt }
		identity.Active = true
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?) AND (last_used_at IS NULL OR last_used_at <= ?)`, now.Unix(), identity.TokenID, now.Unix(), now.Unix()-60); err != nil { return err }
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM api_tokens WHERE id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, identity.TokenID, now.Unix()).Scan(&active); err != nil {
			if errors.Is(err, sql.ErrNoRows) { return ErrTokenNotFound }
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE owners SET last_seen_at = ? WHERE id = ? AND last_seen_at <= ?`, now.Unix(), identity.OwnerID, now.Unix()-60)
		return err
	})
	if err != nil { return TokenIdentity{}, err }
	return identity, nil
}

// CleanupOwnerTokenState removes expired contact challenges and idle token owners.
func CleanupOwnerTokenState(ctx context.Context, db *sql.DB, now time.Time, idlePurgeDays int) (int64, int64, error) {
	var challengeCount, ownerCount int64
	err := Write(ctx, db, func(tx *sql.Tx) error {
		challenges, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE expires_at <= ?`, now.UTC())
		if err != nil { return err }
		challengeCount, err = challenges.RowsAffected()
		if err != nil { return err }
		if idlePurgeDays > 0 {
		cutoff := now.AddDate(0, 0, -idlePurgeDays).Unix()
		owners, deleteErr := tx.ExecContext(ctx, `DELETE FROM owners WHERE last_seen_at < ?`, cutoff)
		if deleteErr != nil { return deleteErr }
			ownerCount, err = owners.RowsAffected()
			if err != nil { return err }
		}
		return nil
	})
	if err != nil { return 0, 0, err }
	return challengeCount, ownerCount, nil
}

// ListOwnerTokens returns metadata only; encrypted token values are never selected.
func ListOwnerTokens(ctx context.Context, db *sql.DB, ownerID string) (items []TokenRecord, resultErr error) {
	if err := ensureTokenLast4(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id, owner_id, kind, COALESCE(name,''), token_prefix, token_last4, created_at, rotated_at, expires_at, last_used_at, revoked_at FROM api_tokens WHERE owner_id = ? ORDER BY CASE kind WHEN 'primary' THEN 0 ELSE 1 END, created_at, id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	items = make([]TokenRecord, 0)
	for rows.Next() {
		item, err := scanTokenRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CountActiveOwnerTokens returns active primary and linked token counts without selecting token material.
func CountActiveOwnerTokens(ctx context.Context, db *sql.DB, now time.Time) (primary, linked int, err error) {
	err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN kind = 'primary' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN kind = 'linked' THEN 1 ELSE 0 END),0) FROM api_tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, now.UTC().Unix()).Scan(&primary, &linked)
	return primary, linked, err
}

// RevealOwnerToken decrypts a token only for an explicit reveal action.
func RevealOwnerToken(ctx context.Context, db *sql.DB, ownerID string, tokenID int64, encryptionKey string) (string, TokenRecord, error) {
	var encrypted string
	var item TokenRecord
	if err := ensureTokenLast4(ctx, db); err != nil {
		return "", item, err
	}
	row := db.QueryRowContext(ctx, `SELECT id, owner_id, kind, COALESCE(name,''), token_prefix, token_last4, created_at, rotated_at, expires_at, last_used_at, revoked_at, token_enc FROM api_tokens WHERE owner_id = ? AND id = ?`, ownerID, tokenID)
	var created int64
	var rotated, expires, lastUsed, revoked sql.NullInt64
	if err := row.Scan(&item.ID, &item.OwnerID, &item.Kind, &item.Name, &item.Prefix, &item.Last4, &created, &rotated, &expires, &lastUsed, &revoked, &encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", item, ErrTokenNotFound
		}
		return "", item, err
	}
	item.CreatedAt = time.Unix(created, 0).UTC()
	item.RotatedAt = unixPointer(rotated)
	item.ExpiresAt = unixPointer(expires)
	item.LastUsed = unixPointer(lastUsed)
	item.RevokedAt = unixPointer(revoked)
	raw, err := decryptOwnerToken(encryptionKey, encrypted)
	return raw, item, err
}

// RotateOwnerToken replaces one token in place, preserving its owner and linked-token relationships.
func RotateOwnerToken(ctx context.Context, db *sql.DB, ownerID string, tokenID int64, encryptionKey string, now time.Time) (TokenRecord, string, error) {
	return mutateToken(ctx, db, ownerID, tokenID, encryptionKey, now, func(tx *sql.Tx, item TokenRecord, raw string, sealed string) (TokenRecord, string, error) {
		result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET token_hash = ?, token_prefix = ?, token_last4 = ?, token_enc = ?, rotated_at = ? WHERE owner_id = ? AND id = ? AND revoked_at IS NULL`, tokenHash(raw), raw[:12], raw[len(raw)-4:], sealed, now.Unix(), ownerID, tokenID)
		if err != nil {
			return TokenRecord{}, "", err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return TokenRecord{}, "", ErrTokenNotFound
		}
		item.Prefix, item.Last4 = raw[:12], raw[len(raw)-4:]
		item.RotatedAt = &now
		return item, raw, nil
	})
}

// CreateLinkedOwnerToken issues a named token, with a bounded active-token count.
func CreateLinkedOwnerToken(ctx context.Context, db *sql.DB, ownerID, name string, expiresAt *time.Time, maxActive int, encryptionKey string, now time.Time) (TokenRecord, string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 64 {
		return TokenRecord{}, "", fmt.Errorf("linked token name must be 1–64 characters")
	}
	if maxActive < 1 {
		maxActive = 25
	}
	if err := ensureTokenLast4(ctx, db); err != nil {
		return TokenRecord{}, "", err
	}
	raw, err := newOwnerToken()
	if err != nil {
		return TokenRecord{}, "", err
	}
	sealed, err := encryptOwnerToken(encryptionKey, raw)
	if err != nil {
		return TokenRecord{}, "", err
	}
	var id int64
	err = Write(ctx, db, func(tx *sql.Tx) error {
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, ownerID, now.Unix()).Scan(&active); err != nil {
		return err
	}
	if active >= maxActive {
		return ErrTokenLimit
	}
	var duplicate int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND lower(name) = lower(?)`, ownerID, name).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return ErrTokenName
	}
	var expiry any
	if expiresAt != nil {
		expiry = expiresAt.Unix()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (owner_id, kind, name, token_hash, token_prefix, token_last4, token_enc, expires_at) VALUES (?, 'linked', ?, ?, ?, ?, ?, ?)`, ownerID, name, tokenHash(raw), raw[:12], raw[len(raw)-4:], sealed, expiry)
	if err != nil {
		return err
	}
	id, err = result.LastInsertId()
	return err
	})
	if err != nil { return TokenRecord{}, "", err }
	item := TokenRecord{ID: id, OwnerID: ownerID, Kind: "linked", Name: name, Prefix: raw[:12], Last4: raw[len(raw)-4:], CreatedAt: now.UTC(), ExpiresAt: expiresAt}
	return item, raw, nil
}

// UpdateLinkedOwnerToken changes a linked token's name and optional expiry.
func UpdateLinkedOwnerToken(ctx context.Context, db *sql.DB, ownerID string, tokenID int64, name string, expiresAt *time.Time, now time.Time) error {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 64 {
		return fmt.Errorf("linked token name must be 1–64 characters")
	}
	if err := ensureTokenLast4(ctx, db); err != nil {
		return err
	}
	return Write(ctx, db, func(tx *sql.Tx) error {
	var duplicate int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ? AND kind = 'linked' AND id <> ? AND lower(name) = lower(?)`, ownerID, tokenID, name).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate > 0 {
		return ErrTokenName
	}
	var expiry any
	if expiresAt != nil {
		expiry = expiresAt.Unix()
	}
	result, err := tx.ExecContext(ctx, `UPDATE api_tokens SET name = ?, expires_at = ? WHERE owner_id = ? AND id = ? AND kind = 'linked' AND revoked_at IS NULL`, name, expiry, ownerID, tokenID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrTokenNotFound
	}
	return nil
	})
}

// RevokeLinkedOwnerToken revokes one linked credential without touching its owner.
func RevokeLinkedOwnerToken(ctx context.Context, db *sql.DB, ownerID string, tokenID int64, now time.Time) error {
	var result sql.Result
	err := Write(ctx, db, func(tx *sql.Tx) error {
		var err error
		result, err = tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ?, revoked_reason = 'owner_revoked' WHERE owner_id = ? AND id = ? AND kind = 'linked' AND revoked_at IS NULL`, now.Unix(), ownerID, tokenID)
		return err
	})
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrTokenNotFound
	}
	return nil
}

func mutateToken(ctx context.Context, db *sql.DB, ownerID string, tokenID int64, encryptionKey string, now time.Time, mutate func(*sql.Tx, TokenRecord, string, string) (TokenRecord, string, error)) (TokenRecord, string, error) {
	if err := ensureTokenLast4(ctx, db); err != nil {
		return TokenRecord{}, "", err
	}
	raw, err := newOwnerToken()
	if err != nil {
		return TokenRecord{}, "", err
	}
	sealed, err := encryptOwnerToken(encryptionKey, raw)
	if err != nil {
		return TokenRecord{}, "", err
	}
	var resultItem TokenRecord
	var resultRaw string
	err = Write(ctx, db, func(tx *sql.Tx) error {
	row := tx.QueryRowContext(ctx, `SELECT id, owner_id, kind, COALESCE(name,''), token_prefix, token_last4, created_at, rotated_at, expires_at, last_used_at, revoked_at FROM api_tokens WHERE owner_id = ? AND id = ?`, ownerID, tokenID)
	item, err := scanTokenRecord(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTokenNotFound
		}
		return err
	}
	item, raw, err = mutate(tx, item, raw, sealed)
	if err != nil {
		return err
	}
	resultItem, resultRaw = item, raw
	return nil
	})
	if err != nil { return TokenRecord{}, "", err }
	return resultItem, resultRaw, nil
}

type rowScanner interface{ Scan(...any) error }

func scanTokenRecord(row rowScanner) (TokenRecord, error) {
	var item TokenRecord
	var created int64
	var rotated, expires, lastUsed, revoked sql.NullInt64
	err := row.Scan(&item.ID, &item.OwnerID, &item.Kind, &item.Name, &item.Prefix, &item.Last4, &created, &rotated, &expires, &lastUsed, &revoked)
	if err != nil {
		return item, err
	}
	item.CreatedAt = time.Unix(created, 0).UTC()
	item.RotatedAt = unixPointer(rotated)
	item.ExpiresAt = unixPointer(expires)
	item.LastUsed = unixPointer(lastUsed)
	item.RevokedAt = unixPointer(revoked)
	return item, nil
}

func unixPointer(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.Unix(value.Int64, 0).UTC()
	return &t
}

func tokenHash(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func newOwnerToken() (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	result := make([]byte, 32)
	buffer := make([]byte, 64)
	for offset := 0; offset < len(result); {
		if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
			return "", err
		}
		for _, value := range buffer {
			if value >= 248 {
				continue
			}
			result[offset] = alphabet[int(value)%len(alphabet)]
			offset++
			if offset == len(result) {
				break
			}
		}
	}
	return "own_" + string(result), nil
}

func validOwnerTokenFormat(raw string) bool {
	if len(raw) != 36 || !strings.HasPrefix(raw, "own_") {
		return false
	}
	for _, value := range raw[4:] {
		if !strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", value) {
			return false
		}
	}
	return true
}

func randomID() (string, error) {
	b := make([]byte, 18)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func encryptOwnerToken(encodedKey, raw string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return "", fmt.Errorf("encryption key must be base64 encoding of 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(raw), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decryptOwnerToken(encodedKey, encoded string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return "", fmt.Errorf("encryption key must be base64 encoding of 32 bytes")
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return "", fmt.Errorf("encrypted token is truncated")
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func ensureTokenLast4(ctx context.Context, db *sql.DB) (resultErr error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(api_tokens)`)
	if err != nil {
		return err
	}
	present := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primary int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primary); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "token_last4" {
			present = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if present {
		return nil
	}
	err = Write(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE api_tokens ADD COLUMN token_last4 TEXT NOT NULL DEFAULT ''`)
		return err
	})
	if err == nil {
		return nil
	}
	rows, checkErr := db.QueryContext(ctx, `PRAGMA table_info(api_tokens)`)
	if checkErr != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, primary int
		var defaultValue any
		if scanErr := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primary); scanErr != nil {
			return scanErr
		}
		if name == "token_last4" {
			return nil
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return rowsErr
	}
	return err
}
