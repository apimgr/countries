package app

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/database"
	"github.com/apimgr/countries/src/overlay"
	"github.com/apimgr/countries/src/testutil"
)

func TestServerTimezoneAndStartupOverlayDefaults(t *testing.T) {
	t.Setenv("TZ", "UTC")
	if got := serverTimezone().String(); got != "UTC" {
		t.Fatalf("serverTimezone() = %q, want UTC", got)
	}
	manager := overlay.NewManager(overlay.Options{Config: *config.DefaultConfig()})
	torStatus, i2pStatus := waitForStartupOverlayAddresses(context.Background(), manager, false, false)
	if torStatus.Status != "disabled" || i2pStatus.Status != "disabled" {
		t.Fatalf("disabled overlay statuses = (%+v, %+v)", torStatus, i2pStatus)
	}
}

func TestMaybeEscalateSystemServiceRejectsUnrecognizedCommand(t *testing.T) {
	if maybeEscalateSystemService("not-a-service-command", false) {
		t.Fatal("unrecognized command attempted privilege escalation")
	}
}

// Restore emptiness decides whether an unprivileged restore can overwrite
// current state, so cover absent, fresh, and populated databases.
func TestRestoreDatabaseIsEmpty(t *testing.T) {
	dir := testutil.TempDir(t)

	t.Run("missing database", func(t *testing.T) {
		empty, err := restoreDatabaseIsEmpty(filepath.Join(dir, "missing.db"))
		if err != nil || !empty {
			t.Fatalf("restoreDatabaseIsEmpty(missing) = (%t, %v), want (true, nil)", empty, err)
		}
	})

	t.Run("zero byte database", func(t *testing.T) {
		path := filepath.Join(dir, "zero.db")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		empty, err := restoreDatabaseIsEmpty(path)
		if err != nil || !empty {
			t.Fatalf("restoreDatabaseIsEmpty(zero byte) = (%t, %v), want (true, nil)", empty, err)
		}
	})

	t.Run("fresh schema", func(t *testing.T) {
		path := filepath.Join(dir, "fresh.db")
		db, err := database.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		empty, err := restoreDatabaseIsEmpty(path)
		if err != nil || !empty {
			t.Fatalf("restoreDatabaseIsEmpty(fresh) = (%t, %v), want (true, nil)", empty, err)
		}
	})

	t.Run("owner record protects database", func(t *testing.T) {
		path := filepath.Join(dir, "owned.db")
		db, err := database.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO owners(id) VALUES (?)`, "owner-test"); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		empty, err := restoreDatabaseIsEmpty(path)
		if err != nil || empty {
			t.Fatalf("restoreDatabaseIsEmpty(populated) = (%t, %v), want (false, nil)", empty, err)
		}
	})

	t.Run("unreadable database reports inspection error", func(t *testing.T) {
		path := filepath.Join(dir, "invalid.db")
		if err := os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
			t.Fatal(err)
		}
		if empty, err := restoreDatabaseIsEmpty(path); err == nil || empty {
			t.Fatalf("restoreDatabaseIsEmpty(invalid) = (%t, %v), want (false, error)", empty, err)
		}
	})
}

// Scheduler failures must remain non-fatal when notifications are unavailable;
// a nil error should be a no-op and must not dereference the manager.
func TestNotifySchedulerFailureUnavailableManager(t *testing.T) {
	t.Run("nil manager with failure", func(t *testing.T) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("notifySchedulerFailure panicked with unavailable manager: %v", recovered)
			}
		}()
		notifySchedulerFailure(nil, "unit test task", os.ErrPermission)
	})

	t.Run("nil error is ignored", func(t *testing.T) {
		notifySchedulerFailure(nil, "unit test task", nil)
	})
}

// Update audit events are persisted as JSONL with the update category and
// supplied details, allowing callers to record failures without losing context.
func TestRecordUpdateAudit(t *testing.T) {
	logDir := filepath.Join(testutil.TempDir(t), "logs")
	recordUpdateAudit(logDir, "update.failed", "error", "failure", map[string]any{"version": "v1.2.3"})

	file, err := os.Open(filepath.Join(logDir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close audit log: %v", err)
		}
	})
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("audit log has no event: scan error %v", scanner.Err())
	}
	var event map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
		t.Fatalf("decode audit event: %v", err)
	}
	if event["event"] != "update.failed" || event["category"] != "update" || event["severity"] != "error" || event["result"] != "failure" {
		t.Fatalf("audit event fields = %#v", event)
	}
	details, ok := event["details"].(map[string]any)
	if !ok || details["version"] != "v1.2.3" {
		t.Fatalf("audit details = %#v", event["details"])
	}
	if scanner.Scan() {
		t.Fatalf("unexpected extra audit event: %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreAuthorizationAndPasswordPrompt(t *testing.T) {
	dir := testutil.TempDir(t)
	input, err := os.CreateTemp(dir, "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Errorf("close restore input: %v", err)
		}
	})
	output, err := os.CreateTemp(dir, "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := output.Close(); err != nil {
			t.Errorf("close restore output: %v", err)
		}
	})

	cfg := config.DefaultConfig()
	if err := authorizeRestore(cfg, filepath.Join(dir, "missing.db"), nil, false, input, output); err != nil {
		t.Fatalf("authorize restore into empty installation: %v", err)
	}

	databasePath := filepath.Join(dir, "existing.db")
	db, err := database.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO owners(id) VALUES (?)`, "restore-auth-test"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := authorizeRestore(cfg, databasePath, nil, false, input, output); err == nil || err.Error() != "restoring an existing installation requires an interactive terminal" {
		t.Fatalf("authorize restore into existing installation = %v", err)
	}

	if password, err := promptBackupPassword(filepath.Join(dir, "missing-backup"), input, output); err == nil || password != "" {
		t.Fatalf("prompt missing backup = %q, %v", password, err)
	}
	plainPath := filepath.Join(dir, "plain-backup")
	if err := os.WriteFile(plainPath, []byte("plain archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if password, err := promptBackupPassword(plainPath, input, output); err != nil || password != "" {
		t.Fatalf("prompt unencrypted backup = %q, %v", password, err)
	}
	encryptedPath := filepath.Join(dir, "encrypted-backup")
	if err := os.WriteFile(encryptedPath, []byte("BKUP1"+"payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if password, err := promptBackupPassword(encryptedPath, input, output); err == nil || password != "" {
		t.Fatalf("prompt encrypted backup without a terminal = %q, %v", password, err)
	}
}
