package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"
)

func TestHealthStorePersistsRequestCounters(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	store, err := NewHealthStore(ctx, db)
	if err != nil {
		t.Fatalf("NewHealthStore() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	store.RecordRequest(now)
	store.RecordRequest(now)
	beforeFlush := store.Stats(now)
	if beforeFlush.RequestsTotal != 2 || beforeFlush.Requests24h != 2 {
		t.Fatalf("in-memory stats = (%d, %d), want (2, 2)", beforeFlush.RequestsTotal, beforeFlush.Requests24h)
	}
	if err := store.FlushStats(ctx); err != nil {
		t.Fatalf("FlushStats() error = %v", err)
	}

	restarted, err := NewHealthStore(ctx, db)
	if err != nil {
		t.Fatalf("NewHealthStore() after flush error = %v", err)
	}
	afterRestart := restarted.Stats(now)
	if afterRestart.RequestsTotal != 2 || afterRestart.Requests24h != 2 {
		t.Fatalf("persisted stats = (%d, %d), want (2, 2)", afterRestart.RequestsTotal, afterRestart.Requests24h)
	}
}

func TestHealthStoreChecksAreCachedAndOrdered(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store, err := NewHealthStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewHealthStore() error = %v", err)
	}
	called := 0
	if err := store.RegisterCheck("data_alpha", true, func(context.Context) string {
		called++
		return "ok"
	}); err != nil {
		t.Fatalf("RegisterCheck() error = %v", err)
	}
	if err := store.RegisterCheck("geoip_country", false, func(context.Context) string { return "degraded" }); err != nil {
		t.Fatalf("RegisterCheck() error = %v", err)
	}
	if called != 0 {
		t.Fatalf("check ran during registration, calls = %d", called)
	}
	store.RefreshChecks(context.Background())
	checks := store.Checks()
	if called != 1 || len(checks) != 3 {
		t.Fatalf("checks = %#v, callback calls = %d", checks, called)
	}
	if checks[0].Key != "database" || checks[1].Key != "data_alpha" || checks[1].Status != "ok" || !checks[1].Critical || checks[2].Key != "geoip_country" || checks[2].Status != "degraded" {
		t.Fatalf("checks = %#v, want registered order and safe statuses", checks)
	}
}

func TestHealthStoreRejectsUnsafeOrDuplicateRegistrations(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store, err := NewHealthStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewHealthStore() error = %v", err)
	}
	check := func(context.Context) string { return "ok" }
	if err := store.RegisterCheck("bad/path", false, check); err == nil {
		t.Fatal("RegisterCheck accepted an unsafe key")
	}
	if err := store.RegisterCheck("data_alpha", false, check); err != nil {
		t.Fatalf("RegisterCheck() error = %v", err)
	}
	if err := store.RegisterCheck("data_alpha", false, check); err == nil {
		t.Fatal("RegisterCheck accepted a duplicate key")
	}
}

func TestHealthStoreProjectStatsAreCached(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store, err := NewHealthStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewHealthStore() error = %v", err)
	}
	value := int64(3)
	if err := store.RegisterStat("searches_total", func() int64 { return value }); err != nil {
		t.Fatalf("RegisterStat() error = %v", err)
	}
	value = 8
	if got := store.Stats(time.Now()).Project[0].Value; got != 3 {
		t.Fatalf("cached project stat = %d, want 3 before refresh", got)
	}
	store.RefreshProjectStats()
	if got := store.Stats(time.Now()).Project[0].Value; got != 8 {
		t.Fatalf("cached project stat = %d after refresh, want 8", got)
	}
}
