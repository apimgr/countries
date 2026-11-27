package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/paths"
	_ "modernc.org/sqlite"
)

func TestInstallFullBackupWithoutOverwrite(t *testing.T) {
	directory := t.TempDir()
	candidate := filepath.Join(directory, "candidate.tar.gz")
	destination := filepath.Join(directory, "backup.tar.gz")
	if err := os.WriteFile(candidate, []byte("verified backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installFullWithoutOverwrite(candidate, destination); err != nil {
		t.Fatalf("install full backup: %v", err)
	}
	if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate remains after install: %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "verified backup" {
		t.Fatalf("installed backup = %q, error = %v", content, err)
	}
	if err := os.WriteFile(candidate, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installFullWithoutOverwrite(candidate, destination); err == nil {
		t.Fatal("install replaced an existing full backup")
	}
	content, err = os.ReadFile(destination)
	if err != nil || string(content) != "verified backup" {
		t.Fatalf("existing backup after collision = %q, error = %v", content, err)
	}
}

func TestNewestFullArchiveUsesFilenameDateAndMtimeTieBreak(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "countries_backup_2026-11-24.tar.gz")
	first := filepath.Join(dir, "countries_backup_2026-11-25.tar.gz.enc")
	newer := filepath.Join(dir, "countries_backup_2026-11-25_020000.tar.gz")
	for _, path := range []string{older, first, newer, filepath.Join(dir, "countries-daily.tar.gz")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(first, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, base.Add(time.Second), base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := NewestFullArchive(context.Background(), "countries", dir)
	if err != nil {
		t.Fatalf("NewestFullArchive() error = %v", err)
	}
	if got != newer {
		t.Fatalf("NewestFullArchive() = %q, want %q", got, newer)
	}
}

func TestNewestFullArchiveIgnoresSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "countries_backup_2026-11-25.tar.gz")
	if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := NewestFullArchive(context.Background(), "countries", dir)
	if err != nil {
		t.Fatalf("NewestFullArchive() error = %v", err)
	}
	if got != "" {
		t.Fatalf("NewestFullArchive() = %q, want no archive", got)
	}
}

func TestNewestFullArchiveHandlesMissingDirectoryAndCancellation(t *testing.T) {
	if _, err := NewestFullArchive(context.Background(), "", t.TempDir()); err == nil {
		t.Fatal("NewestFullArchive() accepted an empty project name")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if got, err := NewestFullArchive(context.Background(), "countries", missing); err != nil || got != "" {
		t.Fatalf("NewestFullArchive(missing directory) = %q, %v; want empty result", got, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "countries_backup_2026-11-25.tar.gz"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewestFullArchive(ctx, "countries", dir); err == nil {
		t.Fatal("NewestFullArchive() ignored canceled context")
	}
}

func TestBackupExportsAndRestoresAllApplicationSecrets(t *testing.T) {
	root := createBackupTestRoot(t)
	source := makeBackupFixture(t, root, "source\n")
	archive := filepath.Join(root, "secrets.tar.gz")
	if _, err := Create(context.Background(), archive, source.options); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	verified, err := Verify(context.Background(), archive, "", Limits{})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	payload, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := readArchive(context.Background(), payload, int64(len(payload)), normalizedLimits(Limits{}))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	var exported []appSecretRecord
	if err := json.Unmarshal(entries["app_secrets.json"], &exported); err != nil {
		t.Fatalf("decode exported secrets: %v", err)
	}
	if len(exported) != 3 || exported[1].Name != "encryption_key_previous_until" || exported[2].Name != "installation_secret_previous_until" {
		t.Fatalf("exported secret rows = %+v", exported)
	}
	if verified.Manifest.Type != "full" {
		t.Fatalf("manifest type = %q, want full", verified.Manifest.Type)
	}
	destination := filepath.Join(root, "restored")
	databasePath := filepath.Join(destination, "data", "db", "server.db")
	secretDB, err := sql.Open("sqlite", filepath.Join(root, "remote-secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	secretDB.SetMaxOpenConns(1)
	defer func() { _ = secretDB.Close() }()
	if _, err := secretDB.Exec(`CREATE TABLE app_secrets(name TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := secretDB.Exec(`INSERT INTO app_secrets VALUES('obsolete','old','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), archive, "", RestoreOptions{ConfigDir: filepath.Join(destination, "config"), DataDir: filepath.Join(destination, "data"), DatabasePath: databasePath, SecretsDB: secretDB}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	var value string
	if err := secretDB.QueryRow(`SELECT value FROM app_secrets WHERE name='encryption_key_previous_until'`).Scan(&value); err != nil || value != "2030-01-01T00:00:00Z" {
		t.Fatalf("restored grace-period secret = %q, err = %v", value, err)
	}
	var obsolete int
	if err := secretDB.QueryRow(`SELECT COUNT(*) FROM app_secrets WHERE name='obsolete'`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatalf("obsolete secret count = %d, err=%v", obsolete, err)
	}
}

func TestDecodeAppSecretsRejectsUnsafeDocuments(t *testing.T) {
	for _, data := range []string{
		`null`,
		`{}`,
		`[{"name":"secret","value":"one","updated_at":"2029-12-02T00:00:00Z"},{"name":"secret","value":"two","updated_at":"2029-12-02T00:00:00Z"}]`,
		`[{"name":"secret","value":"one","updated_at":"invalid"}]`,
		`[{"name":"secret","value":"one","updated_at":"2029-12-02T00:00:00Z","unexpected":true}]`,
		`[] []`,
	} {
		if _, err := decodeAppSecrets([]byte(data)); err == nil {
			t.Errorf("decodeAppSecrets(%s) succeeded", data)
		}
	}
	secrets, err := decodeAppSecrets([]byte(`[{"name":"secret","value":"value","updated_at":"2029-12-02T00:00:00Z"}]`))
	if err != nil || len(secrets) != 1 || secrets[0].Value != "value" {
		t.Fatalf("decodeAppSecrets(valid) = %+v, %v", secrets, err)
	}
}

func TestCreateVerifyAndRestoreEncryptedFullBackup(t *testing.T) {
	root := createBackupTestRoot(t)
	source := makeBackupFixture(t, root, "original config\n")
	securityDir := filepath.Join(source.options.ConfigDir, "security")
	if err := os.MkdirAll(securityDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"pgp.pub.asc":          "public-key\n",
		"pgp.priv.asc.enc":     "encrypted-private-key\n",
		"keyservers.state":     "published-state\n",
		"pgp.priv.old.asc.enc": "retired-encrypted-private-key\n",
	} {
		if err := os.WriteFile(filepath.Join(securityDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	basePath := filepath.Join(root, "backups", "countries_backup_2026-11-22_010203.tar.gz.enc")
	options := source.options
	options.Password = "correct horse battery staple"
	result, err := Create(context.Background(), basePath, options)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !result.Manifest.Encrypted || result.Manifest.EncryptionMethod != "AES-256-GCM" {
		t.Fatalf("encrypted manifest = %+v", result.Manifest)
	}
	if _, err := Verify(context.Background(), basePath, "wrong password", Limits{}); err == nil {
		t.Fatal("Verify() accepted an incorrect password")
	}
	if _, err := Verify(context.Background(), basePath, options.Password, Limits{}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	configDir := filepath.Join(root, "restored-config")
	dataDir := filepath.Join(root, "restored-data")
	databasePath := filepath.Join(dataDir, "db", "server.db")
	if _, err := Restore(context.Background(), basePath, options.Password, RestoreOptions{
		ConfigDir: configDir, DataDir: dataDir, DatabasePath: databasePath,
	}); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(configDir, "server.yml"))
	if err != nil || string(contents) != "original config\n" {
		t.Fatalf("restored server.yml = %q, err = %v", contents, err)
	}
	if _, err := os.Stat(databasePath); err != nil {
		t.Fatalf("restored database is missing: %v", err)
	}
	for name, want := range map[string]string{
		"pgp.pub.asc":          "public-key\n",
		"pgp.priv.asc.enc":     "encrypted-private-key\n",
		"keyservers.state":     "published-state\n",
		"pgp.priv.old.asc.enc": "retired-encrypted-private-key\n",
	} {
		content, err := os.ReadFile(filepath.Join(configDir, "security", name))
		if err != nil || string(content) != want {
			t.Errorf("restored security file %s = %q, error = %v", name, content, err)
		}
	}
}

func TestCreateIncrementalAndRestoreMergedSnapshot(t *testing.T) {
	root := createBackupTestRoot(t)
	source := makeBackupFixture(t, root, "first config\n")
	removedFile := filepath.Join(source.options.ConfigDir, "assets", "old.png")
	if err := os.MkdirAll(filepath.Dir(removedFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(removedFile, []byte("old asset"), 0600); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(root, "backups", "countries_backup_2026-11-21.tar.gz")
	base, err := Create(context.Background(), basePath, source.options)
	if err != nil {
		t.Fatalf("Create(full) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source.options.ConfigDir, "server.yml"), []byte("second config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(removedFile); err != nil {
		t.Fatal(err)
	}
	incrementalPath := filepath.Join(root, "backups", "countries-daily.tar.gz")
	incrementalOptions := source.options
	incrementalOptions.Type = "incremental"
	incrementalOptions.BaseBackup = filepath.Base(basePath)
	incrementalOptions.BaseArchivePath = basePath
	incrementalOptions.BaseSHA256 = base.SHA256
	incremental, err := Create(context.Background(), incrementalPath, incrementalOptions)
	if err != nil {
		t.Fatalf("Create(incremental) error = %v", err)
	}
	if incremental.Manifest.Type != "incremental" || len(incremental.Manifest.Deleted) != 1 || incremental.Manifest.Deleted[0] != "config/assets/old.png" {
		t.Fatalf("incremental manifest = %+v", incremental.Manifest)
	}
	configDir := filepath.Join(root, "incremental-config")
	dataDir := filepath.Join(root, "incremental-data")
	if _, err := Restore(context.Background(), basePath, "", RestoreOptions{ConfigDir: configDir, DataDir: dataDir}); err != nil {
		t.Fatalf("Restore(full) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "assets", "old.png")); err != nil {
		t.Fatalf("base asset is missing before incremental restore: %v", err)
	}
	if _, err := Restore(context.Background(), incrementalPath, "", RestoreOptions{ConfigDir: configDir, DataDir: dataDir}); err != nil {
		t.Fatalf("Restore(incremental) error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(configDir, "server.yml"))
	if err != nil || string(contents) != "second config\n" {
		t.Fatalf("restored incremental server.yml = %q, err = %v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "assets", "old.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted asset remains after incremental restore, stat error = %v", err)
	}
}

func TestReadArchiveRejectsUnsafePathsAndLinks(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "config\\..\\escape", "C:/absolute"} {
		if safeArchivePath(name) {
			t.Errorf("safeArchivePath(%q) = true", name)
		}
	}
	if safeArchivePath("config/server.yml") == false {
		t.Fatal("safeArchivePath rejected a relative application path")
	}
	archiveBytes, err := makeTarArchive(t, []tarFixture{{name: "../escape", content: []byte("unsafe")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(context.Background(), archiveBytes, int64(len(archiveBytes)), Limits{}); err == nil {
		t.Fatal("readArchive accepted a path traversal entry")
	}
	linkBytes, err := makeTarArchive(t, []tarFixture{{name: "config/link", typeFlag: tar.TypeSymlink, linkName: "../secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readArchive(context.Background(), linkBytes, int64(len(linkBytes)), Limits{}); err == nil {
		t.Fatal("readArchive accepted a symbolic link")
	}
}

func TestVerifyRejectsOversizedArchiveBeforeReading(t *testing.T) {
	root := createBackupTestRoot(t)
	path := filepath.Join(root, "too-large.tar.gz")
	if err := os.WriteFile(path, []byte("123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(context.Background(), path, "", Limits{MaxArchiveSize: 4})
	if err == nil || !strings.Contains(err.Error(), "invalid size") {
		t.Fatalf("Verify() error = %v, want archive size error", err)
	}
}

func TestSchedulerDailyThenHourlyUsesCumulativeBases(t *testing.T) {
	root := createBackupTestRoot(t)
	source := makeBackupFixture(t, root, "first config\n")
	backupDir := filepath.Join(root, "backups")
	now := time.Date(2026, time.November, 20, 2, 0, 0, 0, time.UTC)
	createCalls := 0
	scheduler, err := NewScheduler(ScheduledOptions{
		ProjectName: "countries",
		BackupDir:   backupDir,
		Backup:      source.options,
		Retention:   RetentionPolicy{MaxBackups: 1},
		DiskUsage: func(string) (DiskUsage, error) {
			return DiskUsage{FreeBytes: 1 << 40, TotalBytes: 2 << 40}, nil
		},
		Create: func(ctx context.Context, path string, options Options) (Result, error) {
			result, err := Create(ctx, path, options)
			createCalls++
			if createCalls == 2 {
				if writeErr := os.WriteFile(filepath.Join(source.options.ConfigDir, "server.yml"), []byte("changed after snapshot\n"), 0600); writeErr != nil {
					return Result{}, writeErr
				}
			}
			return result, err
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.RunDaily(context.Background())
	if err != nil {
		t.Fatalf("RunDaily(first) error = %v", err)
	}
	if first.Incremental != nil || first.Full == nil {
		t.Fatalf("first daily run = %+v, want full only", first)
	}

	now = now.Add(24 * time.Hour)
	if err := os.WriteFile(filepath.Join(source.options.ConfigDir, "server.yml"), []byte("second config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := scheduler.RunDaily(context.Background())
	if err != nil {
		t.Fatalf("RunDaily(second) error = %v", err)
	}
	if second.Incremental == nil || second.Full == nil {
		t.Fatalf("second daily run = %+v, want incremental followed by full", second)
	}
	if second.Incremental.Manifest.BaseBackup != filepath.Base(first.Full.Path) {
		t.Fatalf("daily incremental base = %q, want prior full %q", second.Incremental.Manifest.BaseBackup, filepath.Base(first.Full.Path))
	}
	if second.Incremental.Manifest.SnapshotAt != second.Full.Manifest.SnapshotAt {
		t.Fatalf("daily snapshot times differ: incremental %q, full %q", second.Incremental.Manifest.SnapshotAt, second.Full.Manifest.SnapshotAt)
	}
	var incrementalConfigHash, fullConfigHash string
	for _, record := range second.Incremental.Manifest.Files {
		if record.Path == "config/server.yml" {
			incrementalConfigHash = record.SHA256
		}
	}
	for _, record := range second.Full.Manifest.Files {
		if record.Path == "config/server.yml" {
			fullConfigHash = record.SHA256
		}
	}
	if incrementalConfigHash == "" || incrementalConfigHash != fullConfigHash {
		t.Fatalf("incremental/full config hashes = %q/%q, want identical captured state", incrementalConfigHash, fullConfigHash)
	}

	hourly, err := scheduler.RunHourly(context.Background())
	if err != nil {
		t.Fatalf("RunHourly() error = %v", err)
	}
	if hourly.Incremental == nil || hourly.Incremental.Manifest.BaseBackup != filepath.Base(second.Full.Path) {
		t.Fatalf("hourly run = %+v, want newest full as base", hourly)
	}
}

func TestSchedulerSkipsWhenDiskUsageExceedsThreshold(t *testing.T) {
	root := createBackupTestRoot(t)
	source := makeBackupFixture(t, root, "config\n")
	createCalls := 0
	scheduler, err := NewScheduler(ScheduledOptions{
		ProjectName: "countries",
		BackupDir:   filepath.Join(root, "backups"),
		Backup:      source.options,
		DiskUsage: func(string) (DiskUsage, error) {
			return DiskUsage{FreeBytes: 9, TotalBytes: 100}, nil
		},
		Create: func(context.Context, string, Options) (Result, error) {
			createCalls++
			return Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.RunDaily(context.Background())
	if err != nil {
		t.Fatalf("RunDaily() error = %v", err)
	}
	if !result.Skipped || createCalls != 0 {
		t.Fatalf("RunDaily() = %+v, create calls = %d, want disk-pressure skip", result, createCalls)
	}
}

func TestSchedulerSkipsWhenFreeSpaceIsBelowTwiceLatestBackup(t *testing.T) {
	root := createBackupTestRoot(t)
	source := makeBackupFixture(t, root, "config\n")
	backupDir := filepath.Join(root, "backups")
	latestPath := filepath.Join(backupDir, "countries_backup_2030-01-01.tar.gz")
	latest, err := Create(context.Background(), latestPath, source.options)
	if err != nil {
		t.Fatalf("Create(latest full backup) error = %v", err)
	}
	createCalls := 0
	scheduler, err := NewScheduler(ScheduledOptions{
		ProjectName: "countries",
		BackupDir:   backupDir,
		Backup:      source.options,
		DiskUsage: func(string) (DiskUsage, error) {
			return DiskUsage{FreeBytes: uint64(latest.Size*2 - 1), TotalBytes: uint64(latest.Size * 10)}, nil
		},
		Create: func(context.Context, string, Options) (Result, error) {
			createCalls++
			return Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.RunDaily(context.Background())
	if err != nil {
		t.Fatalf("RunDaily() error = %v", err)
	}
	if !result.Skipped || !strings.Contains(result.SkipReason, "twice the latest backup") || createCalls != 0 {
		t.Fatalf("RunDaily() = %+v, create calls = %d, want free-space skip", result, createCalls)
	}
}

func TestRetentionDatePriorityAndSizeCap(t *testing.T) {
	root := createBackupTestRoot(t)
	backupDir := filepath.Join(root, "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		t.Fatal(err)
	}
	scheduler, err := NewScheduler(ScheduledOptions{
		ProjectName: "countries",
		BackupDir:   backupDir,
		Retention: RetentionPolicy{
			MaxBackups: 1, KeepWeekly: 1, KeepMonthly: 1, KeepYearly: 1,
		},
		DiskUsage: func(string) (DiskUsage, error) {
			return DiskUsage{FreeBytes: 1 << 30, TotalBytes: 1 << 30}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2024-01-01", "2024-01-07", "2024-01-15", "2024-01-16"} {
		path := filepath.Join(backupDir, "countries_backup_"+date+".tar.gz")
		if err := os.WriteFile(path, []byte(date), 0600); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := scheduler.ApplyRetention(context.Background())
	if err != nil {
		t.Fatalf("ApplyRetention() error = %v", err)
	}
	if len(deleted) != 1 || filepath.Base(deleted[0]) != "countries_backup_2024-01-15.tar.gz" {
		t.Fatalf("retention deleted = %v, want only unmarked January 15 backup", deleted)
	}

	for index, date := range []string{"2025-02-01", "2025-02-02", "2025-02-03"} {
		path := filepath.Join(backupDir, "countries_backup_"+date+".tar.gz")
		if err := os.WriteFile(path, bytes.Repeat([]byte{byte('a' + index)}, 8), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scheduler.options.Retention.MaxBackups = 10
	scheduler.options.Retention.KeepWeekly = 0
	scheduler.options.Retention.KeepMonthly = 0
	scheduler.options.Retention.KeepYearly = 0
	scheduler.options.Retention.MaxTotalSize = "16B"
	deleted, err = scheduler.ApplyRetention(context.Background())
	if err != nil {
		t.Fatalf("ApplyRetention(size cap) error = %v", err)
	}
	if !containsPath(deleted, filepath.Join(backupDir, "countries_backup_2025-02-01.tar.gz")) {
		t.Fatalf("size cap deleted = %v, want oldest February backup removed", deleted)
	}
}

type tarFixture struct {
	name     string
	content  []byte
	typeFlag byte
	linkName string
}

func makeTarArchive(t *testing.T, fixtures []tarFixture) ([]byte, error) {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gz)
	for _, fixture := range fixtures {
		header := &tar.Header{Name: fixture.name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(fixture.content))}
		if fixture.typeFlag != 0 {
			header.Typeflag = fixture.typeFlag
			header.Linkname = fixture.linkName
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if fixture.typeFlag == 0 {
			if _, err := tarWriter.Write(fixture.content); err != nil {
				return nil, err
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return raw.Bytes(), nil
}

type backupFixture struct {
	options Options
}

func makeBackupFixture(t *testing.T, root, serverConfig string) backupFixture {
	t.Helper()
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	databasePath := filepath.Join(dataDir, "db", "server.db")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte(serverConfig), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE fixture (value TEXT NOT NULL)"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO fixture(value) VALUES ('ok')"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE app_secrets(name TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES ('encryption_key_previous','ciphertext','2029-12-02T00:00:00Z'),('encryption_key_previous_until','2030-01-01T00:00:00Z','2029-12-02T00:00:00Z'),('installation_secret_previous_until','2030-01-01T00:00:00Z','2029-12-02T00:00:00Z')`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return backupFixture{options: Options{
		ConfigDir: configDir, DataDir: dataDir, DatabasePath: databasePath,
		AppVersion: "1.2.3", CreatedBy: "test operator",
	}}
}

func createBackupTestRoot(t *testing.T) string {
	t.Helper()
	root, err := paths.CreateTempDir("countries")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove test workspace: %v", err)
		}
	})
	return root
}

func TestPlatformDiskUsage(t *testing.T) {
	usage, err := platformDiskUsage(t.TempDir())
	if err != nil {
		t.Fatalf("platformDiskUsage(temp dir) error = %v", err)
	}
	if usage.TotalBytes == 0 || usage.FreeBytes > usage.TotalBytes {
		t.Fatalf("platformDiskUsage(temp dir) = %+v", usage)
	}
	if _, err := platformDiskUsage(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("platformDiskUsage(missing path) returned nil error")
	}
}

func TestCollectFilesEnforcesLimitsDuringCollection(t *testing.T) {
	root := createBackupTestRoot(t)
	filesDir := filepath.Join(root, "files")
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"one.txt": "abc", "two.txt": "def"} {
		if err := os.WriteFile(filepath.Join(filesDir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	roots := []inputRoot{{source: filesDir, archive: "data"}}
	if _, err := collectFiles(context.Background(), roots, Limits{MaxFiles: 10, MaxFileSize: 16, MaxExpandedSize: 4}); err == nil || !strings.Contains(err.Error(), "maximum expanded size") {
		t.Fatalf("collectFiles() aggregate size error = %v", err)
	}
	if _, err := collectFiles(context.Background(), roots, Limits{MaxFiles: 1, MaxFileSize: 16, MaxExpandedSize: 16}); err == nil || !strings.Contains(err.Error(), "maximum file count") {
		t.Fatalf("collectFiles() file count error = %v", err)
	}
}
