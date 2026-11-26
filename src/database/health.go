package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var healthKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

type HealthCheckFunc func(context.Context) string
type HealthStatFunc func() int64

type HealthCheck struct {
	Key      string
	Status   string
	Critical bool
}

type HealthStat struct {
	Key   string
	Value int64
}

type HealthStats struct {
	RequestsTotal int64
	Requests24h   int64
	ActiveConns   int64
	Project       []HealthStat
}

type healthCheckRegistration struct {
	key      string
	critical bool
	check    HealthCheckFunc
}

type healthStatRegistration struct {
	key     string
	value   HealthStatFunc
	current int64
}

// HealthStore owns persistent request counters and cached public health checks.
// Health handlers should only read its snapshots; scheduler jobs refresh and flush it.
type HealthStore struct {
	db *sql.DB

	flushMu       sync.Mutex
	mu            sync.RWMutex
	total         int64
	hourCounts    map[int64]int64
	pendingTotal  int64
	pendingByHour map[int64]int64
	checks        []healthCheckRegistration
	checkResults  []HealthCheck
	stats         []healthStatRegistration
	active        atomic.Int64
}

func NewHealthStore(ctx context.Context, db *sql.DB) (*HealthStore, error) {
	if db == nil {
		return nil, fmt.Errorf("health store requires a database")
	}
	store := &HealthStore{db: db, hourCounts: make(map[int64]int64), pendingByHour: make(map[int64]int64)}
	if err := store.RefreshStats(ctx); err != nil {
		return nil, err
	}
	if err := store.RegisterCheck("database", true, func(ctx context.Context) string {
		for range 2 {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var result int
			err := db.QueryRowContext(probeCtx, "SELECT 1").Scan(&result)
			cancel()
			if err != nil || result != 1 {
				return "error"
			}
		}
		return "ok"
	}); err != nil {
		return nil, err
	}
	store.RefreshChecks(ctx)
	return store, nil
}

// RegisterCheck adds a public-safe check in stable registration order.
func (s *HealthStore) RegisterCheck(key string, critical bool, check HealthCheckFunc) error {
	if !healthKeyPattern.MatchString(key) || check == nil {
		return fmt.Errorf("invalid health check registration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, registered := range s.checks {
		if registered.key == key {
			return fmt.Errorf("health check %q is already registered", key)
		}
	}
	s.checks = append(s.checks, healthCheckRegistration{key: key, critical: critical, check: check})
	s.checkResults = append(s.checkResults, HealthCheck{Key: key, Status: "error", Critical: critical})
	return nil
}

// RegisterStat adds a public aggregate counter in stable registration order.
func (s *HealthStore) RegisterStat(key string, value HealthStatFunc) error {
	if !healthKeyPattern.MatchString(key) || key == "requests_total" || key == "requests_24h" || key == "active_connections" || value == nil || !strings.HasSuffix(key, "_total") && !strings.HasSuffix(key, "_24h") && !strings.HasSuffix(key, "_active") && !strings.HasSuffix(key, "_healthy") {
		return fmt.Errorf("invalid health statistic registration")
	}
	current := value()
	if current < 0 {
		current = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, registered := range s.stats {
		if registered.key == key {
			return fmt.Errorf("health statistic %q is already registered", key)
		}
	}
	s.stats = append(s.stats, healthStatRegistration{key: key, value: value, current: current})
	return nil
}

// RefreshChecks runs registered checks off the request path and stores only safe status values.
func (s *HealthStore) RefreshChecks(ctx context.Context) {
	s.mu.RLock()
	registrations := append([]healthCheckRegistration(nil), s.checks...)
	s.mu.RUnlock()
	results := make([]HealthCheck, 0, len(registrations))
	for _, registered := range registrations {
		status := registered.check(ctx)
		if status != "ok" && status != "degraded" && status != "error" {
			status = "error"
		}
		results = append(results, HealthCheck{Key: registered.key, Status: status, Critical: registered.critical})
	}
	s.mu.Lock()
	s.checkResults = results
	s.mu.Unlock()
}

// Checks returns a copy of the last refreshed health checks.
func (s *HealthStore) Checks() []HealthCheck {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]HealthCheck(nil), s.checkResults...)
}

// RecordRequest updates in-memory counters; persistence is deferred to FlushStats.
func (s *HealthStore) RecordRequest(now time.Time) {
	hour := now.UTC().Unix() / 3600
	s.mu.Lock()
	s.total++
	s.pendingTotal++
	s.hourCounts[hour]++
	s.pendingByHour[hour]++
	s.trimHoursLocked(hour)
	s.mu.Unlock()
}

// EnterRequest and LeaveRequest track requests currently in flight.
func (s *HealthStore) EnterRequest() { s.active.Add(1) }
func (s *HealthStore) LeaveRequest() { s.active.Add(-1) }

// Stats returns cached core and project statistics without database access.
func (s *HealthStore) Stats(now time.Time) HealthStats {
	hour := now.UTC().Unix() / 3600
	s.mu.Lock()
	s.trimHoursLocked(hour)
	total, recent := s.total, int64(0)
	for bucket, count := range s.hourCounts {
		if bucket >= hour-23 && bucket <= hour {
			recent += count
		}
	}
	project := make([]HealthStat, 0, len(s.stats))
	for _, registered := range s.stats {
		project = append(project, HealthStat{Key: registered.key, Value: registered.current})
	}
	s.mu.Unlock()
	active := s.active.Load()
	if active < 1 {
		active = 1
	}
	return HealthStats{RequestsTotal: total, Requests24h: recent, ActiveConns: active, Project: project}
}

// FlushStats batches local request deltas into shared persistent counters.
func (s *HealthStore) FlushStats(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	total := s.pendingTotal
	hours := make(map[int64]int64, len(s.pendingByHour))
	for hour, count := range s.pendingByHour {
		hours[hour] = count
	}
	s.mu.Unlock()
	if total == 0 {
		return nil
	}
	err := Write(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE health_request_stats SET requests_total = requests_total + ? WHERE id = 1`, total); err != nil { return err }
		for hour, count := range hours {
			if _, err := tx.ExecContext(ctx, `INSERT INTO health_request_hours (hour, requests) VALUES (?, ?) ON CONFLICT(hour) DO UPDATE SET requests = requests + excluded.requests`, hour, count); err != nil { return err }
		}
		return nil
	})
	if err != nil { return err }
	s.mu.Lock()
	s.pendingTotal -= total
	for hour, count := range hours {
		s.pendingByHour[hour] -= count
		if s.pendingByHour[hour] <= 0 {
			delete(s.pendingByHour, hour)
		}
	}
	s.mu.Unlock()
	return nil
}

// RefreshStats flushes pending deltas and refreshes the cached cluster-wide totals.
func (s *HealthStore) RefreshStats(ctx context.Context) error {
	if err := s.FlushStats(ctx); err != nil {
		return err
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT requests_total FROM health_request_stats WHERE id = 1`).Scan(&total); err != nil {
		return err
	}
	hour := time.Now().UTC().Unix() / 3600
	rows, err := s.db.QueryContext(ctx, `SELECT hour, requests FROM health_request_hours WHERE hour >= ? AND hour <= ?`, hour-23, hour)
	if err != nil {
		return err
	}
	hours := make(map[int64]int64)
	for rows.Next() {
		var bucket, count int64
		if err := rows.Scan(&bucket, &count); err != nil {
			_ = rows.Close()
			return err
		}
		hours[bucket] = count
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	s.mu.Lock()
	s.total = total
	s.total += s.pendingTotal
	s.hourCounts = hours
	for bucket, count := range s.pendingByHour {
		s.hourCounts[bucket] += count
	}
	s.trimHoursLocked(hour)
	s.mu.Unlock()
	return nil
}

// Refresh flushes stats and updates cached checks as a scheduler task.
func (s *HealthStore) Refresh(ctx context.Context) error {
	if err := s.RefreshStats(ctx); err != nil {
		return err
	}
	s.RefreshChecks(ctx)
	s.RefreshProjectStats()
	return nil
}

// RefreshProjectStats refreshes registered counters outside the request path.
func (s *HealthStore) RefreshProjectStats() {
	s.mu.RLock()
	registrations := append([]healthStatRegistration(nil), s.stats...)
	s.mu.RUnlock()
	for index := range registrations {
		value := registrations[index].value()
		if value < 0 {
			value = 0
		}
		registrations[index].current = value
	}
	s.mu.Lock()
	for index, registration := range registrations {
		if index < len(s.stats) && s.stats[index].key == registration.key {
			s.stats[index].current = registration.current
		}
	}
	s.mu.Unlock()
}

func (s *HealthStore) trimHoursLocked(current int64) {
	for hour := range s.hourCounts {
		if hour < current-23 || hour > current {
			delete(s.hourCounts, hour)
		}
	}
	for hour := range s.pendingByHour {
		if hour < current-23 || hour > current {
			delete(s.pendingByHour, hour)
		}
	}
}
