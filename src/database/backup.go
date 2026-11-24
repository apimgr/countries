package database

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const maxBackupHistoryRows = 1000

// BackupRecord contains non-secret metadata for one completed backup archive.
type BackupRecord struct {
	ID        int64
	Filename  string
	Filepath  string
	SizeBytes int64
	Type      string
	CreatedAt time.Time
	Checksum  string
	Notes     string
}

// RecordBackup stores metadata after the corresponding archive is created and verified.
func RecordBackup(ctx context.Context, db *sql.DB, record BackupRecord) error {
	if db == nil {
		return errors.New("backup history requires a database")
	}
	record.Filename = strings.TrimSpace(record.Filename)
	record.Filepath = strings.TrimSpace(record.Filepath)
	record.Type = strings.TrimSpace(record.Type)
	record.Checksum = strings.TrimSpace(record.Checksum)
	record.Notes = strings.TrimSpace(record.Notes)
	if record.Filename == "" || record.Filename == "." || record.Filename == ".." || filepath.Base(record.Filename) != record.Filename || strings.ContainsAny(record.Filename, `/\\`) || strings.ContainsRune(record.Filename, '\x00') || len(record.Filename) > 255 {
		return errors.New("backup filename is invalid")
	}
	if record.Filepath == "" || len(record.Filepath) > 4096 || strings.ContainsRune(record.Filepath, '\x00') {
		return errors.New("backup filepath is invalid")
	}
	if record.SizeBytes < 0 {
		return errors.New("backup size cannot be negative")
	}
	if record.Type == "" {
		record.Type = "auto"
	}
	if record.Type != "auto" && record.Type != "manual" && record.Type != "pre_update" {
		return errors.New("backup type is invalid")
	}
	if record.Checksum != "" {
		decoded, err := hex.DecodeString(record.Checksum)
		if err != nil || len(decoded) != 32 {
			return errors.New("backup checksum must be a SHA-256 hex digest")
		}
		record.Checksum = strings.ToLower(record.Checksum)
	}
	if len(record.Notes) > 1024 {
		return errors.New("backup notes exceed 1024 bytes")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	} else {
		record.CreatedAt = record.CreatedAt.UTC()
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `INSERT INTO backups (filename, filepath, size_bytes, type, created_at, checksum, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(filename) DO UPDATE SET filepath=excluded.filepath, size_bytes=excluded.size_bytes, type=excluded.type,
		created_at=excluded.created_at, checksum=excluded.checksum, notes=excluded.notes`,
		record.Filename, record.Filepath, record.SizeBytes, record.Type, record.CreatedAt.Unix(), nullableBackupValue(record.Checksum), nullableBackupValue(record.Notes))
	if err != nil {
		return fmt.Errorf("record backup metadata: %w", err)
	}
	return nil
}

// ListBackups returns the newest backup records first, bounded by limit.
func ListBackups(ctx context.Context, db *sql.DB, limit int) (records []BackupRecord, resultErr error) {
	if db == nil {
		return nil, errors.New("backup history requires a database")
	}
	if limit < 1 || limit > maxBackupHistoryRows {
		return nil, fmt.Errorf("backup history limit must be between 1 and %d", maxBackupHistoryRows)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT id, filename, filepath, size_bytes, type, created_at, COALESCE(checksum, ''), COALESCE(notes, '')
		FROM backups ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list backup metadata: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	records = make([]BackupRecord, 0, limit)
	for rows.Next() {
		var record BackupRecord
		var createdAt int64
		if err := rows.Scan(&record.ID, &record.Filename, &record.Filepath, &record.SizeBytes, &record.Type, &createdAt, &record.Checksum, &record.Notes); err != nil {
			return nil, fmt.Errorf("scan backup metadata: %w", err)
		}
		record.CreatedAt = time.Unix(createdAt, 0).UTC()
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read backup metadata: %w", err)
	}
	return records, nil
}

// DeleteBackupMetadata removes the history row for a deleted archive.
func DeleteBackupMetadata(ctx context.Context, db *sql.DB, filename string) error {
	if db == nil {
		return errors.New("backup history requires a database")
	}
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." || filepath.Base(filename) != filename || strings.ContainsAny(filename, `/\\`) || strings.ContainsRune(filename, '\x00') || len(filename) > 255 {
		return errors.New("backup filename is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `DELETE FROM backups WHERE filename = ?`, filename); err != nil {
		return fmt.Errorf("delete backup metadata: %w", err)
	}
	return nil
}

func nullableBackupValue(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
