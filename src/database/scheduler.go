package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/apimgr/countries/src/scheduler"
)

// SchedulerStore persists scheduled task state and bounded execution history.
type SchedulerStore struct {
	db *sql.DB
}

// NewSchedulerStore binds scheduler persistence to the server database.
func NewSchedulerStore(db *sql.DB) *SchedulerStore {
	return &SchedulerStore{db: db}
}

func (s *SchedulerStore) LoadTasks(ctx context.Context) (tasks []scheduler.Task, resultErr error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, enabled, schedule, last_run, next_run, last_status, last_error, run_count, fail_count FROM scheduler_tasks`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	tasks = make([]scheduler.Task, 0)
	for rows.Next() {
		var task scheduler.Task
		var enabled int
		var lastRun, nextRun sql.NullInt64
		var lastStatus, lastError sql.NullString
		if err := rows.Scan(&task.ID, &task.Name, &enabled, &task.Schedule, &lastRun, &nextRun, &lastStatus, &lastError, &task.RunCount, &task.FailCount); err != nil {
			return nil, err
		}
		task.Enabled = enabled != 0
		if lastRun.Valid {
			task.LastRun = time.Unix(lastRun.Int64, 0)
		}
		if nextRun.Valid {
			task.NextRun = time.Unix(nextRun.Int64, 0)
		}
		if lastStatus.Valid {
			task.LastStatus = lastStatus.String
		}
		if lastError.Valid {
			task.LastError = lastError.String
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *SchedulerStore) SaveTask(ctx context.Context, task scheduler.Task) error {
	var lastRun, nextRun interface{}
	if !task.LastRun.IsZero() {
		lastRun = task.LastRun.Unix()
	}
	if !task.NextRun.IsZero() {
		nextRun = task.NextRun.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO scheduler_tasks (id, name, enabled, schedule, last_run, next_run, last_status, last_error, run_count, fail_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, enabled=excluded.enabled, schedule=excluded.schedule, last_run=excluded.last_run, next_run=excluded.next_run, last_status=excluded.last_status, last_error=excluded.last_error, run_count=excluded.run_count, fail_count=excluded.fail_count`, task.ID, task.Name, boolToInt(task.Enabled), task.Schedule, lastRun, nextRun, nullString(task.LastStatus), nullString(task.LastError), task.RunCount, task.FailCount)
	return err
}

func (s *SchedulerStore) RecordRun(ctx context.Context, record scheduler.RunRecord) error {
	var finishedAt interface{}
	if !record.FinishedAt.IsZero() {
		finishedAt = record.FinishedAt.Unix()
	}
	var message interface{}
	if record.Error != "" {
		message = record.Error
	}
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, `INSERT INTO scheduler_history (task_id, started_at, finished_at, status, error, duration_ms) VALUES (?, ?, ?, ?, ?, ?)`, record.TaskID, record.StartedAt.Unix(), finishedAt, record.Status, message, record.Duration.Milliseconds()); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM scheduler_history WHERE task_id = ? AND id NOT IN (SELECT id FROM scheduler_history WHERE task_id = ? ORDER BY started_at DESC, id DESC LIMIT 100)`, record.TaskID, record.TaskID); err != nil {
		return err
	}
	return transaction.Commit()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}
