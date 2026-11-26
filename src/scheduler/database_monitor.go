package scheduler

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"
)

var databaseCredentialURL = regexp.MustCompile(`(?i)(://)[^/@\s]+:[^/@\s]+@`)
var databaseCredentialQuery = regexp.MustCompile(`(?i)((?:auth[_-]?)?(?:token|password|passwd|secret|key)\s*[=:]\s*)[^&#\s,;]+`)

// DatabaseMonitor applies the consecutive-failure and repeat-notification
// policy for the database_check task.
type DatabaseMonitor struct {
	Failures int
	Repeat   time.Duration
	Probe    func(context.Context) error
	Notify   func(since time.Time, message string)
	Logf     func(level, format string, args ...any)

	mu             sync.Mutex
	failureCount   int
	firstFailureAt time.Time
	lastNotifiedAt time.Time
}

// Check probes the database and emits threshold and repeat notifications.
func (m *DatabaseMonitor) Check(ctx context.Context) error {
	if m == nil || m.Probe == nil {
		return fmt.Errorf("database probe is unavailable")
	}
	err := m.Probe(ctx)
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		if m.failureCount > 0 {
			m.log("INFO", "Database connection recovered")
		}
		m.failureCount = 0
		m.firstFailureAt = time.Time{}
		m.lastNotifiedAt = time.Time{}
		return nil
	}
	m.failureCount++
	if m.failureCount == 1 {
		m.firstFailureAt = now
	}
	threshold := m.Failures
	if threshold < 1 {
		threshold = 3
	}
	if m.failureCount < threshold {
		return err
	}
	repeat := m.Repeat
	if repeat <= 0 {
		repeat = 24 * time.Hour
	}
	if m.lastNotifiedAt.IsZero() || now.Sub(m.lastNotifiedAt) >= repeat {
		message := sanitizeDatabaseError(err.Error())
		m.log("WARN", "database connection probe failed since %s: %s", m.firstFailureAt.Format(time.RFC3339), message)
		if m.Notify != nil {
			m.Notify(m.firstFailureAt, message)
		}
		m.lastNotifiedAt = now
	}
	return err
}

func (m *DatabaseMonitor) State() (int, time.Time) {
	if m == nil {
		return 0, time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.failureCount, m.firstFailureAt
}

func (m *DatabaseMonitor) log(level, format string, args ...any) {
	if m.Logf != nil {
		m.Logf(level, format, args...)
	}
}

func sanitizeDatabaseError(message string) string {
	message = databaseCredentialURL.ReplaceAllString(message, `${1}[redacted]@`)
	message = databaseCredentialQuery.ReplaceAllString(message, `${1}[redacted]`)
	return message
}
