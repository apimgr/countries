package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
)

func TestBackupHistoryRecordUpdateListAndDelete(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	first := BackupRecord{
		Filename:  "countries_backup_2026-11-21.tar.gz",
		Filepath:  "/backups/countries_backup_2026-11-21.tar.gz",
		SizeBytes: 123,
		Type:      "auto",
		CreatedAt: time.Unix(100, 0),
		Checksum:  strings.Repeat("a", 64),
	}
	if err := RecordBackup(ctx, db, first); err != nil {
		t.Fatal(err)
	}
	second := BackupRecord{
		Filename:  "countries_backup_2026-11-22.tar.gz",
		Filepath:  "/backups/countries_backup_2026-11-22.tar.gz",
		SizeBytes: 456,
		Type:      "manual",
		CreatedAt: time.Unix(200, 0),
	}
	if err := RecordBackup(ctx, db, second); err != nil {
		t.Fatal(err)
	}
	updated := second
	updated.Filepath = "/moved/countries_backup_2026-11-22.tar.gz"
	updated.SizeBytes = 789
	updated.Notes = "verified after relocation"
	updated.CreatedAt = time.Unix(300, 0)
	if err := RecordBackup(ctx, db, updated); err != nil {
		t.Fatal(err)
	}

	records, err := ListBackups(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("backup history has %d records, want 2", len(records))
	}
	if records[0].Filename != second.Filename || records[0].Filepath != updated.Filepath || records[0].SizeBytes != updated.SizeBytes || records[0].Notes != updated.Notes || !records[0].CreatedAt.Equal(updated.CreatedAt) {
		t.Fatalf("updated backup record = %+v, want %+v", records[0], updated)
	}
	if records[1].Filename != first.Filename || records[1].Checksum != first.Checksum || records[1].CreatedAt.Unix() != first.CreatedAt.Unix() {
		t.Fatalf("first backup record = %+v, want metadata for %q", records[1], first.Filename)
	}

	if err := DeleteBackupMetadata(ctx, db, first.Filename); err != nil {
		t.Fatal(err)
	}
	records, err = ListBackups(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Filename != second.Filename {
		t.Fatalf("backup history after delete = %+v, want only %q", records, second.Filename)
	}
}

func TestBackupHistoryValidatesInputsAndBoundsListing(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	invalid := []BackupRecord{
		{Filename: "../backup.tar.gz", Filepath: "/backups/backup.tar.gz", Type: "manual"},
		{Filename: "backup.tar.gz", Filepath: "/backups/backup.tar.gz", SizeBytes: -1, Type: "manual"},
		{Filename: "backup.tar.gz", Filepath: "/backups/backup.tar.gz", Type: "unknown"},
		{Filename: "backup.tar.gz", Filepath: "/backups/backup.tar.gz", Type: "manual", Checksum: "invalid"},
		{Filename: "backup.tar.gz", Filepath: "/backups/backup.tar.gz", Type: "manual", Notes: strings.Repeat("n", 1025)},
	}
	for index, record := range invalid {
		if err := RecordBackup(ctx, db, record); err == nil {
			t.Errorf("invalid backup record %d was accepted", index)
		}
	}
	for _, limit := range []int{0, -1, maxBackupHistoryRows + 1} {
		if _, err := ListBackups(ctx, db, limit); err == nil {
			t.Errorf("history limit %d was accepted", limit)
		}
	}
	if err := DeleteBackupMetadata(ctx, db, "../backup.tar.gz"); err == nil {
		t.Error("unsafe backup filename was accepted for deletion")
	}
	records, err := ListBackups(ctx, db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("invalid records were stored: %+v", records)
	}
}
