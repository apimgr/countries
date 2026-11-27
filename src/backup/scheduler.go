package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/countries/src/paths"
	"github.com/apimgr/countries/src/privilege"
	"github.com/shirou/gopsutil/v3/disk"
)

const defaultDiskThreshold = 90

var backupDatePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:_(\d{6}))?$`)

type DiskUsage struct {
	FreeBytes  uint64
	TotalBytes uint64
}

type (
	DiskUsageFunc func(path string) (DiskUsage, error)
	CreateFunc    func(context.Context, string, Options) (Result, error)
)

type RetentionPolicy struct {
	MaxBackups   int
	KeepWeekly   int
	KeepMonthly  int
	KeepYearly   int
	MaxTotalSize string
}

type ScheduledOptions struct {
	ProjectName      string
	BackupDir        string
	ResolveBackupDir func() (string, error)
	Backup           Options
	Retention        RetentionPolicy
	DiskThreshold    int
	DiskUsage        DiskUsageFunc
	Create           CreateFunc
	Now              func() time.Time
}

type ScheduleResult struct {
	Skipped     bool
	SkipReason  string
	Incremental *Result
	Full        *Result
	Deleted     []string
}

type Scheduler struct {
	options   ScheduledOptions
	backupDir string
}

// NewScheduler resolves the backup directory once and retains that path for
// scheduled writes and retention sweeps.
func NewScheduler(options ScheduledOptions) (*Scheduler, error) {
	if options.ProjectName == "" {
		return nil, errors.New("backup scheduler requires a project name")
	}
	if options.ResolveBackupDir != nil {
		resolved, err := options.ResolveBackupDir()
		if err != nil {
			return nil, fmt.Errorf("resolve backup directory: %w", err)
		}
		options.BackupDir = resolved
	}
	if strings.TrimSpace(options.BackupDir) == "" {
		return nil, errors.New("backup scheduler requires a backup directory")
	}
	absolute, err := filepath.Abs(options.BackupDir)
	if err != nil {
		return nil, fmt.Errorf("resolve backup directory: %w", err)
	}
	options.BackupDir = absolute
	if options.DiskThreshold <= 0 {
		options.DiskThreshold = defaultDiskThreshold
	}
	if options.DiskThreshold > 100 {
		return nil, errors.New("disk threshold must be at most 100 percent")
	}
	if options.Retention.MaxBackups <= 0 {
		options.Retention.MaxBackups = 1
	}
	if options.Retention.KeepWeekly < 0 || options.Retention.KeepMonthly < 0 || options.Retention.KeepYearly < 0 {
		return nil, errors.New("backup retention counts cannot be negative")
	}
	if options.DiskUsage == nil {
		options.DiskUsage = platformDiskUsage
	}
	if options.Create == nil {
		options.Create = Create
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if err := EnsureBackupDir(absolute); err != nil {
		return nil, err
	}
	return &Scheduler{options: options, backupDir: absolute}, nil
}

// RunDaily creates the cumulative incremental against the latest full before
// creating the new full, both from the same snapshot instant.
func (s *Scheduler) RunDaily(ctx context.Context) (ScheduleResult, error) {
	if err := EnsureBackupDir(s.backupDir); err != nil {
		return ScheduleResult{}, err
	}
	if err := sweepBackupTemporaryFiles(s.backupDir); err != nil {
		return ScheduleResult{}, fmt.Errorf("backup temporary-file sweep: %w", err)
	}
	candidatePath, err := s.newestFullCandidate(ctx)
	if err != nil {
		return ScheduleResult{}, err
	}
	var latest *Result
	forceTimestampedFull := false
	if candidatePath != "" {
		matches, headerErr := backupPasswordMatches(candidatePath, s.options.Backup.Password)
		if headerErr != nil {
			log.Printf("WARN: latest backup base is corrupt; skipping daily incremental: %v", headerErr)
			forceTimestampedFull = true
		} else if !matches {
			log.Printf("INFO: backup encryption password changed; skipping daily incremental")
			forceTimestampedFull = true
		} else {
			verified, verifyErr := Verify(ctx, candidatePath, s.options.Backup.Password, s.options.Backup.Limits)
			if verifyErr != nil {
				log.Printf("WARN: latest backup base failed verification; skipping daily incremental: %v", verifyErr)
				forceTimestampedFull = true
			} else if verified.Manifest.Type == "full" {
				latest = &verified
			}
		}
	}
	if skip, reason, err := s.diskPressure(); err != nil {
		return ScheduleResult{}, err
	} else if skip {
		return ScheduleResult{Skipped: true, SkipReason: reason}, nil
	}
	snapshotAt := s.options.Now().UTC()
	fullPath := s.fullPath(snapshotAt)
	if forceTimestampedFull {
		fullPath = s.timestampedFullPath(snapshotAt)
	}
	if _, err := os.Lstat(fullPath); err == nil && !forceTimestampedFull {
		if existing, verifyErr := Verify(ctx, fullPath, s.options.Backup.Password, s.options.Backup.Limits); verifyErr == nil && existing.Manifest.Type == "full" {
			return ScheduleResult{Skipped: true, SkipReason: "daily full backup already exists for this date"}, nil
		}
		fullPath = s.timestampedFullPath(snapshotAt)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return ScheduleResult{}, err
	}
	if _, err := os.Lstat(fullPath); err == nil {
		return ScheduleResult{}, fmt.Errorf("full backup filename already exists: %s", fullPath)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ScheduleResult{}, err
	}
	snapshot, err := CaptureSnapshot(ctx, s.options.Backup)
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("capture daily backup snapshot: %w", err)
	}
	result := ScheduleResult{}
	if latest != nil {
		incrementalPath := s.archivePath("daily")
		backupOptions := s.options.Backup
		backupOptions.Type = "incremental"
		backupOptions.SnapshotAt = snapshotAt
		backupOptions.BaseBackup = filepath.Base(latest.Path)
		backupOptions.BaseArchivePath = latest.Path
		backupOptions.BaseSHA256 = latest.SHA256
		backupOptions.Snapshot = snapshot
		incremental, err := s.options.Create(ctx, incrementalPath, backupOptions)
		if err != nil {
			return result, fmt.Errorf("create daily incremental: %w", err)
		}
		result.Incremental = &incremental
	}
	backupOptions := s.options.Backup
	backupOptions.Type = "full"
	backupOptions.SnapshotAt = snapshotAt
	backupOptions.BaseBackup = ""
	backupOptions.BaseArchivePath = ""
	backupOptions.BaseSHA256 = ""
	backupOptions.Deleted = nil
	backupOptions.Snapshot = snapshot
	full, err := s.options.Create(ctx, fullPath, backupOptions)
	if err != nil {
		return result, fmt.Errorf("create daily full backup: %w", err)
	}
	result.Full = &full
	deleted, err := s.ApplyRetention(ctx)
	if err != nil {
		return result, fmt.Errorf("apply backup retention: %w", err)
	}
	result.Deleted = deleted
	return result, nil
}

// EnsureBackupDir creates and secures the configured backup directory and verifies it is writable.
func EnsureBackupDir(path string) error {
	mode := os.FileMode(0o700)
	if privilege.IsElevated() {
		mode = 0o755
	}
	if err := paths.EnsureDir(path); err != nil {
		return backupDirectoryError(path, mode, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return backupDirectoryError(path, mode, err)
	}
	probe, err := os.CreateTemp(path, ".countries-backup-probe-*")
	if err != nil {
		return backupDirectoryError(path, mode, err)
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return backupDirectoryError(path, mode, err)
	}
	if err := os.Remove(probePath); err != nil {
		return backupDirectoryError(path, mode, err)
	}
	return nil
}

func backupDirectoryError(path string, expectedMode os.FileMode, cause error) error {
	owner := "unknown"
	mode := expectedMode.String()
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm().String()
		value := reflect.ValueOf(info.Sys())
		indirect := reflect.Indirect(value)
		if indirect.IsValid() && indirect.Kind() == reflect.Struct {
			uid := indirect.FieldByName("Uid")
			if uid.IsValid() && uid.CanUint() {
				if account, err := user.LookupId(strconv.FormatUint(uid.Uint(), 10)); err == nil {
					owner = account.Username
				}
			}
		}
	}
	running := "unknown"
	if account, err := user.Current(); err == nil {
		running = account.Username
	}
	return fmt.Errorf("backup directory %q (owner %s, mode %s; running user %s) is unavailable: %w", path, owner, mode, running, cause)
}

func sweepBackupTemporaryFiles(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".backup-verify-") || strings.HasPrefix(name, ".countries-backup-probe-") || strings.HasPrefix(name, ".countries-backup-partial-") {
			if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// RunHourly creates a cumulative incremental against the newest full archive.
func (s *Scheduler) RunHourly(ctx context.Context) (ScheduleResult, error) {
	if err := EnsureBackupDir(s.backupDir); err != nil {
		return ScheduleResult{}, err
	}
	if err := sweepBackupTemporaryFiles(s.backupDir); err != nil {
		return ScheduleResult{}, fmt.Errorf("backup temporary-file sweep: %w", err)
	}
	latest, err := s.latestFull(ctx)
	if err != nil {
		return ScheduleResult{}, err
	}
	if latest == nil {
		return ScheduleResult{Skipped: true, SkipReason: "no full backup exists"}, nil
	}
	if skip, reason, err := s.diskPressure(); err != nil {
		return ScheduleResult{}, err
	} else if skip {
		return ScheduleResult{Skipped: true, SkipReason: reason}, nil
	}
	backupOptions := s.options.Backup
	backupOptions.Type = "incremental"
	backupOptions.SnapshotAt = s.options.Now().UTC()
	backupOptions.BaseBackup = filepath.Base(latest.Path)
	backupOptions.BaseArchivePath = latest.Path
	backupOptions.BaseSHA256 = latest.SHA256
	result, err := s.options.Create(ctx, s.archivePath("hourly"), backupOptions)
	if err != nil {
		return ScheduleResult{}, fmt.Errorf("create hourly incremental: %w", err)
	}
	deleted, err := s.ApplyRetention(ctx)
	if err != nil {
		return ScheduleResult{Incremental: &result}, fmt.Errorf("apply backup retention: %w", err)
	}
	return ScheduleResult{Incremental: &result, Deleted: deleted}, nil
}

// ApplyRetention prunes only recognized project backup paths and returns the
// deleted paths. Daily/hourly incrementals are kept, along with their full bases.
func (s *Scheduler) ApplyRetention(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.backupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	backups := make([]backupCandidate, 0)
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		kind, timestamp, ok := s.classifyBackupName(name)
		if !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if timestamp.IsZero() {
			timestamp = info.ModTime()
		}
		candidate := backupCandidate{path: filepath.Join(s.backupDir, name), name: name, kind: kind, timestamp: timestamp, size: info.Size()}
		backups = append(backups, candidate)
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].timestamp.Equal(backups[j].timestamp) {
			return backups[i].name < backups[j].name
		}
		return backups[i].timestamp.Before(backups[j].timestamp)
	})
	keep := make(map[string]bool)
	markNewestMatching(backups, keep, s.options.Retention.KeepYearly, func(item backupCandidate) bool {
		return item.kind == "full" && item.timestamp.Month() == time.January && item.timestamp.Day() == 1
	})
	markNewestMatching(backups, keep, s.options.Retention.KeepMonthly, func(item backupCandidate) bool {
		return item.kind == "full" && item.timestamp.Day() == 1
	})
	markNewestMatching(backups, keep, s.options.Retention.KeepWeekly, func(item backupCandidate) bool {
		return item.kind == "full" && item.timestamp.Weekday() == time.Sunday
	})
	markNewestMatching(backups, keep, s.options.Retention.MaxBackups, func(item backupCandidate) bool {
		return item.kind == "full"
	})
	for _, item := range backups {
		if item.kind == "daily" || item.kind == "hourly" {
			keep[item.name] = true
		}
	}
	protectedBases := make(map[string]bool)
	incrementalBases := make(map[string]string)
	baseReferences := make(map[string]int)
	for _, item := range backups {
		if item.kind != "daily" && item.kind != "hourly" {
			continue
		}
		baseName, isIncremental, err := incrementalBaseReference(ctx, item.path, s.options.Backup.Limits)
		if err != nil {
			return nil, fmt.Errorf("inspect retained incremental %s: %w", item.name, err)
		}
		if isIncremental && baseName != "" {
			protectedBases[baseName] = true
			incrementalBases[item.name] = baseName
			baseReferences[baseName]++
			keep[baseName] = true
		}
	}
	deleted := make([]string, 0)
	for _, item := range backups {
		if item.kind == "daily" || item.kind == "hourly" || keep[item.name] {
			continue
		}
		if err := os.Remove(item.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return deleted, err
		}
		deleted = append(deleted, item.path)
	}
	capBytes, err := parseSizeCap(s.options.Retention.MaxTotalSize, s.backupDir, s.options.DiskUsage)
	if err != nil {
		return deleted, err
	}
	if capBytes > 0 {
		remaining := make([]backupCandidate, 0, len(backups)-len(deleted))
		var total int64
		for _, item := range backups {
			if containsPath(deleted, item.path) {
				continue
			}
			remaining = append(remaining, item)
			total += item.size
		}
		for total > capBytes {
			progress := false
			for _, item := range remaining {
				if total <= capBytes {
					break
				}
				if containsPath(deleted, item.path) || protectedBases[item.name] {
					continue
				}
				if err := os.Remove(item.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return deleted, err
				}
				total -= item.size
				deleted = append(deleted, item.path)
				progress = true
				if base := incrementalBases[item.name]; base != "" {
					baseReferences[base]--
					if baseReferences[base] <= 0 {
						delete(protectedBases, base)
					}
				}
			}
			if !progress {
				break
			}
		}
	}
	return deleted, nil
}

type backupCandidate struct {
	path      string
	name      string
	kind      string
	timestamp time.Time
	size      int64
}

func (s *Scheduler) latestFull(ctx context.Context) (*Result, error) {
	path, err := s.newestFullCandidate(ctx)
	if err != nil || path == "" {
		return nil, err
	}
	verified, err := Verify(ctx, path, s.options.Backup.Password, s.options.Backup.Limits)
	if err != nil {
		return nil, err
	}
	if verified.Manifest.Type != "full" {
		return nil, errors.New("newest backup candidate is not a full backup")
	}
	return &verified, nil
}

func (s *Scheduler) newestFullCandidate(ctx context.Context) (string, error) {
	entries, err := os.ReadDir(s.backupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var latestPath string
	var latestTime time.Time
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return "", err
		}
		if !entry.Type().IsRegular() {
			continue
		}
		kind, _, ok := s.classifyBackupName(entry.Name())
		if !ok || kind != "full" {
			continue
		}
		path := filepath.Join(s.backupDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		candidateTime := info.ModTime()
		if _, parsed, ok := s.classifyBackupName(entry.Name()); ok && !parsed.IsZero() {
			candidateTime = parsed
		}
		if latestPath == "" || candidateTime.After(latestTime) {
			latestPath, latestTime = path, candidateTime
		}
	}
	return latestPath, nil
}

// NewestFullArchive selects a named full backup by filename date, then by
// modification time for files from the same date. It never opens archives.
func NewestFullArchive(ctx context.Context, projectName, backupDir string) (string, error) {
	if strings.TrimSpace(projectName) == "" || strings.TrimSpace(backupDir) == "" {
		return "", errors.New("newest full backup requires a project name and directory")
	}
	entries, err := os.ReadDir(backupDir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	prefix := projectName + "_backup_"
	var latestPath string
	var latestDate, latestModTime time.Time
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return "", err
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			continue
		}
		name := entry.Name()
		stem := strings.TrimSuffix(strings.TrimSuffix(name, ".enc"), ".tar.gz")
		if !strings.HasPrefix(stem, prefix) {
			continue
		}
		parts := backupDatePattern.FindStringSubmatch(strings.TrimPrefix(stem, prefix))
		if parts == nil {
			continue
		}
		date, err := time.Parse("2006-01-02", parts[1])
		if err != nil {
			continue
		}
		info, err := os.Lstat(filepath.Join(backupDir, name))
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if latestPath == "" || date.After(latestDate) || date.Equal(latestDate) && info.ModTime().After(latestModTime) {
			latestPath, latestDate, latestModTime = filepath.Join(backupDir, name), date, info.ModTime()
		}
	}
	return latestPath, nil
}

func (s *Scheduler) diskPressure() (bool, string, error) {
	usage, err := s.options.DiskUsage(s.backupDir)
	if err != nil {
		return false, "", fmt.Errorf("inspect backup disk usage: %w", err)
	}
	usedPercent := float64(0)
	if usage.TotalBytes > 0 && usage.FreeBytes <= usage.TotalBytes {
		usedPercent = float64(usage.TotalBytes-usage.FreeBytes) * 100 / float64(usage.TotalBytes)
	}
	if usedPercent > float64(s.options.DiskThreshold) {
		return true, fmt.Sprintf("disk usage %.2f%% exceeds threshold %d%%", usedPercent, s.options.DiskThreshold), nil
	}
	latestSize, err := s.latestBackupSize()
	if err != nil {
		return false, "", err
	}
	if latestSize > 0 && usage.FreeBytes < uint64(latestSize)*2 {
		return true, fmt.Sprintf("free space %d bytes is below twice the latest backup size %d bytes", usage.FreeBytes, latestSize), nil
	}
	return false, "", nil
}

func (s *Scheduler) latestBackupSize() (int64, error) {
	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		return 0, err
	}
	var newest time.Time
	var size int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if _, _, ok := s.classifyBackupName(entry.Name()); !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return 0, err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
			size = info.Size()
		}
	}
	return size, nil
}

func (s *Scheduler) classifyBackupName(name string) (string, time.Time, bool) {
	if name == s.options.ProjectName+"-daily.tar.gz" || name == s.options.ProjectName+"-daily.tar.gz.enc" {
		return "daily", time.Time{}, true
	}
	if name == s.options.ProjectName+"-hourly.tar.gz" || name == s.options.ProjectName+"-hourly.tar.gz.enc" {
		return "hourly", time.Time{}, true
	}
	prefix := s.options.ProjectName + "_backup_"
	if !strings.HasPrefix(name, prefix) {
		incrementalPrefix := s.options.ProjectName + "-"
		if strings.HasPrefix(name, incrementalPrefix) && (strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.gz.enc")) {
			return "full", time.Time{}, true
		}
		return "", time.Time{}, false
	}
	stem := strings.TrimPrefix(name, prefix)
	if strings.HasSuffix(stem, ".tar.gz.enc") {
		stem = strings.TrimSuffix(stem, ".tar.gz.enc")
	} else if strings.HasSuffix(stem, ".tar.gz") {
		stem = strings.TrimSuffix(stem, ".tar.gz")
	}
	parts := backupDatePattern.FindStringSubmatch(stem)
	if parts == nil {
		return "full", time.Time{}, true
	}
	layout, value := "2006-01-02", parts[1]
	if parts[2] != "" {
		layout = "2006-01-02_150405"
		value += "_" + parts[2]
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return "full", time.Time{}, true
	}
	return "full", parsed, true
}

func (s *Scheduler) archivePath(kind string) string {
	extension := ".tar.gz"
	if s.options.Backup.Password != "" {
		extension += ".enc"
	}
	return filepath.Join(s.backupDir, s.options.ProjectName+"-"+kind+extension)
}

func (s *Scheduler) fullPath(snapshotAt time.Time) string {
	extension := ".tar.gz"
	if s.options.Backup.Password != "" {
		extension += ".enc"
	}
	return filepath.Join(s.backupDir, s.options.ProjectName+"_backup_"+snapshotAt.Format("2006-01-02")+extension)
}

func (s *Scheduler) timestampedFullPath(snapshotAt time.Time) string {
	extension := ".tar.gz"
	if s.options.Backup.Password != "" {
		extension += ".enc"
	}
	return filepath.Join(s.backupDir, s.options.ProjectName+"_backup_"+snapshotAt.Format("2006-01-02_150405")+extension)
}

func markNewestMatching(backups []backupCandidate, keep map[string]bool, count int, predicate func(backupCandidate) bool) {
	marked := 0
	for index := len(backups) - 1; index >= 0 && marked < count; index-- {
		if !keep[backups[index].name] && predicate(backups[index]) {
			keep[backups[index].name] = true
			marked++
		}
	}
}

func readManifest(ctx context.Context, path, password string, limits Limits) (Manifest, error) {
	verified, err := Verify(ctx, path, password, limits)
	if err != nil {
		return Manifest{}, err
	}
	return verified.Manifest, nil
}

func incrementalBaseReference(ctx context.Context, path string, limits Limits) (string, bool, error) {
	header, encrypted, err := readEncryptedBackupHeader(path)
	if err != nil {
		return "", false, err
	}
	if encrypted {
		if header.baseBackup == "" {
			return "", false, errors.New("encrypted incremental header has no base reference")
		}
		return header.baseBackup, true, nil
	}
	manifest, err := readManifest(ctx, path, "", limits)
	if err != nil {
		return "", false, err
	}
	if manifest.Type != "incremental" || manifest.BaseBackup == "" {
		return "", false, errors.New("incremental archive has no valid base reference")
	}
	return manifest.BaseBackup, true, nil
}

func parseSizeCap(value, volume string, usage DiskUsageFunc) (int64, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "0" || value == "false" || value == "no" || value == "none" || value == "disable" || value == "disabled" || value == "off" {
		return 0, nil
	}
	if strings.HasSuffix(value, "%") {
		percent, err := strconv.ParseUint(strings.TrimSuffix(value, "%"), 10, 64)
		if err != nil || percent > 100 {
			return 0, fmt.Errorf("invalid backup size cap %q", value)
		}
		capacity, err := usage(volume)
		if err != nil {
			return 0, err
		}
		return int64(capacity.TotalBytes/100*percent + capacity.TotalBytes%100*percent/100), nil
	}
	units := map[byte]uint64{'k': 1 << 10, 'm': 1 << 20, 'g': 1 << 30, 't': 1 << 40}
	if value == "" {
		return 0, errors.New("backup size cap is empty")
	}
	value = strings.TrimSuffix(value, "b")
	last := value[len(value)-1]
	multiplier := uint64(1)
	if unit, ok := units[last]; ok {
		multiplier = unit
		value = value[:len(value)-1]
	}
	amount, err := strconv.ParseUint(value, 10, 64)
	if err != nil || amount > uint64(^uint64(0))/multiplier || amount*multiplier > uint64(^uint64(0)>>1) {
		return 0, fmt.Errorf("invalid backup size cap %q", value)
	}
	return int64(amount * multiplier), nil
}

func containsPath(paths []string, value string) bool {
	for _, path := range paths {
		if path == value {
			return true
		}
	}
	return false
}

func platformDiskUsage(path string) (DiskUsage, error) {
	usage, err := disk.Usage(path)
	if err != nil {
		return DiskUsage{}, err
	}
	return DiskUsage{FreeBytes: usage.Free, TotalBytes: usage.Total}, nil
}
