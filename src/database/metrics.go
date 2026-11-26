package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appmode "github.com/apimgr/countries/src/mode"
	"modernc.org/sqlite"
)

// MetricsObserver records database operations without coupling the storage
// package to the server's Prometheus implementation.
type MetricsObserver interface {
	ObserveDB(operation, table string, duration time.Duration, errorType string)
}

type databaseBusyMetrics interface { RecordDatabaseBusyRetry() }
type databaseWriteWaitMetrics interface { ObserveDatabaseWriteWait(time.Duration) }

func recordBusyRetry() {
	observerState.RLock(); observer := observerState.observer; observerState.RUnlock()
	if metrics, ok := observer.(databaseBusyMetrics); ok { metrics.RecordDatabaseBusyRetry() }
}

func recordWriteWait(duration time.Duration) {
	observerState.RLock(); observer := observerState.observer; observerState.RUnlock()
	if metrics, ok := observer.(databaseWriteWaitMetrics); ok { metrics.ObserveDatabaseWriteWait(duration) }
}

var observerState struct {
	sync.RWMutex
	observer MetricsObserver
}

var debugQueryLogging atomic.Bool

func init() {
	debugQueryLogging.Store(true)
}

// SetDebugQueryLogging enables or disables SQL query logs while debug mode is active.
func SetDebugQueryLogging(enabled bool) {
	debugQueryLogging.Store(enabled)
}

// SetMetricsObserver installs the process-wide observer used by new and
// existing database connections.
func SetMetricsObserver(observer MetricsObserver) {
	observerState.Lock()
	observerState.observer = observer
	observerState.Unlock()
}

type instrumentedDriver struct {
	base    driver.Driver
	rewrite bool
	replica *ReadReplica
}

func (d *instrumentedDriver) Open(name string) (driver.Conn, error) {
	connection, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return instrumentedConn{Conn: connection, rewrite: d.rewrite, replica: d.replica, txActive: new(atomic.Bool)}, nil
}

type instrumentedConnector struct {
	base    driver.Connector
	rewrite bool
	replica *ReadReplica
}

func (c instrumentedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connection, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return instrumentedConn{Conn: connection, rewrite: c.rewrite, replica: c.replica, txActive: new(atomic.Bool)}, nil
}

func (c instrumentedConnector) Driver() driver.Driver {
	return &instrumentedDriver{base: c.base.Driver(), rewrite: c.rewrite, replica: c.replica}
}

type instrumentedConn struct {
	driver.Conn
	rewrite bool
	replica *ReadReplica
	txActive *atomic.Bool
}

func (c instrumentedConn) Prepare(query string) (driver.Stmt, error) {
	statement, err := c.Conn.Prepare(c.rewriteQuery(query))
	if err != nil {
		return nil, err
	}
	return instrumentedStmt{Stmt: statement, query: query}, nil
}

func (c instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if connection, ok := c.Conn.(driver.ConnPrepareContext); ok {
		statement, err := connection.PrepareContext(ctx, c.rewriteQuery(query))
		if err != nil {
			return nil, err
		}
		return instrumentedStmt{Stmt: statement, query: query}, nil
	}
	return c.Prepare(query)
}

func (c instrumentedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	connection, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	result, err := connection.ExecContext(ctx, c.rewriteQuery(query), args)
	recordDatabaseOperation(query, started, err)
	return result, err
}

func (c instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.replica != nil && (c.txActive == nil || !c.txActive.Load()) {
		table := replicaTableForQuery(query)
		if table != "" {
			started := time.Now()
			values := namedValues(args)
			rows, err := c.replica.query(ctx, query, values, table, func() (driverRows, error) {
				connection, ok := c.Conn.(driver.QueryerContext)
				if !ok { return nil, driver.ErrSkip }
				return connection.QueryContext(ctx, c.rewriteQuery(query), args)
			})
			if err == nil {
				// Cache hits and remote reads are both observable database reads;
				// the cache observer records its own hit/miss separately.
				recordDatabaseOperation(query, started, nil)
				return rows, nil
			}
			if !errors.Is(err, driver.ErrSkip) {
				recordDatabaseOperation(query, started, err)
				return nil, err
			}
		}
	}
	connection, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	rows, err := connection.QueryContext(ctx, c.rewriteQuery(query), args)
	recordDatabaseOperation(query, started, err)
	return rows, err
}

func (c instrumentedConn) rewriteQuery(query string) string {
	if !c.rewrite {
		return query
	}
	return rewriteRemoteSQL(query)
}

func (c instrumentedConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if connection, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := connection.BeginTx(ctx, options)
		if err != nil { return nil, err }
		if c.txActive != nil { c.txActive.Store(true) }
		return instrumentedTx{Tx: tx, active: c.txActive}, nil
	}
	//lint:ignore SA1019 Legacy-driver fallback; the driver lacks ConnBeginTx.
	tx, err := c.Conn.Begin() //nolint:staticcheck
	if err != nil { return nil, err }
	if c.txActive != nil { c.txActive.Store(true) }
	return instrumentedTx{Tx: tx, active: c.txActive}, nil
}

type instrumentedTx struct { driver.Tx; active *atomic.Bool }
func (tx instrumentedTx) Commit() error { err := tx.Tx.Commit(); if tx.active != nil { tx.active.Store(false) }; return err }
func (tx instrumentedTx) Rollback() error { err := tx.Tx.Rollback(); if tx.active != nil { tx.active.Store(false) }; return err }

type instrumentedStmt struct {
	driver.Stmt
	query string
}

func (s instrumentedStmt) Exec(args []driver.Value) (driver.Result, error) {
	started := time.Now()
	//lint:ignore SA1019 Legacy-driver fallback; the driver lacks StmtExecContext.
	result, err := s.Stmt.Exec(args) //nolint:staticcheck
	recordDatabaseOperation(s.query, started, err)
	return result, err
}

func (s instrumentedStmt) Query(args []driver.Value) (driver.Rows, error) {
	started := time.Now()
	//lint:ignore SA1019 Legacy-driver fallback; the driver lacks StmtQueryContext.
	rows, err := s.Stmt.Query(args) //nolint:staticcheck
	recordDatabaseOperation(s.query, started, err)
	return rows, err
}

func (s instrumentedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if statement, ok := s.Stmt.(driver.StmtExecContext); ok {
		started := time.Now()
		result, err := statement.ExecContext(ctx, args)
		recordDatabaseOperation(s.query, started, err)
		return result, err
	}
	return nil, driver.ErrSkip
}

func (s instrumentedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if statement, ok := s.Stmt.(driver.StmtQueryContext); ok {
		started := time.Now()
		rows, err := statement.QueryContext(ctx, args)
		recordDatabaseOperation(s.query, started, err)
		return rows, err
	}
	return nil, driver.ErrSkip
}

func recordDatabaseOperation(query string, started time.Time, err error) {
	operation, table := describeDatabaseQuery(query)
	duration := time.Since(started)
	errorType := ""
	if err != nil {
		errorType = classifyDatabaseError(err)
	}
	if appmode.DebugEnabled() && debugQueryLogging.Load() {
		slog.Debug("db query", "query", query, "operation", operation, "table", table, "duration_ms", duration.Milliseconds(), "error_type", errorType)
	}
	observerState.RLock()
	observer := observerState.observer
	observerState.RUnlock()
	if observer == nil {
		return
	}
	observer.ObserveDB(operation, table, duration, errorType)
}

func describeDatabaseQuery(query string) (string, string) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(fields) == 0 {
		return "other", "other"
	}
	operation := fields[0]
	if operation == "with" {
		for _, field := range fields[1:] {
			if field == "select" || field == "insert" || field == "update" || field == "delete" {
				operation = field
				break
			}
		}
	}
	if operation != "select" && operation != "insert" && operation != "update" && operation != "delete" {
		return "other", "other"
	}
	for index, field := range fields {
		if field != "from" && field != "into" && field != "update" && field != "table" {
			continue
		}
		if index+1 >= len(fields) {
			continue
		}
		table := strings.Trim(fields[index+1], "`\"[]();,")
		if separator := strings.LastIndex(table, "."); separator >= 0 {
			table = table[separator+1:]
		}
		if table != "" {
			return operation, table
		}
	}
	return operation, "other"
}

func classifyDatabaseError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "timeout"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "constraint") {
		return "constraint"
	}
	if strings.Contains(message, "unique") || strings.Contains(message, "duplicate") {
		return "duplicate"
	}
	if strings.Contains(message, "connection") || strings.Contains(message, "closed") {
		return "connection"
	}
	return "other"
}

func init() {
	sql.Register("countries-sqlite-instrumented", &instrumentedDriver{base: &sqlite.Driver{}})
}
