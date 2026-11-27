package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/countries/src/testutil"

	"github.com/apimgr/countries/src/scheduler"
)

type recordingDatabaseMetrics struct {
	operations []string
}

func (m *recordingDatabaseMetrics) ObserveDB(operation, table string, _ time.Duration, _ string) {
	m.operations = append(m.operations, operation+":"+table)
}

func TestSchedulerStorePersistsTaskAndBoundsHistory(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	store := NewSchedulerStore(db)
	task := scheduler.Task{ID: "geoip_update", Name: "GeoIP database update", Schedule: "0 3 * * 0", Enabled: true, LastRun: time.Unix(100, 0), NextRun: time.Unix(200, 0), LastStatus: scheduler.StatusSuccess, RunCount: 1}
	if err := store.SaveTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].ID != task.ID || loaded[0].Schedule != task.Schedule || !loaded[0].LastRun.Equal(task.LastRun) || loaded[0].LastStatus != scheduler.StatusSuccess {
		t.Fatalf("loaded tasks = %+v, want persisted task %+v", loaded, task)
	}
	for run := 0; run < 105; run++ {
		started := time.Unix(int64(run+1), 0)
		if err := store.RecordRun(context.Background(), scheduler.RunRecord{TaskID: task.ID, StartedAt: started, FinishedAt: started.Add(time.Second), Status: scheduler.StatusSuccess, Duration: time.Second}); err != nil {
			t.Fatal(err)
		}
	}
	var historyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scheduler_history WHERE task_id = ?`, task.ID).Scan(&historyCount); err != nil {
		t.Fatal(err)
	}
	if historyCount != 100 {
		t.Fatalf("history count = %d, want 100", historyCount)
	}
}

func TestInstrumentedSQLiteReportsQueriesToMetrics(t *testing.T) {
	db, err := Open(filepath.Join(testutil.TempDir(t), "metrics.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	observer := &recordingDatabaseMetrics{}
	SetMetricsObserver(observer)
	defer SetMetricsObserver(nil)
	if _, err := db.Exec(`CREATE TABLE metric_test (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metric_test (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metric_test`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	seenInsert, seenSelect := false, false
	for _, operation := range observer.operations {
		seenInsert = seenInsert || operation == "insert:metric_test"
		seenSelect = seenSelect || operation == "select:metric_test"
	}
	if !seenInsert || !seenSelect {
		t.Fatalf("observed operations = %v, want insert/select for metric_test", observer.operations)
	}
}
