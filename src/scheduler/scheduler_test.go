package scheduler

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestAddAndGetTasks(t *testing.T) {
	s := New()
	s.AddTask("noop", time.Hour, func() error { return nil })

	tasks := s.GetTasks()
	if len(tasks) != 1 || tasks[0].Name != "noop" {
		t.Fatalf("GetTasks() = %+v, want one task named noop", tasks)
	}
	if !tasks[0].Enabled {
		t.Error("AddTask() should register the task as enabled by default")
	}
}

func TestRemoveTask(t *testing.T) {
	s := New()
	s.AddTask("noop", time.Hour, func() error { return nil })
	s.RemoveTask("noop")

	if tasks := s.GetTasks(); len(tasks) != 0 {
		t.Errorf("GetTasks() after RemoveTask() = %+v, want empty", tasks)
	}

	// Removing a task that doesn't exist must be a safe no-op.
	s.RemoveTask("nonexistent")
}

func TestEnableDisableTask(t *testing.T) {
	s := New()
	s.AddTask("noop", time.Hour, func() error { return nil })

	s.DisableTask("noop")
	tasks := s.GetTasks()
	if tasks[0].Enabled {
		t.Error("DisableTask() should mark the task disabled")
	}

	s.EnableTask("noop")
	tasks = s.GetTasks()
	if !tasks[0].Enabled {
		t.Error("EnableTask() should mark the task enabled")
	}

	// Enabling/disabling an unknown task must be a safe no-op.
	s.EnableTask("nonexistent")
	s.DisableTask("nonexistent")
}

func TestRunNow_Success(t *testing.T) {
	s := New()
	var calls int32
	s.AddTask("counter", time.Hour, func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	})

	if err := s.RunNow("counter"); err != nil {
		t.Fatalf("RunNow() unexpected error: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("task ran %d times, want 1", calls)
	}
}

// RunNow must propagate the task function's own error.
func TestRunNow_PropagatesTaskError(t *testing.T) {
	s := New()
	wantErr := errors.New("task failed")
	s.AddTask("failing", time.Hour, func() error { return wantErr })

	if err := s.RunNow("failing"); !errors.Is(err, wantErr) {
		t.Errorf("RunNow() error = %v, want %v", err, wantErr)
	}
}

// RunNow on a task that was never registered returns nil rather than an
// error — documented quirk of this implementation, verified here so a
// future refactor doesn't silently change the contract.
func TestRunNow_UnknownTaskReturnsNil(t *testing.T) {
	s := New()
	if err := s.RunNow("nonexistent"); err != nil {
		t.Errorf("RunNow(nonexistent) = %v, want nil", err)
	}
}

// RunNow must update LastRun/NextRun after execution.
func TestRunNow_UpdatesRunTimes(t *testing.T) {
	s := New()
	s.AddTask("noop", time.Minute, func() error { return nil })

	before := time.Now()
	if err := s.RunNow("noop"); err != nil {
		t.Fatalf("RunNow() error: %v", err)
	}

	tasks := s.GetTasks()
	if tasks[0].LastRun.Before(before) {
		t.Error("RunNow() should set LastRun to a time at or after the call")
	}
	if !tasks[0].NextRun.After(tasks[0].LastRun) {
		t.Error("RunNow() should set NextRun after LastRun")
	}
}

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"minutely", time.Minute},
		{"hourly", time.Hour},
		{"daily", 24 * time.Hour},
		{"weekly", 7 * 24 * time.Hour},
		{"monthly", 30 * 24 * time.Hour},
		{"2h30m", 2*time.Hour + 30*time.Minute},
		// Unparseable strings fall back to daily.
		{"not-a-duration", 24 * time.Hour},
		{"", 24 * time.Hour},
	}

	for _, c := range cases {
		if got := ParseInterval(c.in); got != c.want {
			t.Errorf("ParseInterval(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Start must be idempotent — calling it twice should not spawn a second
// ticker goroutine or panic. Stop must likewise be safe to call when not
// running, and safe after a real Start.
func TestStartStopIdempotent(t *testing.T) {
	s := New()
	s.Start()
	s.Start()
	s.Stop()
	s.Stop()
}

// runDueTasks is the scheduler's internal tick handler; call it directly
// (rather than waiting on Start's 30-second ticker) to verify it runs only
// tasks that are both enabled and past their NextRun, and updates
// LastRun/NextRun afterward. Task functions run in their own goroutine, so
// poll briefly for the side effect instead of asserting immediately.
func TestRunDueTasks(t *testing.T) {
	s := New()
	var dueCalls, notDueCalls, disabledCalls int32

	s.AddTask("due", time.Minute, func() error {
		atomic.AddInt32(&dueCalls, 1)
		return nil
	})
	// Force the "due" task's NextRun into the past.
	s.mu.Lock()
	s.tasks["due"].NextRun = time.Now().Add(-time.Minute)
	s.mu.Unlock()

	s.AddTask("not-due", time.Hour, func() error {
		atomic.AddInt32(&notDueCalls, 1)
		return nil
	})

	s.AddTask("disabled", time.Minute, func() error {
		atomic.AddInt32(&disabledCalls, 1)
		return nil
	})
	s.mu.Lock()
	s.tasks["disabled"].NextRun = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	s.DisableTask("disabled")

	s.runDueTasks()

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&dueCalls) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if atomic.LoadInt32(&dueCalls) != 1 {
		t.Errorf("due task ran %d times, want 1", dueCalls)
	}
	if atomic.LoadInt32(&notDueCalls) != 0 {
		t.Errorf("not-due task ran %d times, want 0", notDueCalls)
	}
	if atomic.LoadInt32(&disabledCalls) != 0 {
		t.Errorf("disabled task ran %d times, want 0", disabledCalls)
	}

	tasks := s.GetTasks()
	for _, task := range tasks {
		if task.Name != "due" {
			continue
		}
		if !task.NextRun.After(task.LastRun) {
			t.Errorf("due task NextRun = %v, want after LastRun = %v", task.NextRun, task.LastRun)
		}
	}
}

// A task function that returns an error must still update LastRun/NextRun
// and must not crash the scheduler's background goroutine.
func TestRunDueTasks_TaskErrorDoesNotStopScheduler(t *testing.T) {
	s := New()
	s.AddTask("failing", time.Minute, func() error {
		return errors.New("boom")
	})
	s.mu.Lock()
	s.tasks["failing"].NextRun = time.Now().Add(-time.Minute)
	s.mu.Unlock()

	s.runDueTasks()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		lastRun := s.tasks["failing"].LastRun
		s.mu.RUnlock()
		if !lastRun.IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("failing task's LastRun was never updated after runDueTasks")
}
