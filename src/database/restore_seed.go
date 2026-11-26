package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SeedRemoteIfEmpty copies the restored SQLite data into an initialized but
// otherwise empty libSQL database. Existing remote data is never overwritten.
func SeedRemoteIfEmpty(ctx context.Context, remote *sql.DB, sqlitePath string) (seeded bool, resultErr error) {
	if remote == nil {
		return false, errors.New("remote database handle is required")
	}
	if _, err := os.Stat(sqlitePath); err != nil {
		return false, fmt.Errorf("restored SQLite database is unavailable: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var tableNames []string
	rows, err := remote.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'srv_%' ORDER BY name`)
	if err != nil {
		return false, fmt.Errorf("inspect remote database tables: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return false, err
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if len(tableNames) == 0 {
		return false, errors.New("remote database schema is not initialized")
	}
	for _, table := range tableNames {
		canonical := strings.TrimPrefix(table, "srv_")
		if DatabaseIdentifier("libsql", canonical) != table {
			continue
		}
		switch canonical {
		case "config_meta":
			var count, version int
			if err := remote.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(version), 0) FROM `+quoteSQLIdentifier(table, table)).Scan(&count, &version); err != nil {
				return false, err
			}
			if count > 1 || count == 1 && version != 1 {
				return false, nil
			}
		case "health_request_stats":
			var count, requests int
			if err := remote.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(requests_total), 0) FROM `+quoteSQLIdentifier(table, table)).Scan(&count, &requests); err != nil {
				return false, err
			}
			if count > 1 || count == 1 && requests != 0 {
				return false, nil
			}
		default:
			var count int64
			if err := remote.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteSQLIdentifier(table, table)).Scan(&count); err != nil {
				return false, err
			}
			if count != 0 {
				return false, nil
			}
		}
	}
	local, err := openRestoredSQLite(ctx, sqlitePath)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := local.Close(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	err = Write(ctx, remote, func(tx *sql.Tx) error {
		for _, remoteTable := range tableNames {
			canonical := strings.TrimPrefix(remoteTable, "srv_")
			if DatabaseIdentifier("libsql", canonical) != remoteTable {
				continue
			}
			remoteName := quoteSQLIdentifier(remoteTable, remoteTable)
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+remoteName); err != nil {
				return fmt.Errorf("clear empty remote table %s: %w", canonical, err)
			}
		}
		orderedTables := append([]string(nil), tableNames...)
		for index, name := range orderedTables {
			if strings.TrimPrefix(name, "srv_") == "owners" {
				orderedTables[0], orderedTables[index] = orderedTables[index], orderedTables[0]
				break
			}
		}
		for _, remoteTable := range orderedTables {
			canonical := strings.TrimPrefix(remoteTable, "srv_")
			if DatabaseIdentifier("libsql", canonical) != remoteTable {
				continue
			}
			localTable := quoteSQLIdentifier(canonical, canonical)
			remoteName := quoteSQLIdentifier(remoteTable, remoteTable)
			localRows, err := local.QueryContext(ctx, `SELECT * FROM `+localTable)
			if err != nil {
				return fmt.Errorf("read restored table %s: %w", canonical, err)
			}
			columns, err := localRows.Columns()
			if err != nil {
				_ = localRows.Close()
				return err
			}
			quotedColumns := make([]string, len(columns))
			placeholders := make([]string, len(columns))
			for index, column := range columns {
				quotedColumns[index] = quoteSQLIdentifier(column, column)
				placeholders[index] = "?"
			}
			insertSQL := `INSERT INTO ` + remoteName + ` (` + strings.Join(quotedColumns, ",") + `) VALUES (` + strings.Join(placeholders, ",") + `)`
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for index := range values {
				pointers[index] = &values[index]
			}
			for localRows.Next() {
				if err := localRows.Scan(pointers...); err != nil {
					_ = localRows.Close()
					return err
				}
				if _, err := tx.ExecContext(ctx, insertSQL, values...); err != nil {
					_ = localRows.Close()
					return fmt.Errorf("write restored table %s: %w", canonical, err)
				}
			}
			if err := localRows.Err(); err != nil {
				_ = localRows.Close()
				return err
			}
			if err := localRows.Close(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("seed remote restore database: %w", err)
	}
	return true, nil
}

func openRestoredSQLite(ctx context.Context, path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parsed := (&url.URL{Scheme: "file", Path: absPath}).String() + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)"
	db, err := sql.Open("countries-sqlite-instrumented", parsed)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open restored SQLite database: %w", err)
	}
	return db, nil
}
