package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryStore struct {
	mu      sync.Mutex
	tasks   map[string]Task
	entries []RunRecord
}

func (m *memoryStore) LoadTasks(context.Context) ([]Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks := make([]Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (m *memoryStore) SaveTask(_ context.Context, task Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tasks == nil {
		m.tasks = make(map[string]Task)
	}
	task.Func = nil
	m.tasks[task.ID] = task
	return nil
}

func (m *memoryStore) RecordRun(_ context.Context, record RunRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, record)
	return nil
}

func TestRegisterValidatesTaskAndSchedule(t *testing.T) {
	s := New()
	if err := s.Register(Task{Name: "missing-id", Schedule: "@daily", Func: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("Register() should derive id from name: %v", err)
	}
	if err := s.Register(Task{ID: "invalid", Name: "invalid", Schedule: "not a schedule", Func: func(context.Context) error { return nil }}); err == nil {
		t.Fatal("Register() accepted an invalid schedule")
	}
	if err := s.Register(Task{ID: "empty-fn", Name: "empty-fn", Schedule: "@daily"}); err == nil {
		t.Fatal("Register() accepted a nil task function")
	}
}

func TestUpdateConfigurationAppliesTimezoneAndTaskSettings(t *testing.T) {
	scheduler := NewWithOptions(Options{Timezone: "UTC", CatchUpWindow: time.Hour, RetryDelay: time.Minute})
	called := false
	if err := scheduler.Register(Task{
		ID: "sample", Name: "sample", Schedule: "@daily", Enabled: true,
		Func: func(context.Context) error { called = true; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.UpdateConfiguration("Europe/Paris", 2*time.Hour, map[string]TaskConfiguration{
		"sample": {Enabled: false, Schedule: "@hourly", RetryOnFail: true, RetryDelay: 2 * time.Minute},
	}); err != nil {
		t.Fatalf("UpdateConfiguration() error = %v", err)
	}
	tasks := scheduler.GetTasks()
	if len(tasks) != 1 || tasks[0].Enabled || tasks[0].Schedule != "@hourly" || !tasks[0].RetryOnFail || tasks[0].RetryDelay != 2*time.Minute {
		t.Fatalf("updated task = %+v", tasks)
	}
	scheduler.mu.RLock()
	location, catchUpWindow, taskFunc := scheduler.location, scheduler.options.CatchUpWindow, scheduler.findTask("sample").Func
	scheduler.mu.RUnlock()
	if location.String() != "Europe/Paris" || catchUpWindow != 2*time.Hour || taskFunc == nil {
		t.Fatalf("updated scheduler timezone=%v catch-up=%s task function present=%t", location, catchUpWindow, taskFunc != nil)
	}
	if err := taskFunc(context.Background()); err != nil || !called {
		t.Fatalf("existing task function was not preserved: called=%t error=%v", called, err)
	}
	if err := scheduler.UpdateConfiguration("UTC", time.Hour, map[string]TaskConfiguration{"sample": {Schedule: "invalid"}}); err == nil {
		t.Fatal("UpdateConfiguration() accepted an invalid schedule")
	}
	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	if scheduler.location.String() != "Europe/Paris" {
		t.Fatalf("invalid update changed timezone to %q", scheduler.location)
	}
}

func TestNextTimeCronAndAliases(t *testing.T) {
	location := time.UTC
	base := time.Date(2026, time.January, 1, 1, 30, 0, 0, location)
	cases := []struct {
		schedule string
		want     time.Time
	}{
		{"0 2 * * *", time.Date(2026, time.January, 1, 2, 0, 0, 0, location)},
		{"@hourly", time.Date(2026, time.January, 1, 2, 0, 0, 0, location)},
		{"0 0 * * 0", time.Date(2026, time.January, 4, 0, 0, 0, 0, location)},
		{"0 0 * jan sun", time.Date(2026, time.January, 4, 0, 0, 0, 0, location)},
		{"0 0 1 * *", time.Date(2026, time.February, 1, 0, 0, 0, 0, location)},
	}
	for _, test := range cases {
		got, err := NextTime(test.schedule, base, location)
		if err != nil {
			t.Errorf("NextTime(%q) error: %v", test.schedule, err)
			continue
		}
		if !got.Equal(test.want) {
			t.Errorf("NextTime(%q) = %s, want %s", test.schedule, got, test.want)
		}
	}
}

func TestNextTimeEveryAndInvalid(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	got, err := NextTime("@every 5m", base, time.UTC)
	if err != nil || !got.Equal(base.Add(5*time.Minute)) {
		t.Fatalf("NextTime(@every 5m) = %v, %v", got, err)
	}
	for _, schedule := range []string{"@every 0s", "* * *", "60 * * * *", "x * * * *"} {
		if _, err := NextTime(schedule, base, time.UTC); err == nil {
			t.Errorf("NextTime(%q) unexpectedly succeeded", schedule)
		}
	}
}

func TestRunNowRecordsResultAndState(t *testing.T) {
	store := &memoryStore{}
	s := NewWithOptions(Options{Store: store})
	wantErr := errors.New("task failed")
	if err := s.Register(Task{ID: "sample", Name: "sample", Schedule: "@hourly", Enabled: true, Func: func(context.Context) error { return wantErr }}); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := s.RunNow("sample"); !errors.Is(err, wantErr) {
		t.Fatalf("RunNow() error = %v, want %v", err, wantErr)
	}
	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].LastStatus != StatusFailed || tasks[0].LastError != wantErr.Error() || tasks[0].FailCount != 1 || tasks[0].LastRun.Before(before) {
		t.Fatalf("task state after failed run = %+v", tasks)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.entries) != 1 || store.entries[0].Status != StatusFailed || store.entries[0].TaskID != "sample" {
		t.Fatalf("run history = %+v", store.entries)
	}
}

func TestRunNowCatchesPanicAndUnknownTask(t *testing.T) {
	s := New()
	if err := s.Register(Task{ID: "panic", Name: "panic", Schedule: "@hourly", Enabled: true, Func: func(context.Context) error { panic("boom") }}); err != nil {
		t.Fatal(err)
	}
	if err := s.RunNow("panic"); err == nil || err.Error() != "task panicked: boom" {
		t.Fatalf("RunNow() panic error = %v", err)
	}
	if err := s.RunNow("unknown"); err == nil {
		t.Fatal("RunNow() for an unknown task should return an error")
	}
	if got := s.GetTasks()[0].LastStatus; got != StatusFailed {
		t.Fatalf("panic status = %q, want %q", got, StatusFailed)
	}
}

func TestPanickingTaskDoesNotStopSchedulerLoop(t *testing.T) {
	s := NewWithOptions(Options{CheckInterval: time.Millisecond})
	var attempts atomic.Int32
	if err := s.Register(Task{
		ID: "panic-once", Name: "panic-once", Schedule: "@every 1s", Enabled: true,
		NextRun: time.Now().Add(-time.Second),
		Func: func(context.Context) error {
			if attempts.Add(1) == 1 {
				panic("first attempt")
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	s.Start()
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		tasks := s.GetTasks()
		if len(tasks) == 1 && tasks[0].RunCount > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].FailCount != 1 || tasks[0].RunCount == 0 || attempts.Load() < 2 {
		t.Fatalf("task state after panic and recovery = %+v, attempts=%d", tasks, attempts.Load())
	}
}

func TestRestoreUsesStoredStateForRegisteredTasks(t *testing.T) {
	store := &memoryStore{tasks: map[string]Task{
		"known":   {ID: "known", Enabled: false, LastStatus: StatusSuccess, LastRun: time.Unix(10, 0), NextRun: time.Unix(20, 0), RunCount: 7},
		"unknown": {ID: "unknown", Enabled: true},
	}}
	s := NewWithOptions(Options{Store: store})
	if err := s.Register(Task{ID: "known", Name: "known", Schedule: "@daily", Enabled: true, Func: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].Enabled || tasks[0].RunCount != 7 || tasks[0].LastStatus != StatusSuccess {
		t.Fatalf("restored tasks = %+v", tasks)
	}
}

func TestRetriesUseExponentialDelay(t *testing.T) {
	s := NewWithOptions(Options{RetryDelay: time.Minute})
	if err := s.Register(Task{
		ID: "retry", Name: "retry", Schedule: "@hourly", Enabled: true,
		RetryOnFail: true, MaxRetries: 2,
		Func: func(context.Context) error { return errors.New("temporary") },
	}); err != nil {
		t.Fatal(err)
	}
	task := s.tasks["retry"]
	task.NextRun = time.Now().Add(-time.Second)
	if err := s.RunNow("retry"); err == nil {
		t.Fatal("RunNow() should return task failure")
	}
	state := s.GetTasks()[0]
	if state.NextRun.Before(state.LastRun.Add(time.Minute)) || state.NextRun.After(state.LastRun.Add(2*time.Minute)) {
		t.Fatalf("retry next run = %v, expected about one minute after last run %v", state.NextRun, state.LastRun)
	}
}

func TestBoundedConcurrency(t *testing.T) {
	s := NewWithOptions(Options{MaxConcurrent: 2, CheckInterval: time.Millisecond})
	var active, peak atomic.Int32
	release := make(chan struct{})
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("task-%d", i)
		if err := s.Register(Task{ID: id, Name: id, Schedule: "@every 1s", Enabled: true, NextRun: time.Now().Add(-time.Second), Func: func(context.Context) error {
			current := active.Add(1)
			for {
				previous := peak.Load()
				if current <= previous || peak.CompareAndSwap(previous, current) {
					break
				}
			}
			<-release
			active.Add(-1)
			return nil
		}}); err != nil {
			t.Fatal(err)
		}
	}
	s.runDueTasks()
	deadline := time.Now().Add(time.Second)
	for peak.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := peak.Load(); got != 2 {
		close(release)
		t.Fatalf("peak concurrent tasks = %d, want 2", got)
	}
	close(release)
	s.runs.Wait()
}

func TestShutdownWaitsForRunningTask(t *testing.T) {
	s := NewWithOptions(Options{ShutdownTimeout: time.Second})
	started := make(chan struct{})
	finished := make(chan struct{})
	if err := s.Register(Task{ID: "wait", Name: "wait", Schedule: "@hourly", Enabled: true, Func: func(context.Context) error {
		close(started)
		<-finished
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	s.Start()
	runResult := make(chan error, 1)
	go func() { runResult <- s.RunNow("wait") }()
	<-started
	shutdown := make(chan error, 1)
	go func() { shutdown <- s.Shutdown(context.Background()) }()
	close(finished)
	if err := <-shutdown; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := <-runResult; err != nil {
		t.Fatalf("RunNow() error = %v", err)
	}
}

func TestTimezoneFallbackWarnsOnce(t *testing.T) {
	var logs atomic.Int32
	s := NewWithOptions(Options{Timezone: "Not/AZone", DefaultTimezone: "UTC", Logf: func(level, format string, args ...any) {
		if level == "WARN" {
			logs.Add(1)
		}
	}})
	if s.location != time.UTC || logs.Load() != 1 {
		t.Fatalf("timezone=%v warnings=%d", s.location, logs.Load())
	}
}

func TestParseInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"minutely": time.Minute,
		"hourly":   time.Hour,
		"daily":    24 * time.Hour,
		"weekly":   7 * 24 * time.Hour,
		"monthly":  30 * 24 * time.Hour,
		"2h30m":    2*time.Hour + 30*time.Minute,
		"invalid":  24 * time.Hour,
	}
	for input, want := range cases {
		if got := ParseInterval(input); got != want {
			t.Errorf("ParseInterval(%q) = %v, want %v", input, got, want)
		}
	}
}
