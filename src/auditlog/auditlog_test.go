package auditlog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	if info.Mode().Perm() != 0640 {
		t.Fatalf("audit log permissions = %04o, want 0640", info.Mode().Perm())
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
	if second["previous_checksum"] != first["checksum"] {
		t.Fatalf("checksum chain = %#v then %#v", first, second)
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
