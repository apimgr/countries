package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLiteGatewayRoutesReturningWriteThroughWriter(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close(db) })
	if _, err := db.Exec(`CREATE TABLE gateway_returning_test (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := db.QueryRow(`INSERT INTO gateway_returning_test(value) VALUES (?) RETURNING value`, "saved").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "saved" {
		t.Fatalf("RETURNING value = %q, want saved", value)
	}
}

func TestSQLiteStoreConcurrentReadersAndWriters(t *testing.T) {
	store, err := OpenSQLiteStore(filepath.Join(t.TempDir(), "concurrent.db"), 25)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.writeDB.Exec(`CREATE TABLE concurrent_test (id INTEGER PRIMARY KEY, value INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	var errMu sync.Mutex
	var failures []string
	record := func(err error) {
		if err == nil {
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		errMu.Lock()
		failures = append(failures, err.Error())
		errMu.Unlock()
	}
	for worker := 0; worker < 20; worker++ {
		wait.Add(2)
		go func(worker int) {
			defer wait.Done()
			for ctx.Err() == nil {
				err := store.Write(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `INSERT INTO concurrent_test(value) VALUES (?)`, worker)
					return err
				})
				record(err)
			}
		}(worker)
		go func() {
			defer wait.Done()
			for ctx.Err() == nil {
				var count int
				if err := store.readDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM concurrent_test`).Scan(&count); err != nil {
					record(err)
				}
			}
		}()
	}
	wait.Wait()
	for _, failure := range failures {
		if strings.Contains(strings.ToLower(failure), "database is locked") || strings.Contains(strings.ToUpper(failure), "SQLITE_BUSY") || strings.Contains(strings.ToUpper(failure), "SQLITE_LOCKED") {
			t.Fatalf("SQLite concurrency contract failed: %s", failure)
		}
	}
	if len(failures) > 0 {
		t.Fatalf("SQLite concurrency reported errors: %s", fmt.Sprint(failures))
	}
}
