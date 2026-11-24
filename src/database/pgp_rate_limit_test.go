package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
)

func TestClaimPGPPrivateExportRateLimit(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	first := time.Date(2026, time.November, 22, 12, 0, 0, 0, time.UTC)
	allowed, retryAfter, err := ClaimPGPPrivateExport(context.Background(), db, "root", first)
	if err != nil || !allowed || retryAfter != 0 {
		t.Fatalf("first export claim = (%v, %s, %v), want (true, 0, nil)", allowed, retryAfter, err)
	}
	allowed, retryAfter, err = ClaimPGPPrivateExport(context.Background(), db, "root", first.Add(time.Minute))
	if err != nil || allowed || retryAfter != 59*time.Minute {
		t.Fatalf("early export claim = (%v, %s, %v), want (false, 59m, nil)", allowed, retryAfter, err)
	}
	allowed, retryAfter, err = ClaimPGPPrivateExport(context.Background(), db, "root", first.Add(time.Hour))
	if err != nil || !allowed || retryAfter != 0 {
		t.Fatalf("expired export claim = (%v, %s, %v), want (true, 0, nil)", allowed, retryAfter, err)
	}
}
