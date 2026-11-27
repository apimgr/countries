package auditlog

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/testutil"
)

func TestWriteAppendsPrivateStructuredEvents(t *testing.T) {
	logDir := filepath.Join(testutil.TempDir(t), "logs")
	when := time.Date(2026, 11, 22, 12, 13, 14, 123456789, time.FixedZone("offset", -5*60*60))
	for _, eventName := range []string{"update.staged", "update.failed"} {
		if err := Write(logDir, Event{
			Event: eventName, Category: "update", Severity: "info",
			Actor: Actor{Type: "system"}, Result: "success",
			Details: map[string]any{"version": "v1.2.3"},
		}, when); err != nil {
			t.Fatalf("Write(%q) error = %v", eventName, err)
		}
	}
	path := filepath.Join(logDir, "audit.log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("audit log permissions = %04o, want 0600", info.Mode().Perm())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(contents), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("audit log has %d lines, want 2", len(lines))
	}
	var first, second map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatal(err)
	}
	if first["event"] != "update.staged" || second["event"] != "update.failed" || first["id"] == second["id"] {
		t.Fatalf("audit events = %#v and %#v", first, second)
	}
	if first["time"] != "2026-11-22T17:13:14.123Z" || first["category"] != "update" {
		t.Fatalf("audit fields = %#v", first)
	}
	actor, ok := first["actor"].(map[string]any)
	if !ok || actor["type"] != "system" || actor["ip"] != nil {
		t.Fatalf("system actor = %#v", first["actor"])
	}
	if err := Verify(path); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	firstHash := sha256.Sum256(lines[0])
	if first["prev"] != strings.Repeat("0", 64) || second["prev"] != hex.EncodeToString(firstHash[:]) {
		t.Fatalf("previous-line hash chain = %#v then %#v", first, second)
	}
}

func TestAuditPermissionsFollowRuntimeModeAndRepairExistingPaths(t *testing.T) {
	for _, tc := range []struct {
		name          string
		systemMode    bool
		wantDirectory os.FileMode
		wantFile      os.FileMode
	}{
		{name: "user", wantDirectory: 0700, wantFile: 0600},
		{name: "system", systemMode: true, wantDirectory: 0750, wantFile: 0640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(testutil.TempDir(t), "logs")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "audit.log")
			if err := os.WriteFile(path, []byte(""), 0644); err != nil {
				t.Fatal(err)
			}
			opts := DefaultOptions()
			opts.SystemMode = tc.systemMode
			if err := Configure(dir, opts); err != nil {
				t.Fatal(err)
			}
			if err := Write(dir, Event{Event: "server.started", Category: "server", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, time.Now()); err != nil {
				t.Fatal(err)
			}
			dirInfo, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			fileInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := dirInfo.Mode().Perm(); got != tc.wantDirectory {
				t.Errorf("directory mode = %04o, want %04o", got, tc.wantDirectory)
			}
			if got := fileInfo.Mode().Perm(); got != tc.wantFile {
				t.Errorf("audit file mode = %04o, want %04o", got, tc.wantFile)
			}
		})
	}
}

func TestWriteRequiresEventIdentity(t *testing.T) {
	if err := Write(testutil.TempDir(t), Event{Category: "update", Severity: "info", Actor: Actor{Type: "system"}, Result: "failure"}, time.Now()); err == nil {
		t.Fatal("Write accepted an event without an action")
	}
}

func TestWriteRequiresIPForRequestActor(t *testing.T) {
	err := Write(testutil.TempDir(t), Event{Event: "security.test", Category: "security", Severity: "warn", Actor: Actor{Type: "request"}, Result: "failure"}, time.Now())
	if err == nil {
		t.Fatal("Write accepted a request event without an IP address")
	}
}

func TestWriteAndMirrorAppendsBeforeBestEffortDatabaseMirror(t *testing.T) {
	root := testutil.TempDir(t)
	db, err := database.Open(filepath.Join(root, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(root, "logs")
	event := Event{Event: "update.staged", Category: "update", Severity: "info", Actor: Actor{Type: "system"}, Details: map[string]any{"version": "v1.2.3"}, Result: "success"}
	if err := WriteAndMirror(logDir, db, event, time.Now().UTC()); err != nil {
		t.Fatalf("WriteAndMirror(): %v", err)
	}
	var details string
	if err := db.QueryRow(`SELECT details FROM audit_log WHERE action = ?`, event.Event).Scan(&details); err != nil {
		t.Fatal(err)
	}
	var mirrored map[string]any
	if err := json.Unmarshal([]byte(details), &mirrored); err != nil {
		t.Fatal(err)
	}
	if id, ok := mirrored["audit_id"].(string); !ok || len(id) != 26 {
		t.Fatalf("mirrored audit_id = %#v", mirrored["audit_id"])
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := WriteAndMirror(logDir, db, Event{Event: "update.failed", Category: "update", Severity: "error", Actor: Actor{Type: "system"}, Result: "failure"}, time.Now().UTC()); err != nil {
		t.Fatalf("database mirror failure discarded the authoritative event: %v", err)
	}
	if err := Verify(filepath.Join(logDir, "audit.log")); err != nil {
		t.Fatalf("Verify(): %v", err)
	}
}

func TestWriteRejectsSymlinkAuditPathBeforeReadingTarget(t *testing.T) {
	root := testutil.TempDir(t)
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.log")
	if err := os.WriteFile(target, []byte("private target"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(logDir, "audit.log")); err != nil {
		t.Fatal(err)
	}
	err := Write(logDir, Event{
		Event: "server.started", Category: "server", Severity: "info",
		Actor: Actor{Type: "system"}, Result: "success",
	}, time.Now())
	if err == nil {
		t.Fatal("Write accepted a symlink audit path")
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "private target" {
		t.Fatalf("symlink target changed to %q", contents)
	}
}

func TestVerifyRejectsTamperedLog(t *testing.T) {
	path := filepath.Join(testutil.TempDir(t), "audit.log")
	if err := Write(filepath.Dir(path), Event{Event: "server.started", Category: "server", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path); err == nil {
		t.Fatal("Verify accepted a tampered log")
	}
}

func TestRotationKeepsAnchorAndContinuesPreviousLineChain(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "logs")
	opts := DefaultOptions()
	opts.Rotate = "daily"
	opts.Keep = "2"
	opts.Compress = true
	if err := Configure(dir, opts); err != nil {
		t.Fatal(err)
	}
	firstTime := time.Date(2030, 2, 1, 12, 0, 0, 0, time.UTC)
	first, err := Append(dir, Event{Event: "server.started", Category: "server", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, firstTime)
	if err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(dir, "audit.log")
	firstLine, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	firstLine = bytes.TrimSuffix(firstLine, []byte{'\n'})
	firstHash := sha256.Sum256(firstLine)
	second, err := Append(dir, Event{Event: "server.stopped", Category: "server", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, firstTime.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Previous != hex.EncodeToString(firstHash[:]) {
		t.Fatalf("rotated first prev = %q, want %x", second.Previous, firstHash)
	}
	anchor, err := os.ReadFile(filepath.Join(dir, "audit.anchor"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(anchor)) != second.Previous {
		t.Fatalf("anchor = %q, want %q", anchor, second.Previous)
	}
	if err := Verify(filepath.Join(dir, "audit.log")); err != nil {
		t.Fatalf("Verify after rotation: %v", err)
	}
	archives, err := filepath.Glob(filepath.Join(dir, "audit.log.*.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("compressed archives = %v", archives)
	}
	archive, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	reader, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
	_ = first
}

func TestAuditOptionsFilterCategoriesAndUserAgent(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "logs")
	opts := DefaultOptions()
	opts.Events.Configuration = false
	opts.IncludeUserAgent = false
	if err := Configure(dir, opts); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Event{Event: "config.updated", Category: "config", Severity: "info", Actor: Actor{Type: "system"}, Details: map[string]any{"user_agent": "private-agent", "changed_keys": []any{"server.port"}}, Result: "success"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Event{Event: "security.report_received", Category: "security", Severity: "warn", Actor: Actor{Type: "system"}, Details: map[string]any{"user_agent": "private-agent", "smtp_password": "private-password"}, Result: "success"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Event{Event: "token.created", Category: "token", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Event{Event: "token.usage", Category: "token", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(contents, []byte("config.updated")) || bytes.Contains(contents, []byte("private-agent")) || bytes.Contains(contents, []byte("private-password")) || bytes.Contains(contents, []byte("token.usage")) || !bytes.Contains(contents, []byte("security.report_received")) || !bytes.Contains(contents, []byte("token.created")) || !bytes.Contains(contents, []byte("[redacted]")) {
		t.Fatalf("audit options were not applied: %s", contents)
	}
}

func TestAppendReportsBrokenChainOnceAndContinues(t *testing.T) {
	dir := filepath.Join(testutil.TempDir(t), "logs")
	for _, name := range []string{"first", "second"} {
		if err := Write(dir, Event{Event: "server." + name, Category: "server", Severity: "info", Actor: Actor{Type: "system"}, Details: map[string]any{"label": name}, Result: "success"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "audit.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	var altered map[string]any
	if err := json.Unmarshal(lines[0], &altered); err != nil {
		t.Fatal(err)
	}
	altered["details"] = map[string]any{"label": "changed-without-authority"}
	lines[0], err = json.Marshal(altered)
	if err != nil {
		t.Fatal(err)
	}
	data = append(bytes.Join(lines, []byte{'\n'}), '\n')
	if err := os.WriteFile(path, data, 0640); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"third", "fourth"} {
		if err := Write(dir, Event{Event: "server." + name, Category: "server", Severity: "info", Actor: Actor{Type: "system"}, Result: "success"}, time.Now()); err != nil {
			t.Fatalf("append after chain break: %v", err)
		}
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(data, []byte("security.audit_chain_broken")); n != 1 {
		t.Fatalf("chain-break reports = %d, want one: %s", n, data)
	}
	if !bytes.Contains(data, []byte("server.third")) || !bytes.Contains(data, []byte("server.fourth")) {
		t.Fatalf("audit writer stopped after detecting tampering: %s", data)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if event["event"] == "security.audit_chain_broken" {
			encoded, _ := json.Marshal(event["details"])
			if bytes.Contains(encoded, []byte("changed-without-authority")) {
				t.Fatalf("chain-break report leaked event contents: %s", encoded)
			}
		}
	}
}

func TestReadAnchorRequiresPrivateRegularFile(t *testing.T) {
	directory := testutil.TempDir(t)
	anchor := filepath.Join(directory, "audit.anchor")
	if err := os.WriteFile(anchor, []byte(strings.Repeat("0", 64)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAnchor(anchor); err == nil {
		t.Fatal("readAnchor accepted permissions broader than 0600")
	}
	if err := os.Chmod(anchor, 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := readAnchor(anchor); err != nil || value != strings.Repeat("0", 64) {
		t.Fatalf("readAnchor() = %q, %v", value, err)
	}
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte(strings.Repeat("0", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(anchor); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := readAnchor(anchor); err == nil {
		t.Fatal("readAnchor accepted a symlink")
	}
}
