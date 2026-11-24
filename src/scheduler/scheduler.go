// Package scheduler runs persistent, bounded recurring application tasks.
package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Task is a scheduled operation. Func is process-local and is not persisted.
type Task struct {
	ID          string
	Name        string
	Schedule    string
	Enabled     bool
	RetryOnFail bool
	RetryDelay  time.Duration
	MaxRetries  int
	LastRun     time.Time
	NextRun     time.Time
	LastStatus  string
	LastError   string
	RunCount    int64
	FailCount   int64
	Func        func(context.Context) error

	interval  time.Duration
	retries   int
	running   bool
	startedAt time.Time
}

// TaskConfiguration contains the operator-editable settings applied on reload.
type TaskConfiguration struct {
	Enabled     bool
	Schedule    string
	RetryOnFail bool
	RetryDelay  time.Duration
}

// RunRecord is the durable history of one task attempt.
type RunRecord struct {
	TaskID     string
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string
	Error      string
	Duration   time.Duration
}

// Store persists task state and execution history in the application's database.
type Store interface {
	LoadTasks(context.Context) ([]Task, error)
	SaveTask(context.Context, Task) error
	RecordRun(context.Context, RunRecord) error
}

// Options controls the scheduler runtime. It has no disabled state: Start always
// starts the control loop, even when there are no registered tasks.
type Options struct {
	Store           Store
	Timezone        string
	DefaultTimezone string
	CatchUpWindow   time.Duration
	CheckInterval   time.Duration
	ShutdownTimeout time.Duration
	MaxConcurrent   int
	MaxRetries      int
	RetryDelay      time.Duration
	Logf            func(level, format string, args ...any)
}

// Scheduler owns task registration, persistence, due-time calculation, and execution.
type Scheduler struct {
	mu          sync.RWMutex
	tasks       map[string]*Task
	options     Options
	location    *time.Location
	controlCtx  context.Context
	stopControl context.CancelFunc
	runCtx      context.Context
	cancelRuns  context.CancelFunc
	loopDone    chan struct{}
	runs        sync.WaitGroup
	semaphore   chan struct{}
	started     bool
	stopping    bool
	stateLoaded bool
	startOnce   sync.Once
}

// New creates a scheduler with safe defaults. Use NewWithOptions to configure
// persistent state, timezone, catch-up, retries, and concurrency.
func New() *Scheduler {
	return NewWithOptions(Options{})
}

// NewWithOptions creates an always-on scheduler. An invalid timezone falls back
// to the configured default and is reported once.
func NewWithOptions(options Options) *Scheduler {
	if options.DefaultTimezone == "" {
		options.DefaultTimezone = "America/New_York"
	}
	if options.Timezone == "" {
		options.Timezone = options.DefaultTimezone
	}
	if options.CatchUpWindow <= 0 {
		options.CatchUpWindow = time.Hour
	}
	if options.CheckInterval <= 0 {
		options.CheckInterval = time.Second
	}
	if options.ShutdownTimeout <= 0 {
		options.ShutdownTimeout = 30 * time.Second
	}
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = 4
	}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	if options.MaxRetries == 0 {
		options.MaxRetries = 3
	}
	if options.MaxRetries > 10 {
		options.MaxRetries = 10
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = 5 * time.Minute
	}
	if options.Logf == nil {
		options.Logf = func(level, format string, args ...any) {
			log.Printf("%s: %s", level, fmt.Sprintf(format, args...))
		}
	}
	location, err := time.LoadLocation(options.Timezone)
	if err != nil {
		options.Logf("WARN", "invalid server.scheduler.timezone %q; using %q", options.Timezone, options.DefaultTimezone)
		location, err = time.LoadLocation(options.DefaultTimezone)
		if err != nil {
			options.Logf("WARN", "invalid default scheduler timezone %q; using UTC", options.DefaultTimezone)
			location = time.UTC
		}
	}
	return &Scheduler{
		tasks:       make(map[string]*Task),
		options:     options,
		location:    location,
		semaphore:   make(chan struct{}, options.MaxConcurrent),
		stateLoaded: options.Store == nil,
	}
}

// AddTask is the compatibility interval API. Invalid intervals and nil
// functions are rejected by leaving the task unregistered.
func (s *Scheduler) AddTask(name string, interval time.Duration, fn func() error) {
	if interval <= 0 || fn == nil {
		s.log("WARN", "scheduler rejected task %q: interval and function are required", name)
		return
	}
	_ = s.Register(Task{
		ID:       name,
		Name:     name,
		Schedule: "@every " + interval.String(),
		Enabled:  true,
		Func: func(context.Context) error {
			return fn()
		},
	})
}

// Register adds or replaces a named task after validating its schedule.
func (s *Scheduler) Register(task Task) error {
	if strings.TrimSpace(task.ID) == "" {
		task.ID = strings.TrimSpace(task.Name)
	}
	if task.ID == "" || strings.TrimSpace(task.Name) == "" || task.Func == nil {
		return fmt.Errorf("scheduler task requires an id, name, and function")
	}
	if task.Schedule == "" && task.interval > 0 {
		task.Schedule = "@every " + task.interval.String()
	}
	if _, err := NextTime(task.Schedule, time.Now(), s.location); err != nil {
		return fmt.Errorf("scheduler task %q schedule: %w", task.ID, err)
	}
	if task.RetryDelay < 0 || task.MaxRetries < 0 || task.MaxRetries > 10 {
		return fmt.Errorf("scheduler task %q retry settings are outside supported bounds", task.ID)
	}
	if task.RetryDelay == 0 {
		task.RetryDelay = s.options.RetryDelay
	}
	if task.MaxRetries == 0 {
		task.MaxRetries = s.options.MaxRetries
	}
	if task.LastStatus == "" {
		task.LastStatus = StatusPending
	}
	if task.NextRun.IsZero() {
		next, err := NextTime(task.Schedule, time.Now(), s.location)
		if err != nil {
			return err
		}
		task.NextRun = next
	}
	copy := task
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return fmt.Errorf("scheduler is stopping")
	}
	if current := s.tasks[task.ID]; current != nil {
		copy.LastRun = current.LastRun
		copy.NextRun = current.NextRun
		copy.LastStatus = current.LastStatus
		copy.LastError = current.LastError
		copy.RunCount = current.RunCount
		copy.FailCount = current.FailCount
		copy.retries = current.retries
		copy.running = current.running
		if current.Schedule != task.Schedule {
			copy.NextRun, _ = NextTime(task.Schedule, time.Now(), s.location)
			copy.retries = 0
		}
	}
	s.tasks[task.ID] = &copy
	loaded := s.stateLoaded
	s.mu.Unlock()
	if loaded {
		return s.persist(copy)
	}
	return nil
}

// UpdateConfiguration applies live scheduler settings and updates registered
// task schedules without replacing their running functions or history.
func (s *Scheduler) UpdateConfiguration(timezone string, catchUpWindow time.Duration, tasks map[string]TaskConfiguration) error {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return fmt.Errorf("scheduler timezone %q: %w", timezone, err)
	}
	if catchUpWindow <= 0 {
		return fmt.Errorf("scheduler catch-up window must be positive")
	}
	for id, settings := range tasks {
		if strings.TrimSpace(settings.Schedule) == "" {
			continue
		}
		if _, err := NextTime(settings.Schedule, time.Now(), location); err != nil {
			return fmt.Errorf("scheduler task %q schedule: %w", id, err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	timezoneChanged := s.location == nil || s.location.String() != timezone
	s.location = location
	s.options.Timezone = timezone
	s.options.CatchUpWindow = catchUpWindow
	for id, settings := range tasks {
		current := s.findTask(id)
		if current == nil {
			continue
		}
		if strings.TrimSpace(settings.Schedule) != "" {
			if timezoneChanged || current.Schedule != settings.Schedule {
				current.Schedule = settings.Schedule
				current.NextRun, _ = NextTime(settings.Schedule, time.Now(), location)
				current.retries = 0
			}
		}
		current.Enabled = settings.Enabled
		current.RetryOnFail = settings.RetryOnFail
		current.RetryDelay = settings.RetryDelay
		if current.RetryDelay <= 0 {
			current.RetryDelay = s.options.RetryDelay
		}
	}
	return nil
}

// Restore loads persisted task state, retaining only tasks registered in this
// process so stored rows cannot execute code that was not registered by the app.
func (s *Scheduler) Restore(ctx context.Context) error {
	if s.options.Store == nil {
		return nil
	}
	stored, err := s.options.Store.LoadTasks(ctx)
	if err != nil {
		return fmt.Errorf("load scheduler tasks: %w", err)
	}
	now := time.Now()
	s.mu.Lock()
	for _, old := range stored {
		current := s.tasks[old.ID]
		if current == nil {
			continue
		}
		current.Enabled = old.Enabled
		current.LastRun = old.LastRun
		current.LastStatus = old.LastStatus
		current.LastError = old.LastError
		current.RunCount = old.RunCount
		current.FailCount = old.FailCount
		if old.Schedule == current.Schedule {
			current.NextRun = old.NextRun
		}
		if current.NextRun.IsZero() || old.Schedule != current.Schedule {
			current.NextRun, err = NextTime(current.Schedule, now, s.location)
			if err != nil {
				s.mu.Unlock()
				return fmt.Errorf("calculate next run for %q: %w", current.ID, err)
			}
		}
	}
	s.stateLoaded = true
	snapshots := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		snapshots = append(snapshots, *task)
	}
	s.mu.Unlock()
	for _, task := range snapshots {
		if err := s.persist(task); err != nil {
			return fmt.Errorf("persist scheduler task %q after restore: %w", task.ID, err)
		}
	}
	return nil
}

// Start starts the scheduler control loop once. Startup persistence errors are
// logged; applications needing startup failure semantics should call Restore
// first and handle its error.
func (s *Scheduler) Start() {
	s.startOnce.Do(func() {
		if err := s.Restore(context.Background()); err != nil {
			s.log("WARN", "scheduler state restore failed: %v", err)
		}
		s.controlCtx, s.stopControl = context.WithCancel(context.Background())
		s.runCtx, s.cancelRuns = context.WithCancel(context.Background())
		s.loopDone = make(chan struct{})
		s.mu.Lock()
		s.started = true
		s.stopping = false
		registered := make([]string, 0, len(s.tasks))
		for _, task := range s.tasks {
			registered = append(registered, task.ID)
		}
		s.mu.Unlock()
		sort.Strings(registered)
		s.log("INFO", "scheduler started with tasks: %s", strings.Join(registered, ", "))
		go s.loop()
	})
}

// StartContext starts the scheduler and stops its control loop when ctx ends.
// Running jobs receive the same shutdown grace period as Shutdown.
func (s *Scheduler) StartContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.Start()
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), s.options.ShutdownTimeout)
			defer cancel()
			_ = s.Shutdown(shutdownCtx)
		case <-s.loopDone:
		}
	}()
}

func (s *Scheduler) loop() {
	defer close(s.loopDone)
	s.runStartupCatchup()
	ticker := time.NewTicker(s.options.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.controlCtx.Done():
			return
		case <-ticker.C:
			s.runDueTasks()
		}
	}
}

func (s *Scheduler) runStartupCatchup() {
	now := time.Now()
	s.mu.RLock()
	var missed []*Task
	var expired []*Task
	for _, task := range s.tasks {
		if task.Enabled && !task.running && !task.NextRun.IsZero() && !task.NextRun.After(now) {
			if now.Sub(task.NextRun) <= s.options.CatchUpWindow {
				missed = append(missed, task)
			} else {
				expired = append(expired, task)
			}
		}
	}
	s.mu.RUnlock()
	for _, task := range expired {
		s.mu.Lock()
		current := s.tasks[task.ID]
		if current != nil && !current.running && !current.NextRun.After(now) {
			if next, err := NextTime(current.Schedule, now, s.location); err == nil {
				current.NextRun = next
			}
			snapshot := *current
			s.mu.Unlock()
			if err := s.persist(snapshot); err != nil {
				s.log("WARN", "scheduler could not persist catch-up state for %q: %v", task.ID, err)
			}
		} else {
			s.mu.Unlock()
		}
	}
	sort.Slice(missed, func(i, j int) bool { return missed[i].NextRun.Before(missed[j].NextRun) })
	for _, task := range missed {
		s.launch(task, now)
	}
}

func (s *Scheduler) runDueTasks() {
	now := time.Now()
	s.mu.RLock()
	var due []*Task
	for _, task := range s.tasks {
		if task.Enabled && !task.running && !task.NextRun.IsZero() && !task.NextRun.After(now) {
			due = append(due, task)
		}
	}
	s.mu.RUnlock()
	sort.Slice(due, func(i, j int) bool { return due[i].NextRun.Before(due[j].NextRun) })
	for _, task := range due {
		s.launch(task, now)
	}
}

func (s *Scheduler) launch(task *Task, now time.Time) {
	select {
	case s.semaphore <- struct{}{}:
	default:
		return
	}
	s.mu.Lock()
	current := s.tasks[task.ID]
	if s.stopping || current == nil || current.running || !current.Enabled || current.NextRun.After(now) {
		s.mu.Unlock()
		<-s.semaphore
		return
	}
	current.running = true
	current.startedAt = now
	current.LastStatus = StatusRunning
	runTask := *current
	s.runs.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.runs.Done()
		defer func() { <-s.semaphore }()
		s.execute(runTask)
	}()
}

func (s *Scheduler) execute(task Task) {
	started := time.Now()
	runCtx := s.runCtx
	if runCtx == nil {
		runCtx = context.Background()
	}
	err := invoke(runCtx, task.Func)
	finished := time.Now()
	status := StatusSuccess
	message := ""
	if err != nil {
		status = StatusFailed
		message = err.Error()
	}
	if s.options.Store != nil {
		storeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if storeErr := s.options.Store.RecordRun(storeCtx, RunRecord{TaskID: task.ID, StartedAt: started, FinishedAt: finished, Status: status, Error: message, Duration: finished.Sub(started)}); storeErr != nil {
			s.log("WARN", "scheduler could not persist run history for %q: %v", task.ID, storeErr)
		}
		cancel()
	}
	s.mu.Lock()
	current := s.tasks[task.ID]
	if current != nil {
		current.running = false
		current.startedAt = time.Time{}
		current.LastRun = finished
		current.LastStatus = status
		current.LastError = message
		if err == nil {
			current.RunCount++
			current.retries = 0
		} else {
			current.FailCount++
			current.retries++
		}
		if err != nil && current.RetryOnFail && current.retries <= current.MaxRetries {
			current.NextRun = finished.Add(current.RetryDelay * time.Duration(1<<(current.retries-1)))
		} else {
			current.retries = 0
			if next, nextErr := NextTime(current.Schedule, finished, s.location); nextErr == nil {
				current.NextRun = next
			} else {
				current.Enabled = false
				current.LastStatus = StatusFailed
				current.LastError = nextErr.Error()
			}
		}
		updated := *current
		s.mu.Unlock()
		if err != nil {
			if task.RetryOnFail && updated.retries > 0 {
				s.log("WARN", "scheduler task %q failed; retry %d/%d scheduled: %v", task.ID, updated.retries, task.MaxRetries, err)
			} else {
				s.log("ERROR", "scheduler task %q failed after retries: %v", task.ID, err)
			}
		} else {
			s.log("DEBUG", "scheduler task %q completed", task.ID)
		}
		if persistErr := s.persist(updated); persistErr != nil {
			s.log("WARN", "scheduler could not persist task %q: %v", task.ID, persistErr)
		}
		return
	}
	s.mu.Unlock()
}

func invoke(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task panicked: %v", recovered)
		}
	}()
	return fn(ctx)
}

// RunNow executes one enabled task immediately and returns its result.
func (s *Scheduler) RunNow(name string) error {
	s.mu.RLock()
	task := s.tasks[name]
	if task == nil {
		for _, candidate := range s.tasks {
			if candidate.Name == name {
				task = candidate
				break
			}
		}
	}
	if task == nil {
		s.mu.RUnlock()
		return fmt.Errorf("scheduler task %q not found", name)
	}
	if !task.Enabled {
		s.mu.RUnlock()
		return fmt.Errorf("scheduler task %q is disabled", name)
	}
	if task.running {
		s.mu.RUnlock()
		return fmt.Errorf("scheduler task %q is already running", name)
	}
	copy := *task
	s.mu.RUnlock()
	select {
	case s.semaphore <- struct{}{}:
	default:
		return fmt.Errorf("scheduler concurrency limit reached")
	}
	s.mu.Lock()
	if current := s.tasks[task.ID]; current == nil || current != task || task.running || !task.Enabled || s.stopping {
		s.mu.Unlock()
		<-s.semaphore
		return fmt.Errorf("scheduler task is unavailable, disabled, already running, or scheduler is stopping")
	}
	task.running = true
	task.startedAt = time.Now()
	task.LastStatus = StatusRunning
	copy = *task
	s.runs.Add(1)
	s.mu.Unlock()
	defer s.runs.Done()
	defer func() { <-s.semaphore }()
	started := time.Now()
	runCtx := s.runCtx
	if runCtx == nil {
		runCtx = context.Background()
	}
	err := invoke(runCtx, copy.Func)
	finished := time.Now()
	status, message := StatusSuccess, ""
	if err != nil {
		status, message = StatusFailed, err.Error()
	}
	s.finishManual(copy, started, finished, status, message)
	return err
}

func (s *Scheduler) finishManual(task Task, started, finished time.Time, status, message string) {
	if s.options.Store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.options.Store.RecordRun(ctx, RunRecord{TaskID: task.ID, StartedAt: started, FinishedAt: finished, Status: status, Error: message, Duration: finished.Sub(started)}); err != nil {
			s.log("WARN", "scheduler could not persist run history for %q: %v", task.ID, err)
		}
		cancel()
	}
	s.mu.Lock()
	current := s.tasks[task.ID]
	if current == nil {
		s.mu.Unlock()
		return
	}
	current.running = false
	current.startedAt = time.Time{}
	current.LastRun = finished
	current.LastStatus = status
	current.LastError = message
	if status == StatusSuccess {
		current.RunCount++
		current.retries = 0
	} else {
		current.FailCount++
	}
	if status == StatusFailed {
		current.retries++
	}
	if status == StatusFailed && current.RetryOnFail && current.retries <= current.MaxRetries {
		current.NextRun = finished.Add(current.RetryDelay * time.Duration(1<<(current.retries-1)))
	} else {
		current.retries = 0
		if next, err := NextTime(current.Schedule, finished, s.location); err == nil {
			current.NextRun = next
		} else {
			current.Enabled = false
			current.LastStatus = StatusFailed
			current.LastError = err.Error()
		}
	}
	updated := *current
	s.mu.Unlock()
	if status == StatusSuccess {
		s.log("DEBUG", "scheduler task %q completed", task.ID)
	} else {
		s.log("WARN", "scheduler task %q failed: %s", task.ID, message)
	}
	if err := s.persist(updated); err != nil {
		s.log("WARN", "scheduler could not persist task %q: %v", task.ID, err)
	}
}

// EnableTask enables a registered task and schedules its next occurrence.
func (s *Scheduler) EnableTask(name string) {
	s.setEnabled(name, true)
}

// DisableTask prevents future executions while allowing an active invocation to finish.
func (s *Scheduler) DisableTask(name string) {
	s.setEnabled(name, false)
}

func (s *Scheduler) setEnabled(name string, enabled bool) {
	s.mu.Lock()
	task := s.findTask(name)
	if task == nil {
		s.mu.Unlock()
		return
	}
	task.Enabled = enabled
	if enabled {
		if next, err := NextTime(task.Schedule, time.Now(), s.location); err == nil {
			task.NextRun = next
		}
	}
	copy := *task
	s.mu.Unlock()
	if err := s.persist(copy); err != nil {
		s.log("WARN", "scheduler could not persist task %q: %v", copy.ID, err)
	}
}

// RemoveTask removes a task by id or name.
func (s *Scheduler) RemoveTask(name string) {
	s.mu.Lock()
	task := s.findTask(name)
	if task != nil && !task.running {
		delete(s.tasks, task.ID)
	}
	s.mu.Unlock()
}

func (s *Scheduler) findTask(name string) *Task {
	if task := s.tasks[name]; task != nil {
		return task
	}
	for _, task := range s.tasks {
		if task.Name == name {
			return task
		}
	}
	return nil
}

// GetTasks returns a sorted snapshot of current task state.
func (s *Scheduler) GetTasks() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		copy := *task
		copy.Func = nil
		copy.running = false
		copy.retries = 0
		tasks = append(tasks, copy)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks
}

// Shutdown stops scheduling new work, waits for active jobs, then cancels
// interrupted jobs at ctx's deadline. Any interrupted tasks remain due so a
// subsequent process can recover them within its catch-up window.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.started || s.stopping {
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	stopControl := s.stopControl
	loopDone := s.loopDone
	cancelRuns := s.cancelRuns
	s.mu.Unlock()
	stopControl()
	select {
	case <-loopDone:
	case <-ctx.Done():
		cancelRuns()
		s.markInterrupted()
		return ctx.Err()
	}
	runsDone := make(chan struct{})
	go func() {
		s.runs.Wait()
		close(runsDone)
	}()
	select {
	case <-runsDone:
		cancelRuns()
		return nil
	case <-ctx.Done():
		cancelRuns()
		s.markInterrupted()
		return ctx.Err()
	}
}

func (s *Scheduler) markInterrupted() {
	now := time.Now()
	s.mu.Lock()
	var interrupted []Task
	for _, task := range s.tasks {
		if !task.running {
			continue
		}
		started := task.startedAt
		if started.IsZero() {
			started = now
		}
		task.startedAt = started
		task.running = false
		task.LastRun = now
		task.LastStatus = StatusFailed
		task.LastError = "interrupted during shutdown"
		task.FailCount++
		task.NextRun = now
		interrupted = append(interrupted, *task)
	}
	s.mu.Unlock()
	for _, task := range interrupted {
		if s.options.Store != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := s.options.Store.RecordRun(ctx, RunRecord{TaskID: task.ID, StartedAt: task.startedAt, FinishedAt: now, Status: StatusFailed, Error: task.LastError, Duration: now.Sub(task.startedAt)}); err != nil {
				s.log("WARN", "scheduler could not persist interrupted task %q: %v", task.ID, err)
			}
			cancel()
		}
		if err := s.persist(task); err != nil {
			s.log("WARN", "scheduler could not persist interrupted task %q: %v", task.ID, err)
		}
	}
}

// Stop stops the scheduler and waits up to the configured shutdown timeout.
func (s *Scheduler) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), s.options.ShutdownTimeout)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		s.log("WARN", "scheduler shutdown did not finish cleanly: %v", err)
	}
}

func (s *Scheduler) persist(task Task) error {
	if s.options.Store == nil || !s.stateIsLoaded() {
		return nil
	}
	task.Func = nil
	task.interval = 0
	task.retries = 0
	task.running = false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.options.Store.SaveTask(ctx, task)
}

func (s *Scheduler) stateIsLoaded() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stateLoaded
}

func (s *Scheduler) log(level, format string, args ...any) {
	s.options.Logf(level, format, args...)
}

// NextTime returns the first scheduled time strictly after after. Schedules
// accept standard five-field cron syntax, common @ aliases, and @every durations.
func NextTime(schedule string, after time.Time, location *time.Location) (time.Time, error) {
	schedule = strings.TrimSpace(schedule)
	if location == nil {
		location = time.UTC
	}
	if strings.HasPrefix(schedule, "@every ") {
		duration, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(schedule, "@every ")))
		if err != nil || duration < time.Second {
			return time.Time{}, fmt.Errorf("@every requires a duration of at least one second")
		}
		return after.Add(duration), nil
	}
	aliases := map[string]string{
		"@hourly":   "0 * * * *",
		"@daily":    "0 0 * * *",
		"@midnight": "0 0 * * *",
		"@weekly":   "0 0 * * 0",
		"@monthly":  "0 0 1 * *",
	}
	if expanded, ok := aliases[schedule]; ok {
		schedule = expanded
	}
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("schedule must be a five-field cron expression or supported @ form")
	}
	minute, err := parseField(fields[0], 0, 59, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("hour: %w", err)
	}
	day, err := parseField(fields[2], 1, 31, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("day: %w", err)
	}
	month, err := parseField(fields[3], 1, 12, map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("month: %w", err)
	}
	weekday, err := parseField(fields[4], 0, 7, map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("weekday: %w", err)
	}
	if weekday[7] {
		weekday[0] = true
	}
	start := after.In(location).Truncate(time.Minute).Add(time.Minute)
	limit := start.AddDate(5, 0, 0)
	for candidate := start; candidate.Before(limit); candidate = candidate.Add(time.Minute) {
		if !minute[candidate.Minute()] || !hour[candidate.Hour()] || !month[int(candidate.Month())] {
			continue
		}
		dayMatch := day[candidate.Day()]
		weekdayMatch := weekday[int(candidate.Weekday())]
		dayOfMonthWildcard := fields[2] == "*"
		dayOfWeekWildcard := fields[4] == "*"
		matchesDay := dayMatch && weekdayMatch
		if !dayOfMonthWildcard && !dayOfWeekWildcard {
			matchesDay = dayMatch || weekdayMatch
		}
		if matchesDay {
			return candidate, nil
		}
	}
	return time.Time{}, fmt.Errorf("schedule has no occurrence in the next five years")
}

func parseField(value string, min, max int, names map[string]int) (map[int]bool, error) {
	selected := make(map[int]bool)
	for _, part := range strings.Split(value, ",") {
		step := 1
		base := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return nil, fmt.Errorf("invalid step %q", part)
			}
			parsed, err := strconv.Atoi(pieces[1])
			if err != nil || parsed < 1 {
				return nil, fmt.Errorf("invalid step %q", part)
			}
			step, base = parsed, pieces[0]
		}
		low, high := min, max
		if base != "*" {
			if strings.Contains(base, "-") {
				bounds := strings.Split(base, "-")
				if len(bounds) != 2 {
					return nil, fmt.Errorf("invalid range %q", base)
				}
				var err error
				low, err = parseValue(bounds[0], names)
				if err != nil {
					return nil, fmt.Errorf("invalid value %q", bounds[0])
				}
				high, err = parseValue(bounds[1], names)
				if err != nil {
					return nil, fmt.Errorf("invalid value %q", bounds[1])
				}
			} else {
				parsed, err := parseValue(base, names)
				if err != nil {
					return nil, fmt.Errorf("invalid value %q", base)
				}
				low, high = parsed, parsed
			}
		}
		if low < min || high > max || low > high {
			return nil, fmt.Errorf("value %q outside %d..%d", base, min, max)
		}
		for item := low; item <= high; item += step {
			selected[item] = true
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("empty field")
	}
	return selected, nil
}

func parseValue(value string, names map[string]int) (int, error) {
	if number, err := strconv.Atoi(value); err == nil {
		return number, nil
	}
	if number, ok := names[strings.ToLower(value)]; ok {
		return number, nil
	}
	return 0, fmt.Errorf("not a numeric or named value")
}

// ParseInterval preserves the prior helper, returning the previous daily
// fallback for invalid input. New task registration should validate schedules.
func ParseInterval(value string) time.Duration {
	switch value {
	case "minutely":
		return time.Minute
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "weekly":
		return 7 * 24 * time.Hour
	case "monthly":
		return 30 * 24 * time.Hour
	default:
		if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
			return duration
		}
		return 24 * time.Hour
	}
}
