// Package auditlog writes append-only, tamper-evident JSONL audit events.
package auditlog

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	writeMu           sync.Mutex
	optionsMu         sync.RWMutex
	configuredOptions = map[string]Options{}
	reportedBroken    = map[string]bool{}
	warnedGroups      = map[string]bool{}
)

// Options controls audit categories, user-agent collection, and file lifecycle.
type Options struct {
	Enabled          bool
	SystemMode       bool
	Filename         string
	Format           string
	Rotate           string
	Keep             string
	Compress         bool
	Group            string
	IncludeUserAgent bool
	Events           EventCategories
	groupID          int
	groupResolved    bool
}

type EventCategories struct {
	Configuration bool
	Security      bool
	Backup        bool
	Server        bool
	TokenUsage    bool
}

type Actor struct {
	Type string  `json:"type"`
	ID   string  `json:"id,omitempty"`
	IP   *string `json:"ip,omitempty"`
}

type Target struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Event struct {
	ID       string         `json:"id"`
	Time     time.Time      `json:"time"`
	Event    string         `json:"event"`
	Category string         `json:"category"`
	Severity string         `json:"severity"`
	Actor    Actor          `json:"actor"`
	Target   *Target        `json:"target,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
	Result   string         `json:"result"`
	Source   string         `json:"source,omitempty"`
	Previous string         `json:"prev"`
}

// DefaultOptions enables all required audit categories with the AI.md defaults.
func DefaultOptions() Options {
	return Options{
		Enabled: true, Filename: "audit.log", Format: "json", Rotate: "daily", Keep: "none", IncludeUserAgent: true,
		Events: EventCategories{Configuration: true, Security: true, Backup: true, Server: true},
	}
}

// Configure registers audit settings for a log directory and verifies its
// active chain and anchor. It is safe to call before serving requests.
func Configure(logDir string, opts Options) error {
	if strings.TrimSpace(logDir) == "" {
		return errors.New("audit log directory is empty")
	}
	if opts.Filename == "" {
		opts.Filename = "audit.log"
	}
	if opts.Format == "" {
		opts.Format = "json"
	}
	if opts.Format != "json" {
		return errors.New("audit log format must be json")
	}
	if filepath.Base(opts.Filename) != opts.Filename || opts.Filename == "." || opts.Filename == ".." {
		return errors.New("invalid audit log filename")
	}
	if opts.Rotate == "" {
		opts.Rotate = "daily"
	}
	if opts.Keep == "" {
		opts.Keep = "none"
	}
	opts.Group = strings.TrimSpace(opts.Group)
	if !opts.SystemMode {
		opts.Group = ""
	}
	if opts.Group != "" {
		gid, err := lookupAuditGroup(opts.Group)
		if err != nil {
			warnUnknownAuditGroup(opts.Group)
			opts.Group = ""
		} else {
			opts.groupID = gid
			opts.groupResolved = true
		}
	}
	if !opts.Enabled {
		opts.Events = EventCategories{}
	}
	if err := validateRetention(opts.Keep); err != nil {
		return err
	}
	if _, err := parseRotation(opts.Rotate); err != nil {
		return err
	}
	if err := secureAuditDirectory(logDir, opts); err != nil {
		return err
	}
	abs, err := filepath.Abs(logDir)
	if err != nil {
		return err
	}
	optionsMu.Lock()
	configuredOptions[abs] = opts
	optionsMu.Unlock()
	if !opts.Enabled {
		return nil
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	path := filepath.Join(abs, opts.Filename)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("audit log path is not a regular file")
		}
		if err := secureAuditFile(path, opts); err != nil {
			return err
		}
		if verifyErr := verifyWithAnchor(path, filepath.Join(abs, "audit.anchor")); verifyErr != nil {
			reason := "anchor_mismatch"
			if strings.Contains(verifyErr.Error(), "anchor missing") {
				reason = "anchor_missing"
			}
			reportBrokenLocked(abs, opts, reason, verifyErr)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect audit log: %w", err)
	}
	return nil
}

// Write appends one audit event. System events may use a nil IP when no client
// address exists; request events must provide the observed client address.
func Write(logDir string, event Event, now time.Time) error {
	_, err := Append(logDir, event, now)
	return err
}

// Append writes one authoritative audit event and returns its assigned ID.
func Append(logDir string, event Event, now time.Time) (Event, error) {
	if strings.TrimSpace(logDir) == "" || strings.TrimSpace(event.Event) == "" || strings.TrimSpace(event.Category) == "" || strings.TrimSpace(event.Severity) == "" || strings.TrimSpace(event.Actor.Type) == "" || strings.TrimSpace(event.Result) == "" {
		return Event{}, errors.New("audit event is missing a required field")
	}
	if event.Actor.Type == "user" {
		event.Actor.Type = "owner"
	}
	if event.Source == "secret-rotation" {
		event.Source = "cli"
	}
	if event.Actor.Type != "owner" && event.Actor.Type != "operator" && event.Actor.Type != "anonymous" && event.Actor.Type != "system" {
		return Event{}, fmt.Errorf("invalid audit actor type %q", event.Actor.Type)
	}
	if event.Actor.Type == "system" || event.Actor.Type == "anonymous" || (event.Actor.Type == "operator" && (event.Actor.ID == "cli" || event.Actor.ID == "server" || event.Actor.ID == "scheduler")) {
		event.Actor.ID = ""
	}
	networkSource := event.Source == "web" || event.Source == "api" || event.Source == "mcp"
	if networkSource && event.Actor.Type != "system" && (event.Actor.IP == nil || strings.TrimSpace(*event.Actor.IP) == "") {
		return Event{}, errors.New("request audit event is missing its actor IP")
	}
	if event.Result != "success" && event.Result != "failure" {
		return Event{}, errors.New("audit result must be success or failure")
	}
	switch strings.ToLower(event.Severity) {
	case "info", "informational", "low":
		event.Severity = "info"
	case "warn", "warning", "medium":
		event.Severity = "warn"
	case "error":
		event.Severity = "error"
	case "critical", "high":
		event.Severity = "critical"
	default:
		return Event{}, errors.New("invalid audit severity")
	}
	if event.Source != "" && event.Source != "web" && event.Source != "api" && event.Source != "mcp" && event.Source != "cli" && event.Source != "scheduler" {
		return Event{}, errors.New("invalid audit source")
	}
	opts := optionsFor(logDir)
	if !opts.Enabled || !categoryEnabled(opts.Events, event.Category, event.Event) {
		return Event{}, nil
	}
	if event.Details != nil {
		event.Details = redactDetails(event.Details)
		if !opts.IncludeUserAgent {
			event.Details = withoutUserAgent(event.Details)
		}
	}
	if now.IsZero() {
		now = time.Now()
	}
	eventID, err := newULID(now)
	if err != nil {
		return Event{}, err
	}
	event.ID = eventID
	event.Time = now.UTC().Truncate(time.Millisecond)
	writeMu.Lock()
	defer writeMu.Unlock()
	abs, err := filepath.Abs(logDir)
	if err != nil {
		return Event{}, err
	}
	if err := secureAuditDirectory(abs, opts); err != nil {
		return Event{}, err
	}
	path := filepath.Join(abs, opts.Filename)
	anchorPath := filepath.Join(abs, "audit.anchor")
	info, err := os.Lstat(path)
	if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return Event{}, errors.New("audit log path is not a regular file")
	}
	if err != nil && !os.IsNotExist(err) {
		return Event{}, fmt.Errorf("inspect audit log: %w", err)
	}
	if err == nil {
		if verifyErr := verifyWithAnchor(path, anchorPath); verifyErr != nil {
			reason := "anchor_mismatch"
			if strings.Contains(verifyErr.Error(), "anchor missing") {
				reason = "anchor_missing"
			}
			reportBrokenLocked(abs, opts, reason, verifyErr)
		}
	}
	if err := rotateIfDueLocked(path, anchorPath, opts, now); err != nil {
		return Event{}, err
	}
	fileMode := auditFileMode(opts)
	if err := ensureLineBoundary(path, fileMode); err != nil {
		return Event{}, err
	}
	previous, err := lastLineHash(path, anchorPath)
	if err != nil {
		return Event{}, err
	}
	event.Previous = previous
	line, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode audit event: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return Event{}, fmt.Errorf("open audit log: %w", err)
	}
	if err := secureAuditFile(path, opts); err != nil {
		_ = file.Close()
		return Event{}, err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return Event{}, fmt.Errorf("append audit event: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return Event{}, fmt.Errorf("sync audit event: %w", err)
	}
	if err := file.Close(); err != nil {
		return Event{}, fmt.Errorf("close audit log: %w", err)
	}
	if err := verifyWithAnchor(path, anchorPath); err != nil {
		reason := "anchor_mismatch"
		if strings.Contains(err.Error(), "anchor missing") {
			reason = "anchor_missing"
		}
		reportBrokenLocked(abs, opts, reason, err)
	}
	return event, nil
}

// WriteAndMirror appends the authoritative file record before attempting its
// queryable database mirror. A mirror failure never removes or suppresses the
// file event.
func WriteAndMirror(logDir string, db *sql.DB, event Event, now time.Time) error {
	written, err := Append(logDir, event, now)
	if err != nil {
		return err
	}
	if written.ID == "" {
		return nil
	}
	if db == nil {
		return nil
	}
	details := make(map[string]any, len(written.Details)+1)
	for key, value := range written.Details {
		details[key] = value
	}
	details["audit_id"] = written.ID
	encoded, err := json.Marshal(details)
	if err == nil {
		var actorIP, targetType, targetID any
		if written.Actor.IP != nil {
			actorIP = *written.Actor.IP
		}
		if written.Target != nil {
			targetType, targetID = written.Target.Type, written.Target.ID
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err = db.ExecContext(ctx, `INSERT INTO audit_log (timestamp, level, category, action, actor_ip, target_type, target_id, details, success) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, written.Time.Unix(), written.Severity, written.Category, written.Event, actorIP, targetType, targetID, string(encoded), written.Result == "success")
	}
	if err != nil {
		log.Printf("audit.mirror_failed: audit_id=%s event=%s error=%v", written.ID, written.Event, err)
	}
	return nil
}

// Verify checks the previous-line hash chain and reports any modified or truncated line.
func Verify(path string) error {
	return verifyWithAnchor(path, filepath.Join(filepath.Dir(path), "audit.anchor"))
}

func verifyWithAnchor(path, anchorPath string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	previous, anchorErr := readAnchor(anchorPath)
	if errors.Is(anchorErr, os.ErrNotExist) {
		previous = strings.Repeat("0", 64)
	} else if anchorErr != nil {
		return anchorErr
	}
	anchorExists := anchorErr == nil
	lineNumber := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNumber++
			if line[len(line)-1] != '\n' {
				return fmt.Errorf("audit log line %d is incomplete", lineNumber)
			}
			line = line[:len(line)-1]
			var event Event
			if err := json.Unmarshal(line, &event); err != nil {
				return fmt.Errorf("decode audit log line %d: %w", lineNumber, err)
			}
			if lineNumber == 1 && !anchorExists && event.Previous != strings.Repeat("0", 64) {
				return errors.New("audit anchor missing for non-zero first prev")
			}
			if event.Previous != previous {
				return fmt.Errorf("audit log line %d does not follow previous line hash", lineNumber)
			}
			digest := sha256.Sum256(line)
			previous = hex.EncodeToString(digest[:])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read audit log: %w", readErr)
		}
	}
	return nil
}

func lastLineHash(path, anchorPath string) (string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		anchor, anchorErr := readAnchor(anchorPath)
		if errors.Is(anchorErr, os.ErrNotExist) {
			return strings.Repeat("0", 64), nil
		}
		return anchor, anchorErr
	}
	if err != nil {
		return "", fmt.Errorf("open audit log for chaining: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReader(file)
	var last []byte
	lastFound := false
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			last = append(last[:0], line...)
			lastFound = true
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read audit log for chaining: %w", readErr)
		}
	}
	if !lastFound {
		anchor, anchorErr := readAnchor(anchorPath)
		if errors.Is(anchorErr, os.ErrNotExist) {
			return strings.Repeat("0", 64), nil
		}
		return anchor, anchorErr
	}
	var item Event
	if err := json.Unmarshal(last, &item); err != nil {
		return "", fmt.Errorf("decode last audit event: %w", err)
	}
	digest := sha256.Sum256(last)
	return hex.EncodeToString(digest[:]), nil
}

func optionsFor(logDir string) Options {
	abs, err := filepath.Abs(logDir)
	if err != nil {
		return DefaultOptions()
	}
	optionsMu.RLock()
	opts, ok := configuredOptions[abs]
	optionsMu.RUnlock()
	if !ok {
		return DefaultOptions()
	}
	return opts
}

func categoryEnabled(categories EventCategories, category, event string) bool {
	category = strings.ToLower(strings.TrimSpace(category))
	event = strings.ToLower(strings.TrimSpace(event))
	if strings.HasPrefix(event, "token.usage") {
		return categories.TokenUsage
	}
	switch category {
	case "config", "configuration":
		return categories.Configuration
	case "security", "authentication", "secret", "secrets", "pgp", "token", "mcp":
		return categories.Security
	case "backup", "restore":
		return categories.Backup
	case "token_usage":
		return categories.TokenUsage
	case "server", "update", "scheduler", "maintenance", "compliance", "cli":
		return categories.Server
	default:
		return false
	}
}

func withoutUserAgent(details map[string]any) map[string]any {
	clean := make(map[string]any, len(details))
	for key, value := range details {
		if strings.EqualFold(key, "user_agent") || strings.EqualFold(key, "user-agent") || strings.EqualFold(key, "ua") {
			continue
		}
		switch nested := value.(type) {
		case map[string]any:
			clean[key] = withoutUserAgent(nested)
		case []any:
			items := make([]any, len(nested))
			for i, item := range nested {
				if child, ok := item.(map[string]any); ok {
					items[i] = withoutUserAgent(child)
				} else {
					items[i] = item
				}
			}
			clean[key] = items
		default:
			clean[key] = value
		}
	}
	return clean
}

func redactDetails(details map[string]any) map[string]any {
	clean := make(map[string]any, len(details))
	for key, value := range details {
		if sensitiveDetail(key) {
			clean[key] = "[redacted]"
			continue
		}
		switch nested := value.(type) {
		case map[string]any:
			clean[key] = redactDetails(nested)
		case []any:
			items := make([]any, len(nested))
			for i, item := range nested {
				if child, ok := item.(map[string]any); ok {
					items[i] = redactDetails(child)
				} else {
					items[i] = item
				}
			}
			clean[key] = items
		default:
			clean[key] = value
		}
	}
	return clean
}

func sensitiveDetail(name string) bool {
	name = strings.ToLower(strings.ReplaceAll(name, "-", "_"))
	for _, fragment := range []string{"password", "secret", "token", "credential", "authorization", "private_key", "api_key", "smtp_pass", "email"} {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}

func readAnchor(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("audit anchor is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return "", errors.New("audit anchor permissions must be 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if len(value) != 64 {
		return "", errors.New("audit anchor has invalid length")
	}
	if _, err := hex.DecodeString(value); err != nil || strings.ToLower(value) != value {
		return "", errors.New("audit anchor is not lowercase SHA-256 hex")
	}
	return value, nil
}

func writeAnchor(path, value string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".audit-anchor-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.WriteString(tmp, value+"\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func reportBrokenLocked(logDir string, opts Options, reason string, cause error) {
	if reportedBroken[logDir] {
		return
	}
	reportedBroken[logDir] = true
	event := Event{Event: "security.audit_chain_broken", Category: "security", Severity: "critical", Actor: Actor{Type: "system"}, Details: map[string]any{"file": opts.Filename, "reason": reason}, Result: "failure"}
	if strings.Contains(cause.Error(), "line ") {
		message := cause.Error()
		at := strings.Index(message, "line ")
		if at >= 0 {
			var line int
			_, _ = fmt.Sscanf(message[at:], "line %d", &line)
			if line > 0 {
				event.Details["line"] = line
			}
		}
	}
	if err := appendRawLocked(logDir, opts, event, time.Now().UTC()); err != nil {
		log.Printf("security.audit_chain_broken: file=%s reason=%s", opts.Filename, reason)
	}
}

func appendRawLocked(logDir string, opts Options, event Event, now time.Time) error {
	path := filepath.Join(logDir, opts.Filename)
	anchorPath := filepath.Join(logDir, "audit.anchor")
	fileMode := auditFileMode(opts)
	if err := ensureLineBoundary(path, fileMode); err != nil {
		return err
	}
	id, err := newULID(now)
	if err != nil {
		return err
	}
	event.ID, event.Time = id, now.UTC().Truncate(time.Millisecond)
	previous, err := lastLineHash(path, anchorPath)
	if err != nil {
		return err
	}
	event.Previous = previous
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	if err := secureAuditFile(path, opts); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func ensureLineBoundary(path string, fileMode os.FileMode) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	if info.Size() == 0 {
		return file.Close()
	}
	if _, err := file.Seek(-1, io.SeekEnd); err != nil {
		_ = file.Close()
		return err
	}
	var last [1]byte
	if _, err := file.Read(last[:]); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	appendFile, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	if _, err := appendFile.Write([]byte{'\n'}); err != nil {
		_ = appendFile.Close()
		return err
	}
	if err := appendFile.Sync(); err != nil {
		_ = appendFile.Close()
		return err
	}
	return appendFile.Close()
}

type rotation struct {
	period string
	size   int64
}

func parseRotation(value string) (rotation, error) {
	var result rotation
	for _, item := range strings.Split(strings.ToLower(strings.TrimSpace(value)), ",") {
		item = strings.TrimSpace(item)
		switch item {
		case "daily", "weekly", "monthly", "yearly", "never":
			result.period = item
		case "":
		default:
			var suffix string
			var multiplier int64
			if strings.HasSuffix(item, "mb") {
				suffix, multiplier = "mb", 1<<20
			} else if strings.HasSuffix(item, "gb") {
				suffix, multiplier = "gb", 1<<30
			} else {
				return result, fmt.Errorf("unsupported rotation option %q", item)
			}
			n, err := strconv.ParseInt(strings.TrimSuffix(item, suffix), 10, 64)
			if err != nil || n <= 0 {
				return result, fmt.Errorf("invalid rotation size %q", item)
			}
			result.size = n * multiplier
		}
	}
	if result.period == "" && result.size == 0 {
		return result, errors.New("empty rotation policy")
	}
	return result, nil
}

func rotateIfDueLocked(path, anchorPath string, opts Options, now time.Time) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := parseRotation(opts.Rotate)
	if err != nil {
		return err
	}
	due := p.period != "never" && periodElapsed(p.period, info.ModTime(), now)
	if p.size > 0 && info.Size() >= p.size {
		due = true
	}
	if !due || info.Size() == 0 {
		return nil
	}
	if err := verifyWithAnchor(path, anchorPath); err != nil {
		return nil
	}
	last, err := lastLineHash(path, anchorPath)
	if err != nil {
		return err
	}
	oldAnchor, oldAnchorErr := readAnchor(anchorPath)
	if err := writeAnchor(anchorPath, last); err != nil {
		return fmt.Errorf("write audit anchor before rotation: %w", err)
	}
	base := fmt.Sprintf("%s.%s", path, now.UTC().Format("2006-01-02"))
	rotated := base
	for index := 0; ; index++ {
		candidate := base
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", base, index)
		}
		archive := candidate
		if opts.Compress {
			archive += ".gz"
		}
		_, candidateErr := os.Lstat(candidate)
		_, archiveErr := os.Lstat(archive)
		if os.IsNotExist(candidateErr) && os.IsNotExist(archiveErr) {
			rotated = candidate
			break
		}
		if candidateErr != nil && !os.IsNotExist(candidateErr) {
			return fmt.Errorf("inspect audit archive: %w", candidateErr)
		}
		if archiveErr != nil && !os.IsNotExist(archiveErr) {
			return fmt.Errorf("inspect compressed audit archive: %w", archiveErr)
		}
	}
	if err := os.Rename(path, rotated); err != nil {
		if oldAnchorErr == nil {
			_ = writeAnchor(anchorPath, oldAnchor)
		} else if errors.Is(oldAnchorErr, os.ErrNotExist) {
			_ = os.Remove(anchorPath)
		}
		return fmt.Errorf("rotate audit log: %w", err)
	}
	if opts.Compress {
		if err := compressFile(rotated, opts); err != nil {
			return err
		}
	}
	return pruneAudit(path, opts.Keep)
}

func periodElapsed(period string, then, now time.Time) bool {
	then = then.UTC()
	now = now.UTC()
	if now.Before(then) {
		return false
	}
	switch period {
	case "daily":
		return then.Year() != now.Year() || then.YearDay() != now.YearDay()
	case "weekly":
		y1, w1 := then.ISOWeek()
		y2, w2 := now.ISOWeek()
		return y1 != y2 || w1 != w2
	case "monthly":
		return then.Year() != now.Year() || then.Month() != now.Month()
	case "yearly":
		return then.Year() != now.Year()
	default:
		return false
	}
}

func compressFile(path string, opts Options) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(path+".gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, auditFileMode(opts))
	if err != nil {
		return err
	}
	if err := secureAuditFile(path+".gz", opts); err != nil {
		_ = out.Close()
		_ = os.Remove(path + ".gz")
		return err
	}
	gz := gzip.NewWriter(out)
	_, copyErr := io.Copy(gz, in)
	closeGZErr := gz.Close()
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(path + ".gz")
		return copyErr
	}
	if closeGZErr != nil {
		_ = os.Remove(path + ".gz")
		return closeGZErr
	}
	if closeErr != nil {
		_ = os.Remove(path + ".gz")
		return closeErr
	}
	return os.Remove(path)
}

func auditDirectoryMode(opts Options) os.FileMode {
	if opts.SystemMode {
		return 0o750
	}
	return 0o700
}

func auditFileMode(opts Options) os.FileMode {
	if opts.SystemMode {
		return 0o640
	}
	return 0o600
}

func secureAuditDirectory(path string, opts Options) error {
	directoryMode := auditDirectoryMode(opts)
	if err := os.MkdirAll(path, directoryMode); err != nil {
		return fmt.Errorf("create audit directory: %w", err)
	}
	if opts.SystemMode {
		gid := defaultAuditGroupID()
		if opts.groupResolved {
			gid = opts.groupID
		}
		if gid >= 0 {
			if err := setAuditGroup(path, gid); err != nil {
				return fmt.Errorf("set audit directory group: %w", err)
			}
		}
	}
	if err := os.Chmod(path, directoryMode); err != nil {
		return fmt.Errorf("secure audit directory: %w", err)
	}
	return nil
}

func secureAuditFile(path string, opts Options) error {
	if opts.SystemMode {
		gid := defaultAuditGroupID()
		if opts.groupResolved {
			gid = opts.groupID
		}
		if gid >= 0 {
			if err := setAuditGroup(path, gid); err != nil {
				return fmt.Errorf("set audit file group: %w", err)
			}
		}
	}
	if err := os.Chmod(path, auditFileMode(opts)); err != nil {
		return fmt.Errorf("secure audit log: %w", err)
	}
	return nil
}

func warnUnknownAuditGroup(name string) {
	optionsMu.Lock()
	if warnedGroups[name] {
		optionsMu.Unlock()
		return
	}
	warnedGroups[name] = true
	optionsMu.Unlock()
	log.Printf("WARN: configured audit group %q does not exist or is unavailable; using the default group", name)
}

func validateRetention(value string) error {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "none" || value == "forever" {
		return nil
	}
	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) != 2 || (parts[0] != "weekly" && parts[0] != "monthly") {
			return fmt.Errorf("invalid retention policy %q", value)
		}
		value = parts[1]
	}
	if count, err := strconv.Atoi(value); err == nil && count >= 0 {
		return nil
	}
	if len(value) < 2 {
		return fmt.Errorf("invalid retention policy %q", value)
	}
	n, err := strconv.Atoi(value[:len(value)-1])
	if err != nil || n < 0 || !strings.Contains("dwm", value[len(value)-1:]) {
		return fmt.Errorf("invalid retention policy %q", value)
	}
	return nil
}

func pruneAudit(path, keep string) error {
	files, err := filepath.Glob(path + ".*")
	if err != nil {
		return err
	}
	anchorPath := path + ".anchor"
	archives := files[:0]
	for _, name := range files {
		if name != anchorPath {
			archives = append(archives, name)
		}
	}
	files = archives
	sort.Slice(files, func(i, j int) bool {
		leftTime, leftSequence := auditArchiveOrder(path, files[i])
		rightTime, rightSequence := auditArchiveOrder(path, files[j])
		if !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		return leftSequence < rightSequence
	})
	policy := strings.ToLower(strings.TrimSpace(keep))
	if policy == "forever" {
		return nil
	}
	if policy == "none" {
		for _, name := range files {
			if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	if strings.Contains(policy, ":") {
		policy = strings.SplitN(policy, ":", 2)[1]
	}
	if count, err := strconv.Atoi(policy); err == nil {
		for len(files) > count {
			if err := os.Remove(files[0]); err != nil && !os.IsNotExist(err) {
				return err
			}
			files = files[1:]
		}
		return nil
	}
	n, err := strconv.Atoi(policy[:len(policy)-1])
	if err != nil {
		return err
	}
	days := n
	switch policy[len(policy)-1] {
	case 'w':
		days *= 7
	case 'm':
		days *= 30
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	for _, name := range files {
		if info, err := os.Stat(name); err == nil && info.ModTime().Before(cutoff) {
			if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func auditArchiveOrder(path, archive string) (time.Time, int) {
	name := filepath.Base(archive)
	name = strings.TrimSuffix(name, ".gz")
	prefix := filepath.Base(path) + "."
	if !strings.HasPrefix(name, prefix) {
		if info, err := os.Stat(archive); err == nil {
			return info.ModTime(), 0
		}
		return time.Time{}, 0
	}
	value := strings.TrimPrefix(name, prefix)
	if timestamp, err := time.Parse("20060102T150405Z", value); err == nil {
		return timestamp, 0
	}
	sequence := 0
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		if parsed, err := strconv.Atoi(value[dot+1:]); err == nil {
			sequence = parsed
			value = value[:dot]
		}
	}
	if date, err := time.Parse("2006-01-02", value); err == nil {
		return date, sequence
	}
	if info, err := os.Stat(archive); err == nil {
		return info.ModTime(), 0
	}
	return time.Time{}, 0
}

func newULID(now time.Time) (string, error) {
	var raw [16]byte
	milliseconds := uint64(now.UnixMilli())
	raw[0] = byte(milliseconds >> 40)
	raw[1] = byte(milliseconds >> 32)
	raw[2] = byte(milliseconds >> 24)
	raw[3] = byte(milliseconds >> 16)
	raw[4] = byte(milliseconds >> 8)
	raw[5] = byte(milliseconds)
	if _, err := rand.Read(raw[6:]); err != nil {
		return "", fmt.Errorf("generate audit event ID: %w", err)
	}
	return base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding).EncodeToString(raw[:]), nil
}
