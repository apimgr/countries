package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SQLiteStore keeps one serialized writer and a separate query-only reader pool.
type SQLiteStore struct {
	writeDB *sql.DB
	readDB  *sql.DB
}

// OpenSQLiteStore opens the two SQLite handles required by the concurrency contract.
func OpenSQLiteStore(path string, readers int) (*SQLiteStore, error) {
	return OpenSQLiteStoreWithPool(path, PoolOptions{MaxOpen: readers, MaxIdle: readers, MaxLifetime: 5 * time.Minute, MaxIdleTime: time.Minute})
}

// OpenSQLiteStoreWithPool applies the configured reader pool while keeping the writer fixed at one connection.
func OpenSQLiteStoreWithPool(path string, pool PoolOptions) (*SQLiteStore, error) {
	pool = normalizePoolOptions(pool)
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	base := (&url.URL{Scheme: "file", Path: absPath}).String() + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	writer, err := sql.Open("countries-sqlite-instrumented", base+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open SQLite writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	writer.SetConnMaxLifetime(0)
	reader, err := sql.Open("countries-sqlite-instrumented", base+"&_pragma=query_only(1)")
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("open SQLite readers: %w", err)
	}
	reader.SetMaxOpenConns(pool.MaxOpen)
	reader.SetMaxIdleConns(pool.MaxIdle)
	reader.SetConnMaxLifetime(pool.MaxLifetime)
	reader.SetConnMaxIdleTime(pool.MaxIdleTime)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := writer.PingContext(ctx); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("verify SQLite writer: %w", err)
	}
	if err := reader.PingContext(ctx); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("verify SQLite readers: %w", err)
	}
	var journal string
	if err := writer.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil || !strings.EqualFold(journal, "wal") {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("SQLite database %q did not enable WAL mode", absPath)
	}
	if err := EnsureSchema(ctx, writer); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	if err := os.Chmod(absPath, 0o600); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("secure database file: %w", err)
	}
	return &SQLiteStore{writeDB: writer, readDB: reader}, nil
}

// Read returns the query-only handle. Callers must close result rows before writing.
func (store *SQLiteStore) Read() *sql.DB { return store.readDB }

// Write executes a short transaction with bounded busy retries.
func (store *SQLiteStore) Write(ctx context.Context, callback func(*sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		waitStarted := time.Now()
		tx, beginErr := store.writeDB.BeginTx(ctx, nil)
		recordWriteWait(time.Since(waitStarted))
		if beginErr == nil {
			if callbackErr := callback(tx); callbackErr != nil {
				_ = tx.Rollback()
				err = callbackErr
			} else {
				err = tx.Commit()
			}
		} else {
			err = beginErr
		}
		if !isSQLiteBusy(err) || attempt == 4 {
			return err
		}
		recordBusyRetry()
		wait := time.Duration(25*(1<<attempt))*time.Millisecond + time.Duration(rand.Intn(25))*time.Millisecond
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "SQLITE_BUSY") || strings.Contains(message, "SQLITE_LOCKED") || strings.Contains(message, "DATABASE IS LOCKED")
}

// Close checkpoints WAL before releasing the reader and writer handles.
func (store *SQLiteStore) Close() error {
	if store == nil {
		return nil
	}
	_, checkpointErr := store.writeDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	readErr := store.readDB.Close()
	writeErr := store.writeDB.Close()
	return errors.Join(checkpointErr, readErr, writeErr)
}
