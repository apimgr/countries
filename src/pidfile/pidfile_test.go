package pidfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireAndRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "server.pid")
	if err := Acquire(path, 0o600); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		t.Fatalf("PID file contents = %q, error = %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat PID file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("PID file mode = %v, want 0600", info.Mode().Perm())
	}
	if err := Acquire(path, 0o600); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second Acquire() error = %v, want already running", err)
	}
	if err := Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("PID file still exists: %v", err)
	}
}

func TestAcquireRemovesCorruptAndStalePIDs(t *testing.T) {
	for _, content := range []string{"not-a-pid", "999999999"} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.pid")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Acquire(path, 0o600); err != nil {
				t.Fatalf("Acquire() error = %v", err)
			}
			if err := Remove(path); err != nil {
				t.Fatalf("Remove() error = %v", err)
			}
		})
	}
}
