package database

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/apimgr/countries/src/testutil"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnerTokenLifecycleAndMetadataIsolation(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	ctx := context.Background()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	primary, rawPrimary, err := CreateOwnerToken(ctx, db, key)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	if primary.Kind != "primary" || primary.OwnerID == "" || primary.Last4 != rawPrimary[len(rawPrimary)-4:] {
		t.Fatalf("created primary metadata = %+v", primary)
	}
	identity, err := LookupOwnerToken(ctx, db, rawPrimary, now)
	if err != nil || identity.OwnerID != primary.OwnerID || identity.Kind != "primary" || !identity.Active || identity.ExpiresAt != nil {
		t.Fatalf("LookupOwnerToken() = (%+v, %v)", identity, err)
	}
	if _, err := LookupOwnerToken(ctx, db, "own_invalid", now); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("invalid token error = %v, want ErrTokenNotFound", err)
	}

	listed, err := ListOwnerTokens(ctx, db, primary.OwnerID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListOwnerTokens() = (%+v, %v), want one metadata record", listed, err)
	}
	if listed[0].Last4 != primary.Last4 || listed[0].Prefix != rawPrimary[:12] {
		t.Fatalf("listed token metadata = %+v", listed[0])
	}
	metadataJSON, err := json.Marshal(listed)
	if err != nil || strings.Contains(string(metadataJSON), rawPrimary) {
		t.Fatalf("listed metadata exposed raw token value: %s (%v)", metadataJSON, err)
	}
	revealed, _, err := RevealOwnerToken(ctx, db, primary.OwnerID, primary.ID, key)
	if err != nil || revealed != rawPrimary {
		t.Fatalf("RevealOwnerToken() = (%q, %v)", revealed, err)
	}

	expires := now.Add(24 * time.Hour)
	linked, rawLinked, err := CreateLinkedOwnerToken(ctx, db, primary.OwnerID, "build agent", &expires, 2, key, now)
	if err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	if linked.Kind != "linked" || linked.Name != "build agent" || linked.ExpiresAt == nil || !linked.ExpiresAt.Equal(expires) {
		t.Fatalf("created linked metadata = %+v", linked)
	}
	if _, _, err := CreateLinkedOwnerToken(ctx, db, primary.OwnerID, "BUILD AGENT", nil, 2, key, now); !errors.Is(err, ErrTokenName) {
		t.Fatalf("duplicate name error = %v, want ErrTokenName", err)
	}
	second, _, err := CreateLinkedOwnerToken(ctx, db, primary.OwnerID, "another", nil, 2, key, now)
	if err != nil {
		t.Fatalf("CreateLinkedOwnerToken(second): %v", err)
	}
	if err := UpdateLinkedOwnerToken(ctx, db, primary.OwnerID, linked.ID, "ANOTHER", nil, now); !errors.Is(err, ErrTokenName) {
		t.Fatalf("duplicate rename error = %v, want ErrTokenName", err)
	}
	if _, _, err := CreateLinkedOwnerToken(ctx, db, primary.OwnerID, "second", nil, 1, key, now); !errors.Is(err, ErrTokenLimit) {
		t.Fatalf("token limit error = %v, want ErrTokenLimit", err)
	}

	rotated, newPrimary, err := RotateOwnerToken(ctx, db, primary.OwnerID, primary.ID, key, now.Add(time.Minute))
	if err != nil || rotated.ID != primary.ID || rotated.Kind != "primary" || newPrimary == rawPrimary {
		t.Fatalf("RotateOwnerToken() = (%+v, %q, %v)", rotated, newPrimary, err)
	}
	if _, err := LookupOwnerToken(ctx, db, rawPrimary, now.Add(2*time.Minute)); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("old primary remains valid: %v", err)
	}
	if _, err := LookupOwnerToken(ctx, db, rawLinked, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("rotating primary invalidated linked token: %v", err)
	}
	linkedIdentity, err := LookupOwnerToken(ctx, db, rawLinked, now.Add(2*time.Minute))
	if err != nil || !linkedIdentity.Active || linkedIdentity.Kind != "linked" || linkedIdentity.ExpiresAt == nil || !linkedIdentity.ExpiresAt.Equal(expires) {
		t.Fatalf("linked token identity = (%+v, %v), want active linked token with expiry", linkedIdentity, err)
	}

	if err := UpdateLinkedOwnerToken(ctx, db, primary.OwnerID, linked.ID, "release bot", nil, now); err != nil {
		t.Fatalf("UpdateLinkedOwnerToken(): %v", err)
	}
	if err := RevokeLinkedOwnerToken(ctx, db, primary.OwnerID, linked.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RevokeLinkedOwnerToken(): %v", err)
	}
	if _, err := LookupOwnerToken(ctx, db, rawLinked, now.Add(3*time.Minute)); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("revoked linked token remains valid: %v", err)
	}
	listed, err = ListOwnerTokens(ctx, db, primary.OwnerID)
	if err != nil || len(listed) != 3 || listed[1].Name != "release bot" || listed[1].RevokedAt == nil || listed[2].ID != second.ID {
		t.Fatalf("linked token metadata after update/revoke = (%+v, %v)", listed, err)
	}
}

func TestOwnerTokenExpiryAndSchemaMigration(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	ctx := context.Background()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	primary, _, err := CreateOwnerToken(ctx, db, key)
	if err != nil {
		t.Fatalf("CreateOwnerToken(): %v", err)
	}
	expiry := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	_, raw, err := CreateLinkedOwnerToken(ctx, db, primary.OwnerID, "short-lived", &expiry, 5, key, expiry.Add(-time.Hour))
	if err != nil {
		t.Fatalf("CreateLinkedOwnerToken(): %v", err)
	}
	if _, err := LookupOwnerToken(ctx, db, raw, expiry); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expired token lookup error = %v, want ErrTokenNotFound", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE api_tokens DROP COLUMN token_last4`); err != nil {
		t.Fatalf("remove token_last4 to simulate existing database: %v", err)
	}
	if err := ensureTokenLast4(ctx, db); err != nil {
		t.Fatalf("token_last4 migration: %v", err)
	}
	if err := ensureTokenLast4(ctx, db); err != nil {
		t.Fatalf("repeated token_last4 migration: %v", err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('api_tokens') WHERE name = 'token_last4'`).Scan(&rows); err != nil {
		t.Fatalf("inspect token_last4 migration: %v", err)
	}
	if rows != 1 {
		t.Fatalf("token_last4 column count = %d, want 1", rows)
	}
}

func TestCleanupOwnerTokenStateExpiresChallengesAndPurgesIdleOwners(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	oldOwner, _, err := CreateOwnerToken(ctx, db, key)
	if err != nil {
		t.Fatalf("CreateOwnerToken(old): %v", err)
	}
	newOwner, _, err := CreateOwnerToken(ctx, db, key)
	if err != nil {
		t.Fatalf("CreateOwnerToken(new): %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE owners SET last_seen_at = ? WHERE id = ?`, now.AddDate(0, 0, -31).Unix(), oldOwner.OwnerID); err != nil {
		t.Fatalf("set idle owner timestamp: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO captcha_challenges(id, answer_hash, expires_at) VALUES('expired', 'hash', ?), ('live', 'hash', ?)`, now.Add(-time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatalf("insert challenges: %v", err)
	}
	challengeCount, ownerCount, err := CleanupOwnerTokenState(ctx, db, now, 30)
	if err != nil || challengeCount != 1 || ownerCount != 1 {
		t.Fatalf("CleanupOwnerTokenState() = (%d, %d, %v), want (1, 1, nil)", challengeCount, ownerCount, err)
	}
	var oldTokens, newOwners, liveChallenges int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = ?`, oldOwner.OwnerID).Scan(&oldTokens); err != nil {
		t.Fatalf("count old owner tokens: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id = ?`, newOwner.OwnerID).Scan(&newOwners); err != nil {
		t.Fatalf("count recent owner: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM captcha_challenges WHERE id = 'live'`).Scan(&liveChallenges); err != nil {
		t.Fatalf("count live challenge: %v", err)
	}
	if oldTokens != 0 || newOwners != 1 || liveChallenges != 1 {
		t.Fatalf("post-cleanup counts: old tokens=%d recent owners=%d live challenges=%d", oldTokens, newOwners, liveChallenges)
	}
	if _, ownerCount, err := CleanupOwnerTokenState(ctx, db, now, 0); err != nil || ownerCount != 0 {
		t.Fatalf("disabled idle purge removed owners: count=%d error=%v", ownerCount, err)
	}
}
