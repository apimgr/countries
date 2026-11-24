// Package backup creates, verifies, and safely restores application archives.
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/countries/src/paths"
	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite"
)

const (
	manifestName     = "manifest.json"
	encryptedMagic   = "COUNTRIES-BACKUP-V1\x00"
	defaultFileCap   = int64(512 << 20)
	defaultArchive   = int64(1 << 30)
	defaultExpanded  = int64(4 << 30)
	defaultFileCount = 10000
)

// Manifest describes the archive payload and the files represented in it.
type Manifest struct {
	Version          string       `json:"version"`
	CreatedAt        string       `json:"created_at"`
	CreatedBy        string       `json:"created_by"`
	AppVersion       string       `json:"app_version"`
	Contents         []string     `json:"contents"`
	Encrypted        bool         `json:"encrypted"`
	EncryptionMethod string       `json:"encryption_method,omitempty"`
	Checksum         string       `json:"checksum"`
	SHA256           string       `json:"sha256"`
	Type             string       `json:"type"`
	SnapshotAt       string       `json:"snapshot_at"`
	BaseBackup       string       `json:"base_backup,omitempty"`
	BaseSHA256       string       `json:"base_sha256,omitempty"`
	Deleted          []string     `json:"deleted,omitempty"`
	Files            []FileRecord `json:"files"`
}

// FileRecord carries the full archive's content index used by verification and incrementals.
type FileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	MTime  int64  `json:"mtime"`
}

// Limits bound untrusted restore input. Zero fields use the PART 21 defaults.
type Limits struct {
	MaxArchiveSize  int64
	MaxExpandedSize int64
	MaxFiles        int
	MaxFileSize     int64
	MaxRatio        int64
}

// Options specifies the source roots and backup policy.
type Options struct {
	ConfigDir          string
	DataDir            string
	DatabasePath       string
	SourceDatabasePath string
	BackupDir          string
	AppVersion         string
	CreatedBy          string
	Password           string
	Compliance         bool
	IncludeSSL         bool
	IncludeData        bool
	TLSFiles           []string
	Type               string
	SnapshotAt         time.Time
	BaseBackup         string
	BaseArchivePath    string
	BaseSHA256         string
	Deleted            []string
	Limits             Limits
	Snapshot           *Snapshot
}

// Snapshot holds one captured source state for creating multiple archives
// with identical contents, such as a daily incremental and its full backup.
type Snapshot struct {
	files map[string]fileData
}

// Result reports the verified archive and its manifest.
type Result struct {
	Path     string
	Size     int64
	SHA256   string
	Manifest Manifest
}

// RestoreOptions identifies the installation roots and the active database path.
type RestoreOptions struct {
	ConfigDir           string
	DataDir             string
	DatabasePath        string
	TLSFileDestinations map[string]string
	Limits              Limits
}

// Create writes a single verified archive. The compressed tar stream is held
// in memory so plaintext never reaches disk when encryption is enabled.
func Create(ctx context.Context, destination string, options Options) (Result, error) {
	if options.Compliance && strings.TrimSpace(options.Password) == "" {
		return Result{}, errors.New("compliance mode requires an encrypted backup password")
	}
	if options.Type == "" {
		options.Type = "full"
	}
	if options.Type != "full" && options.Type != "incremental" {
		return Result{}, fmt.Errorf("unsupported backup type %q", options.Type)
	}
	if options.Type == "incremental" && (options.BaseBackup == "" || options.BaseSHA256 == "") {
		return Result{}, errors.New("incremental backup requires its base filename and checksum")
	}
	if options.Type == "incremental" && (filepath.Base(options.BaseBackup) != options.BaseBackup || options.BaseArchivePath == "" || filepath.Base(options.BaseArchivePath) != options.BaseBackup) {
		return Result{}, errors.New("incremental base filename must match its archive path")
	}
	if options.SnapshotAt.IsZero() {
		options.SnapshotAt = time.Now().UTC()
	} else {
		options.SnapshotAt = options.SnapshotAt.UTC()
	}
	if options.CreatedBy == "" {
		options.CreatedBy = "operator"
	}
	var files map[string]fileData
	var err error
	if options.Snapshot != nil {
		files = cloneFiles(options.Snapshot.files)
	} else {
		files, err = captureSnapshotFiles(ctx, options)
		if err != nil {
			return Result{}, err
		}
	}
	if len(files) > normalizedLimits(options.Limits).MaxFiles {
		return Result{}, errors.New("backup exceeds maximum file count")
	}
	var expandedSize int64
	for _, file := range files {
		if int64(len(file.content)) > normalizedLimits(options.Limits).MaxExpandedSize-expandedSize {
			return Result{}, errors.New("backup exceeds maximum expanded size")
		}
		expandedSize += int64(len(file.content))
	}
	if options.Type == "full" {
		if _, ok := files["data/server.db"]; !ok {
			return Result{}, errors.New("main database is missing from backup input")
		}
	}
	if options.Type == "incremental" {
		basePath := options.BaseArchivePath
		if basePath == "" {
			return Result{}, errors.New("incremental backup requires the base archive path")
		}
		base, err := Verify(ctx, basePath, options.Password, options.Limits)
		if err != nil {
			return Result{}, fmt.Errorf("verify incremental base: %w", err)
		}
		if base.Manifest.Type != "full" || filepath.Base(options.BaseBackup) != options.BaseBackup || filepath.Base(basePath) != options.BaseBackup || base.SHA256 != options.BaseSHA256 {
			return Result{}, errors.New("incremental base must name a valid full backup and match its checksum")
		}
		baseFiles := make(map[string]FileRecord, len(base.Manifest.Files))
		for _, file := range base.Manifest.Files {
			baseFiles[file.Path] = file
		}
		deleted := make([]string, 0)
		present := make(map[string]struct{}, len(files))
		for name := range files {
			present[name] = struct{}{}
		}
		for name, current := range files {
			digest := sha256.Sum256(current.content)
			old, exists := baseFiles[name]
			if !exists || old.Size != int64(len(current.content)) || old.MTime != current.mtime || old.SHA256 != hex.EncodeToString(digest[:]) {
				continue
			}
			delete(files, name)
		}
		for name := range baseFiles {
			if _, exists := present[name]; !exists {
				deleted = append(deleted, name)
			}
		}
		sort.Strings(deleted)
		options.Deleted = deleted
	}
	manifest := buildManifest(files, options)
	payload, err := marshalArchive(files, manifest)
	if err != nil {
		return Result{}, err
	}
	if options.Password != "" {
		payload, err = encrypt(payload, options.Password)
		if err != nil {
			return Result{}, err
		}
	}
	limits := normalizedLimits(options.Limits)
	if int64(len(payload)) > limits.MaxArchiveSize {
		return Result{}, fmt.Errorf("backup archive exceeds maximum size of %d bytes", limits.MaxArchiveSize)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return Result{}, fmt.Errorf("create backup directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".backup-verify-*.tmp")
	if err != nil {
		return Result{}, fmt.Errorf("create backup candidate: %w", err)
	}
	candidatePath := temporary.Name()
	defer func() { _ = os.Remove(candidatePath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return Result{}, err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return Result{}, fmt.Errorf("write backup candidate: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return Result{}, err
	}
	if err := temporary.Close(); err != nil {
		return Result{}, err
	}
	verified, err := Verify(ctx, candidatePath, options.Password, options.Limits)
	if err != nil {
		return Result{}, fmt.Errorf("verify newly created backup: %w", err)
	}
	if err := os.Rename(candidatePath, destination); err != nil {
		return Result{}, fmt.Errorf("install verified backup: %w", err)
	}
	return Result{Path: destination, Size: int64(len(payload)), SHA256: verified.SHA256, Manifest: verified.Manifest}, nil
}

// CaptureSnapshot takes a SQLite online snapshot and copies the configured
// backup sources once so multiple archives can share the same point-in-time set.
func CaptureSnapshot(ctx context.Context, options Options) (*Snapshot, error) {
	files, err := captureSnapshotFiles(ctx, options)
	if err != nil {
		return nil, err
	}
	return &Snapshot{files: files}, nil
}

func captureSnapshotFiles(ctx context.Context, options Options) (map[string]fileData, error) {
	if options.DatabasePath == "" {
		options.DatabasePath = filepath.Join(options.DataDir, "db", "server.db")
	}
	if !filepath.IsAbs(options.DatabasePath) {
		options.DatabasePath = filepath.Join(options.DataDir, options.DatabasePath)
	}
	originalDatabasePath := options.DatabasePath
	if originalDatabasePath == "" {
		originalDatabasePath = filepath.Join(options.DataDir, "db", "server.db")
	}
	options.SourceDatabasePath = originalDatabasePath
	snapshotDir, err := paths.CreateTempDir("countries")
	if err != nil {
		return nil, fmt.Errorf("create database snapshot workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(snapshotDir) }()
	if err := os.Chmod(snapshotDir, 0700); err != nil {
		return nil, fmt.Errorf("secure database snapshot workspace: %w", err)
	}
	databaseSnapshot := filepath.Join(snapshotDir, "server.db")
	if err := snapshotSQLite(ctx, originalDatabasePath, databaseSnapshot); err != nil {
		return nil, fmt.Errorf("snapshot main database: %w", err)
	}
	options.DatabasePath = databaseSnapshot
	inputs, err := backupInputs(options)
	if err != nil {
		return nil, err
	}
	return collectFiles(ctx, inputs, options.Limits)
}

func cloneFiles(files map[string]fileData) map[string]fileData {
	clone := make(map[string]fileData, len(files))
	for name, file := range files {
		clone[name] = file
	}
	return clone
}

// Verify authenticates, decompresses, checks the manifest and checksums, and
// validates any included SQLite database without extracting files to disk.
func Verify(ctx context.Context, path, password string, limits Limits) (Result, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("open backup: %w", err)
	}
	limits = normalizedLimits(limits)
	if info.Size() <= 0 || info.Size() > limits.MaxArchiveSize {
		return Result{}, fmt.Errorf("backup archive has invalid size %d", info.Size())
	}
	file, err := os.Open(path)
	if err != nil {
		return Result{}, fmt.Errorf("open backup: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, limits.MaxArchiveSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return Result{}, fmt.Errorf("read backup: %w", readErr)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close backup: %w", closeErr)
	}
	if int64(len(payload)) > limits.MaxArchiveSize {
		return Result{}, errors.New("backup archive exceeds configured size limit")
	}
	archiveBytes, encrypted, err := maybeDecrypt(payload, password)
	if err != nil {
		return Result{}, err
	}
	entries, err := readArchive(ctx, archiveBytes, int64(len(payload)), limits)
	if err != nil {
		return Result{}, err
	}
	manifestBytes, ok := entries[manifestName]
	if !ok {
		return Result{}, errors.New("backup manifest is missing")
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return Result{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.Version == "" || manifest.CreatedAt == "" || manifest.AppVersion == "" || manifest.SnapshotAt == "" || (manifest.Type != "full" && manifest.Type != "incremental") {
		return Result{}, errors.New("backup manifest is incomplete or invalid")
	}
	if manifest.Encrypted != encrypted {
		return Result{}, errors.New("backup encryption metadata does not match file format")
	}
	if encrypted && manifest.EncryptionMethod != "AES-256-GCM" {
		return Result{}, errors.New("backup manifest has an unsupported encryption method")
	}
	if manifest.Checksum != manifestChecksum(manifest.Files) {
		return Result{}, errors.New("backup manifest checksum mismatch")
	}
	if manifest.SHA256 != fileIndexSHA256(manifest.Files) {
		return Result{}, errors.New("backup manifest SHA-256 mismatch")
	}
	if err := verifyFileIndex(entries, manifest); err != nil {
		return Result{}, err
	}
	if err := verifyContentsList(manifest); err != nil {
		return Result{}, err
	}
	if dbBytes, ok := entries["data/server.db"]; ok {
		if err := verifySQLite(ctx, dbBytes); err != nil {
			return Result{}, err
		}
	}
	digest := sha256.Sum256(payload)
	archiveHash := hex.EncodeToString(digest[:])
	if manifest.Type == "incremental" {
		if !safeArchivePath(manifest.BaseBackup) || filepath.Base(manifest.BaseBackup) != manifest.BaseBackup || len(manifest.BaseSHA256) != sha256.Size*2 {
			return Result{}, errors.New("incremental manifest has an invalid base reference")
		}
		base, err := Verify(ctx, filepath.Join(filepath.Dir(path), manifest.BaseBackup), password, limits)
		if err != nil {
			return Result{}, fmt.Errorf("ERR_BASE_MISSING: verify incremental base: %w", err)
		}
		if base.Manifest.Type != "full" || !strings.EqualFold(base.SHA256, manifest.BaseSHA256) {
			return Result{}, errors.New("ERR_BASE_CORRUPT: incremental base checksum or type is invalid")
		}
		for _, deleted := range manifest.Deleted {
			if !safeArchivePath(deleted) || deleted == manifestName {
				return Result{}, fmt.Errorf("incremental manifest contains unsafe deleted path %q", deleted)
			}
		}
	}
	return Result{Path: path, Size: info.Size(), SHA256: archiveHash, Manifest: manifest}, nil
}

// Restore verifies the complete archive before writing anything, stages all
// extracted content under a private project-scoped temporary directory, then
// atomically replaces only paths rooted under ConfigDir or DataDir.
func Restore(ctx context.Context, path, password string, options RestoreOptions) (Manifest, error) {
	verified, err := Verify(ctx, path, password, options.Limits)
	if err != nil {
		return Manifest{}, err
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup: %w", err)
	}
	archiveBytes, _, err := maybeDecrypt(payload, password)
	if err != nil {
		return Manifest{}, err
	}
	if int64(len(payload)) != verified.Size || hashHex(payload) != verified.SHA256 {
		return Manifest{}, errors.New("backup changed after verification")
	}
	entries, err := readArchive(ctx, archiveBytes, int64(len(payload)), normalizedLimits(options.Limits))
	if err != nil {
		return Manifest{}, err
	}
	if verified.Manifest.Type == "incremental" {
		basePath := filepath.Join(filepath.Dir(path), verified.Manifest.BaseBackup)
		basePayload, err := os.ReadFile(basePath)
		if err != nil {
			return Manifest{}, fmt.Errorf("ERR_BASE_MISSING: read incremental base: %w", err)
		}
		baseVerified, err := Verify(ctx, basePath, password, options.Limits)
		if err != nil {
			return Manifest{}, fmt.Errorf("ERR_BASE_CORRUPT: verify incremental base: %w", err)
		}
		if hashHex(basePayload) != baseVerified.SHA256 {
			return Manifest{}, errors.New("ERR_BASE_CORRUPT: incremental base changed after verification")
		}
		baseArchive, _, err := maybeDecrypt(basePayload, password)
		if err != nil {
			return Manifest{}, err
		}
		baseEntries, err := readArchive(ctx, baseArchive, int64(len(basePayload)), normalizedLimits(options.Limits))
		if err != nil {
			return Manifest{}, err
		}
		for name, content := range entries {
			if name != manifestName {
				baseEntries[name] = content
			}
		}
		for _, name := range verified.Manifest.Deleted {
			if !safeArchivePath(name) || name == manifestName {
				return Manifest{}, fmt.Errorf("incremental manifest contains unsafe deleted path %q", name)
			}
			delete(baseEntries, name)
		}
		entries = baseEntries
	}
	limits := normalizedLimits(options.Limits)
	if len(entries)-1 > limits.MaxFiles {
		return Manifest{}, errors.New("combined backup exceeds maximum file count")
	}
	var expanded int64
	for name, content := range entries {
		if name == manifestName {
			continue
		}
		expanded += int64(len(content))
		if int64(len(content)) > limits.MaxFileSize || expanded > limits.MaxExpandedSize {
			return Manifest{}, errors.New("combined backup exceeds restore size limits")
		}
	}
	tempDir, err := paths.CreateTempDir("countries")
	if err != nil {
		return Manifest{}, err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	if err := os.Chmod(tempDir, 0700); err != nil {
		return Manifest{}, fmt.Errorf("secure restore workspace: %w", err)
	}
	staged := filepath.Join(tempDir, "staged")
	for name, content := range entries {
		if name == manifestName {
			continue
		}
		if err := contextErr(ctx); err != nil {
			return Manifest{}, err
		}
		if !safeArchivePath(name) || !supportedArchiveRoot(name) {
			return Manifest{}, fmt.Errorf("unsafe path in backup: %q", name)
		}
		if strings.HasPrefix(name, "connection-tls/") && options.TLSFileDestinations[name] == "" {
			return Manifest{}, fmt.Errorf("restore requires a destination for connection TLS file %q", name)
		}
		target := filepath.Join(staged, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return Manifest{}, fmt.Errorf("stage restore file %s: %w", name, err)
		}
		if err := os.WriteFile(target, content, 0600); err != nil {
			return Manifest{}, fmt.Errorf("stage restore file %s: %w", name, err)
		}
	}
	if err := installStaged(ctx, staged, options.ConfigDir, options.DataDir, options.DatabasePath, options.TLSFileDestinations, verified.Manifest.Deleted); err != nil {
		return Manifest{}, err
	}
	return verified.Manifest, nil
}

func backupInputs(options Options) ([]inputRoot, error) {
	if strings.TrimSpace(options.ConfigDir) == "" || strings.TrimSpace(options.DataDir) == "" {
		return nil, errors.New("backup requires config and data directories")
	}
	configRoot, err := filepath.Abs(options.ConfigDir)
	if err != nil {
		return nil, err
	}
	dataRoot, err := filepath.Abs(options.DataDir)
	if err != nil {
		return nil, err
	}
	inputs := []inputRoot{{source: filepath.Join(configRoot, "server.yml"), archive: "config/server.yml"}}
	for _, name := range []string{"yaml.d", "secrets.yml", "assets", "template", "security"} {
		inputs = append(inputs, inputRoot{source: filepath.Join(configRoot, name), archive: "config/" + name, optional: true})
	}
	if options.IncludeSSL {
		inputs = append(inputs, inputRoot{source: filepath.Join(configRoot, "ssl"), archive: "config/ssl", optional: true})
	}
	if options.IncludeData {
		inputs = append(inputs, inputRoot{source: dataRoot, archive: "data", optional: true})
	}
	databasePath := options.DatabasePath
	if !filepath.IsAbs(databasePath) {
		databasePath = filepath.Join(dataRoot, databasePath)
	}
	databasePath, err = filepath.Abs(databasePath)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, inputRoot{source: databasePath, archive: "data/server.db"})
	if options.SourceDatabasePath != "" {
		excludedDatabase, err := filepath.Abs(options.SourceDatabasePath)
		if err != nil {
			return nil, err
		}
		for index := range inputs {
			if inputs[index].archive == "data" {
				inputs[index].exclude = excludedDatabase
			}
		}
	}
	for _, file := range options.TLSFiles {
		if strings.TrimSpace(file) != "" {
			abs, err := filepath.Abs(file)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, inputRoot{source: abs, archive: "connection-tls/" + filepath.Base(abs), optional: true})
		}
	}
	return inputs, nil
}

type inputRoot struct {
	source   string
	archive  string
	optional bool
	exclude  string
}

type fileData struct {
	content []byte
	mtime   int64
}

func collectFiles(ctx context.Context, roots []inputRoot, limits Limits) (map[string]fileData, error) {
	limits = normalizedLimits(limits)
	files := make(map[string]fileData)
	var expandedSize int64
	for _, root := range roots {
		info, err := os.Lstat(root.source)
		if errors.Is(err, os.ErrNotExist) && root.optional {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect backup source %s: %w", root.source, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("backup source may not be a symlink: %s", root.source)
		}
		if info.IsDir() {
			err = filepath.WalkDir(root.source, func(current string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if err := contextErr(ctx); err != nil {
					return err
				}
				currentInfo, err := os.Lstat(current)
				if err != nil {
					return err
				}
				if currentInfo.Mode()&os.ModeSymlink != 0 || !currentInfo.IsDir() && !currentInfo.Mode().IsRegular() {
					return fmt.Errorf("unsupported backup source type: %s", current)
				}
				if currentInfo.IsDir() {
					return nil
				}
				if root.exclude != "" {
					currentAbs, err := filepath.Abs(current)
					if err != nil {
						return err
					}
					if currentAbs == root.exclude {
						return nil
					}
				}
				relative, err := filepath.Rel(root.source, current)
				if err != nil {
					return err
				}
				name := pathJoin(root.archive, filepath.ToSlash(relative))
				return addFile(files, name, current, limits, &expandedSize)
			})
		} else if info.Mode().IsRegular() {
			err = addFile(files, root.archive, root.source, limits, &expandedSize)
		} else {
			return nil, fmt.Errorf("unsupported backup source type: %s", root.source)
		}
		if err != nil {
			return nil, fmt.Errorf("collect backup source %s: %w", root.source, err)
		}
	}
	return files, nil
}

func addFile(files map[string]fileData, name, source string, limits Limits, expandedSize *int64) error {
	if !safeArchivePath(name) {
		return fmt.Errorf("unsafe backup path %q", name)
	}
	if _, exists := files[name]; exists {
		return fmt.Errorf("duplicate backup path %q", name)
	}
	if len(files) >= limits.MaxFiles {
		return errors.New("backup exceeds maximum file count")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > limits.MaxFileSize {
		return fmt.Errorf("invalid backup file %s (size %d)", source, info.Size())
	}
	remaining := limits.MaxExpandedSize - *expandedSize
	if info.Size() > remaining {
		return errors.New("backup exceeds maximum expanded size")
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Size() < 0 || openedInfo.Size() > limits.MaxFileSize || openedInfo.Size() > remaining {
		_ = file.Close()
		return fmt.Errorf("invalid backup file %s (size %d)", source, openedInfo.Size())
	}
	readLimit := limits.MaxFileSize
	if remaining < readLimit {
		readLimit = remaining
	}
	reader := io.Reader(file)
	if readLimit < math.MaxInt64 {
		reader = io.LimitReader(file, readLimit+1)
	}
	content, err := io.ReadAll(reader)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if int64(len(content)) > limits.MaxFileSize {
		return fmt.Errorf("backup file %s exceeds configured size limit", source)
	}
	if int64(len(content)) > remaining {
		return errors.New("backup exceeds maximum expanded size")
	}
	*expandedSize += int64(len(content))
	files[name] = fileData{content: content, mtime: openedInfo.ModTime().UnixNano()}
	return nil
}

func buildManifest(files map[string]fileData, options Options) Manifest {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	records := make([]FileRecord, 0, len(names))
	for _, name := range names {
		file := files[name]
		digest := sha256.Sum256(file.content)
		records = append(records, FileRecord{Path: name, Size: int64(len(file.content)), SHA256: hex.EncodeToString(digest[:]), MTime: file.mtime})
	}
	contents := append([]string(nil), names...)
	return Manifest{
		Version: "1.0.0", CreatedAt: options.SnapshotAt.Format(time.RFC3339), CreatedBy: options.CreatedBy,
		AppVersion: options.AppVersion, Contents: contents, Encrypted: options.Password != "",
		EncryptionMethod: encryptionMethod(options.Password), Checksum: manifestChecksum(records), SHA256: fileIndexSHA256(records), Type: options.Type,
		SnapshotAt: options.SnapshotAt.Format(time.RFC3339), BaseBackup: options.BaseBackup,
		BaseSHA256: options.BaseSHA256, Deleted: append([]string(nil), options.Deleted...), Files: records,
	}
}

func manifestChecksum(records []FileRecord) string {
	copyRecords := append([]FileRecord(nil), records...)
	sort.Slice(copyRecords, func(i, j int) bool { return copyRecords[i].Path < copyRecords[j].Path })
	hash := sha256.New()
	for _, record := range copyRecords {
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%s\n", record.Path, record.Size, record.SHA256)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func hashHex(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func fileIndexSHA256(records []FileRecord) string {
	return strings.TrimPrefix(manifestChecksum(records), "sha256:")
}

func verifyContentsList(manifest Manifest) error {
	if len(manifest.Contents) != len(manifest.Files) {
		return errors.New("backup contents list does not match manifest file index")
	}
	contents := append([]string(nil), manifest.Contents...)
	sort.Strings(contents)
	for index, record := range manifest.Files {
		if contents[index] != record.Path {
			return errors.New("backup contents list does not match manifest file index")
		}
	}
	return nil
}

func marshalArchive(files map[string]fileData, manifest Manifest) ([]byte, error) {
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode backup manifest: %w", err)
	}
	files[manifestName] = fileData{content: manifestBytes}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	gzipWriter.ModTime = time.Unix(0, 0)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, name := range names {
		content := files[name].content
		header := &tar.Header{Name: name, Mode: 0600, Size: int64(len(content)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := tarWriter.Write(content); err != nil {
			return nil, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, err
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return raw.Bytes(), nil
}

func encrypt(plaintext []byte, password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate backup salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate backup nonce: %w", err)
	}
	output := make([]byte, 0, len(encryptedMagic)+len(salt)+len(nonce)+len(plaintext)+aead.Overhead())
	output = append(output, encryptedMagic...)
	output = append(output, salt...)
	output = append(output, nonce...)
	output = aead.Seal(output, nonce, plaintext, []byte(encryptedMagic))
	return output, nil
}

func maybeDecrypt(payload []byte, password string) ([]byte, bool, error) {
	if !bytes.HasPrefix(payload, []byte(encryptedMagic)) {
		return payload, false, nil
	}
	if password == "" {
		return nil, true, errors.New("encrypted backup requires a password")
	}
	minimum := len(encryptedMagic) + 16 + 12 + 16
	if len(payload) < minimum {
		return nil, true, errors.New("encrypted backup is truncated")
	}
	position := len(encryptedMagic)
	salt := payload[position : position+16]
	position += 16
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, true, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, true, err
	}
	nonce := payload[position : position+aead.NonceSize()]
	position += aead.NonceSize()
	plaintext, err := aead.Open(nil, nonce, payload[position:], []byte(encryptedMagic))
	if err != nil {
		return nil, true, errors.New("invalid backup password or corrupted encrypted archive")
	}
	return plaintext, true, nil
}

func readArchive(ctx context.Context, archiveBytes []byte, compressedSize int64, limits Limits) (map[string][]byte, error) {
	limits = normalizedLimits(limits)
	gzipReader, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return nil, fmt.Errorf("invalid backup archive format: %w", err)
	}
	defer func() { _ = gzipReader.Close() }()
	tarReader := tar.NewReader(gzipReader)
	entries := make(map[string][]byte)
	var expanded int64
	for {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read backup tar: %w", err)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safeArchivePath(name) || header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("backup contains an unsupported or unsafe entry %q", header.Name)
		}
		if _, exists := entries[name]; exists {
			return nil, fmt.Errorf("backup contains duplicate path %q", name)
		}
		if len(entries) >= limits.MaxFiles+1 {
			return nil, errors.New("backup exceeds maximum file count")
		}
		if header.Size < 0 || header.Size > limits.MaxFileSize {
			return nil, fmt.Errorf("backup file %s exceeds maximum file size", name)
		}
		if header.Size > limits.MaxExpandedSize-expanded {
			return nil, errors.New("backup exceeds maximum expanded size")
		}
		expanded += header.Size
		content, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil {
			return nil, fmt.Errorf("read backup file %s: %w", name, err)
		}
		if int64(len(content)) != header.Size {
			return nil, fmt.Errorf("backup file %s is truncated", name)
		}
		entries[name] = content
	}
	if compressedSize > 0 && (expanded/compressedSize > limits.MaxRatio || expanded/compressedSize == limits.MaxRatio && expanded%compressedSize != 0) {
		return nil, errors.New("backup exceeds maximum compression ratio")
	}
	return entries, nil
}

func verifyFileIndex(entries map[string][]byte, manifest Manifest) error {
	indexed := make(map[string]FileRecord, len(manifest.Files))
	for _, record := range manifest.Files {
		if !safeArchivePath(record.Path) || record.Path == manifestName {
			return fmt.Errorf("manifest contains unsafe file path %q", record.Path)
		}
		if _, exists := indexed[record.Path]; exists {
			return fmt.Errorf("manifest contains duplicate file path %q", record.Path)
		}
		indexed[record.Path] = record
	}
	if len(entries)-1 != len(indexed) {
		return errors.New("backup content does not match manifest file list")
	}
	for name, content := range entries {
		if name == manifestName {
			continue
		}
		record, ok := indexed[name]
		if !ok || record.Size != int64(len(content)) {
			return fmt.Errorf("backup file %s does not match manifest", name)
		}
		digest := sha256.Sum256(content)
		if !strings.EqualFold(record.SHA256, hex.EncodeToString(digest[:])) {
			return fmt.Errorf("backup checksum mismatch for %s", name)
		}
	}
	return nil
}

func verifySQLite(ctx context.Context, content []byte) error {
	tempDir, err := paths.CreateTempDir("countries")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	if err := os.Chmod(tempDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(tempDir, "server.db")
	if err := os.WriteFile(path, content, 0600); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open SQLite backup: %w", err)
	}
	defer func() { _ = db.Close() }()
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result string
	if err := db.QueryRowContext(queryCtx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("check SQLite backup integrity: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite backup integrity check failed: %s", result)
	}
	return nil
}

func snapshotSQLite(ctx context.Context, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect SQLite source %s: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("SQLite source is not a regular file: %s", source)
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return fmt.Errorf("open SQLite source: %w", err)
	}
	defer func() { _ = db.Close() }()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := db.ExecContext(queryCtx, "PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("set SQLite snapshot busy timeout: %w", err)
	}
	quoted := strings.ReplaceAll(destination, "'", "''")
	if _, err := db.ExecContext(queryCtx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("create SQLite online snapshot: %w", err)
	}
	return os.Chmod(destination, 0600)
}

type restoreOriginal struct {
	content []byte
	mode    os.FileMode
	exists  bool
}

func installStaged(ctx context.Context, staged, configDir, dataDir, databasePath string, tlsDestinations map[string]string, deleted []string) error {
	targets := make(map[string]struct{})
	for _, root := range []string{"config", "data", "connection-tls"} {
		base := filepath.Join(staged, root)
		walkErr := filepath.WalkDir(base, func(current string, entry os.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(staged, current)
			if err != nil {
				return err
			}
			archivePath := filepath.ToSlash(relative)
			destination, err := archiveDestination(archivePath, configDir, dataDir, databasePath, tlsDestinations)
			if err != nil {
				return err
			}
			if destination == "" {
				return fmt.Errorf("restore path %q has no destination", archivePath)
			}
			targets[destination] = struct{}{}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
			return walkErr
		}
	}
	for _, name := range deleted {
		destination, err := archiveDestination(name, configDir, dataDir, databasePath, tlsDestinations)
		if err != nil {
			return err
		}
		if destination == "" {
			return fmt.Errorf("deleted path %q has no restore destination", name)
		}
		targets[destination] = struct{}{}
	}
	originals := make(map[string]restoreOriginal, len(targets))
	for target := range targets {
		if err := ensureNoSymlinkAncestors(target); err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			originals[target] = restoreOriginal{}
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore destination is not a regular file: %s", target)
		}
		content, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		originals[target] = restoreOriginal{content: content, mode: info.Mode().Perm(), exists: true}
	}
	if err := installStagedUnchecked(ctx, staged, configDir, dataDir, databasePath, tlsDestinations, deleted); err != nil {
		var rollbackErrors []string
		for target, original := range originals {
			if original.exists {
				if restoreErr := writeAtomic(target, original.content); restoreErr != nil {
					rollbackErrors = append(rollbackErrors, restoreErr.Error())
					continue
				}
				if restoreErr := os.Chmod(target, original.mode); restoreErr != nil {
					rollbackErrors = append(rollbackErrors, restoreErr.Error())
				}
			} else if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				rollbackErrors = append(rollbackErrors, removeErr.Error())
			}
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("restore failed: %w; rollback errors: %s", err, strings.Join(rollbackErrors, "; "))
		}
		return err
	}
	return nil
}

func installStagedUnchecked(ctx context.Context, staged, configDir, dataDir, databasePath string, tlsDestinations map[string]string, deleted []string) error {
	for _, root := range []struct {
		prefix string
		target string
	}{{"config", configDir}, {"data", dataDir}} {
		if root.target == "" {
			continue
		}
		base := filepath.Join(staged, root.prefix)
		if _, err := os.Stat(base); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := filepath.WalkDir(base, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := contextErr(ctx); err != nil {
				return err
			}
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("restore contains unsupported path %s", current)
			}
			if info.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(base, current)
			if err != nil {
				return err
			}
			destination := filepath.Join(root.target, relative)
			if root.prefix == "data" && filepath.ToSlash(relative) == "server.db" {
				if databasePath == "" {
					databasePath = filepath.Join(dataDir, "db", "server.db")
				}
				destination = databasePath
			}
			if err := ensureNoSymlinkAncestors(destination); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
				return err
			}
			content, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			return writeAtomic(destination, content)
		}); err != nil {
			return fmt.Errorf("install restored %s files: %w", root.prefix, err)
		}
	}
	tlsRoot := filepath.Join(staged, "connection-tls")
	if _, err := os.Stat(tlsRoot); err == nil {
		err = filepath.WalkDir(tlsRoot, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := contextErr(ctx); err != nil {
				return err
			}
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("restore contains unsupported TLS path %s", current)
			}
			relative, err := filepath.Rel(staged, current)
			if err != nil {
				return err
			}
			archivePath := filepath.ToSlash(relative)
			destination := tlsDestinations[archivePath]
			if destination == "" {
				return fmt.Errorf("restore requires a destination for connection TLS file %q", archivePath)
			}
			if err := ensureNoSymlinkAncestors(destination); err != nil {
				return err
			}
			content, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			return writeAtomic(destination, content)
		})
		if err != nil {
			return fmt.Errorf("install restored connection TLS files: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, name := range deleted {
		destination, err := archiveDestination(name, configDir, dataDir, databasePath, tlsDestinations)
		if err != nil {
			return err
		}
		if destination == "" {
			return fmt.Errorf("deleted path %q has no restore destination", name)
		}
		if err := ensureNoSymlinkAncestors(destination); err != nil {
			return err
		}
		if err := removeRegularFile(destination); err != nil {
			return fmt.Errorf("remove deleted restore path %s: %w", name, err)
		}
	}
	return nil
}

func archiveDestination(name, configDir, dataDir, databasePath string, tlsDestinations map[string]string) (string, error) {
	if !safeArchivePath(name) || !supportedArchiveRoot(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid archive path %q", name)
	}
	switch parts[0] {
	case "config":
		if configDir == "" {
			return "", errors.New("restore config directory is required")
		}
		return filepath.Join(configDir, filepath.FromSlash(parts[1])), nil
	case "data":
		if parts[1] == "server.db" {
			if databasePath != "" {
				return databasePath, nil
			}
			if dataDir == "" {
				return "", errors.New("restore data directory is required")
			}
			return filepath.Join(dataDir, "db", "server.db"), nil
		}
		if dataDir == "" {
			return "", errors.New("restore data directory is required")
		}
		return filepath.Join(dataDir, filepath.FromSlash(parts[1])), nil
	case "connection-tls":
		return tlsDestinations[name], nil
	default:
		return "", fmt.Errorf("unsupported archive root in %q", name)
	}
}

func removeRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to remove non-regular destination %s", path)
	}
	return os.Remove(path)
}

func ensureNoSymlinkAncestors(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	current := volume + string(os.PathSeparator)
	relative := strings.TrimPrefix(absolute, current)
	for _, part := range strings.Split(relative, string(os.PathSeparator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore destination traverses symbolic link %s", current)
		}
	}
	return nil
}

func supportedArchiveRoot(name string) bool {
	return strings.HasPrefix(name, "config/") || strings.HasPrefix(name, "data/") || strings.HasPrefix(name, "connection-tls/")
}

func writeAtomic(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".backup-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary backup file: %w", err)
	}
	temp := file.Name()
	defer func() { _ = os.Remove(temp) }()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return fmt.Errorf("install backup file: %w", err)
	}
	return nil
}

func safeArchivePath(name string) bool {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	platformPath := filepath.FromSlash(name)
	if filepath.IsAbs(platformPath) || filepath.VolumeName(platformPath) != "" {
		return false
	}
	if len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':' {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	return clean == name && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func pathJoin(base, name string) string {
	if name == "." || name == "" {
		return base
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(name, "/")
}

func normalizedLimits(limits Limits) Limits {
	if limits.MaxArchiveSize <= 0 {
		limits.MaxArchiveSize = defaultArchive
	}
	if limits.MaxExpandedSize <= 0 {
		limits.MaxExpandedSize = defaultExpanded
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaultFileCount
	}
	if limits.MaxFileSize <= 0 {
		limits.MaxFileSize = defaultFileCap
	}
	if limits.MaxRatio <= 0 {
		limits.MaxRatio = 100
	}
	return limits
}

func encryptionMethod(password string) string {
	if password == "" {
		return ""
	}
	return "AES-256-GCM"
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
