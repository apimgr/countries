package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
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
