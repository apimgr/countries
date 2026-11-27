package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestReconcileReplicaTableAppliesOnlyChangedRows(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "replica.db"), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.writeDB.Exec(`INSERT INTO config(key,value,type,updated_at) VALUES ('changed','old','string',1),('removed','gone','string',1)`); err != nil {
		t.Fatal(err)
	}
	var priorRowID int64
	if err := store.readDB.QueryRow(`SELECT rowid FROM config WHERE key='changed'`).Scan(&priorRowID); err != nil {
		t.Fatal(err)
	}
	remote := replicaSnapshot{
		columns: []string{"key", "value", "type", "updated_at"},
		records: [][]any{{"changed", "new", "string", int64(2)}, {"added", "present", "string", int64(2)}},
	}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		return reconcileReplicaTable(context.Background(), tx, "config", remote)
	}); err != nil {
		t.Fatal(err)
	}
	var value string
	var rowID int64
	if err := store.readDB.QueryRow(`SELECT value, rowid FROM config WHERE key='changed'`).Scan(&value, &rowID); err != nil {
		t.Fatal(err)
	}
	if value != "new" || rowID != priorRowID {
		t.Fatalf("changed row = (%q, %d), want updated value and preserved row identity %d", value, rowID, priorRowID)
	}
	var count int
	if err := store.readDB.QueryRow(`SELECT COUNT(*) FROM config WHERE key='removed'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("remote-deleted row count = %d, err = %v; want 0", count, err)
	}
	if err := store.readDB.QueryRow(`SELECT COUNT(*) FROM config WHERE key='added'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remote-inserted row count = %d, err = %v; want 1", count, err)
	}
}

func TestReplicaNeverCachesApplicationSecrets(t *testing.T) {
	for _, excluded := range replicaExcludedTables {
		for _, table := range replicaTables {
			if table == excluded {
				t.Fatalf("excluded table %q is present in the local replica scope", excluded)
			}
		}
		if _, exists := replicaPrimaryKeys[excluded]; exists {
			t.Fatalf("excluded table %q has a replica primary-key mapping", excluded)
		}
	}
}

func TestReplicaRequiresFreshSyncForOfflineStartup(t *testing.T) {
	replica := &ReadReplica{maxAge: time.Hour, updated: make(map[string]time.Time), dirty: make(map[string]bool)}
	if replica.HasFreshCache() {
		t.Fatal("unsynchronized cache was considered usable")
	}
	for _, table := range replicaTables {
		replica.updated[table] = time.Now()
	}
	if !replica.HasFreshCache() {
		t.Fatal("fresh synchronized cache was rejected")
	}
	replica.dirty[replicaTables[0]] = true
	if replica.HasFreshCache() {
		t.Fatal("dirty cache was considered usable")
	}
}

func TestReplicaSnapshotWatermarkUsesHighestUpdatedAtValue(t *testing.T) {
	snapshot := replicaSnapshot{columns: []string{"id", "updated_at"}, records: [][]any{{int64(1), int64(9)}, {int64(2), int64(12)}, {int64(3), int64(4)}}}
	if got := replicaSnapshotWatermark(snapshot); got != "12" {
		t.Fatalf("numeric watermark = %q, want 12", got)
	}
	snapshot = replicaSnapshot{columns: []string{"updated_at"}, records: [][]any{{"2026-01-01T00:00:00Z"}, {"2026-01-02T00:00:00Z"}}}
	if got := replicaSnapshotWatermark(snapshot); got != "2026-01-02T00:00:00Z" {
		t.Fatalf("timestamp watermark = %q", got)
	}
	if got := replicaSnapshotWatermark(replicaSnapshot{columns: []string{"name"}}); got != "" {
		t.Fatalf("watermark without updated_at = %q, want empty", got)
	}
}

func TestOpenReadReplicaPurgesPreviouslyCachedExcludedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replica.db")
	store, err := OpenSQLiteStore(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('cookie_signing_key','old-cache-secret','2026-01-01T00:00:00Z')`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`INSERT INTO audit_log(category,action) VALUES('test','old-cache-event')`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`INSERT INTO rate_limits(key,count,window_start) VALUES('old-cache-counter',1,1)`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`CREATE TABLE replica_state(table_name TEXT PRIMARY KEY, updated_at INTEGER NOT NULL, dirty INTEGER NOT NULL DEFAULT 0)`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`INSERT INTO replica_state(table_name,updated_at,dirty) VALUES('owners',?,0)`, time.Now().Unix()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`INSERT INTO config(key,value,type,updated_at) VALUES('cached-setting','cached','string',42)`); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.writeDB.Exec(`INSERT INTO replica_state(table_name,updated_at,dirty) VALUES('config',?,0)`, time.Now().Unix()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	replica, err := OpenReadReplica(path, ReplicaOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replica.Close() })
	for _, table := range replicaExcludedTables {
		var count int
		if err := replica.store.readDB.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&count); err != nil {
			t.Fatalf("count rows in %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("excluded table %s retains %d old replica rows", table, count)
		}
	}
	var syncedAt string
	if err := replica.store.readDB.QueryRow(`SELECT synced_at FROM cache_meta WHERE table_name='owners'`).Scan(&syncedAt); err != nil || syncedAt == "" {
		t.Fatalf("migrated cache metadata synced_at = %q, %v", syncedAt, err)
	}
	var watermark string
	if err := replica.store.readDB.QueryRow(`SELECT watermark FROM cache_meta WHERE table_name='config'`).Scan(&watermark); err != nil || watermark != "42" {
		t.Fatalf("migrated config watermark = %q, %v", watermark, err)
	}
	var legacyTableCount int
	if err := replica.store.readDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='replica_state'`).Scan(&legacyTableCount); err != nil || legacyTableCount != 0 {
		t.Fatalf("legacy replica_state table count = %d, %v", legacyTableCount, err)
	}
}
