package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	appmode "github.com/apimgr/countries/src/mode"
	"modernc.org/sqlite"
)

// MetricsObserver records database operations without coupling the storage
// package to the server's Prometheus implementation.
type MetricsObserver interface {
	ObserveDB(operation, table string, duration time.Duration, errorType string)
}

var observerState struct {
	sync.RWMutex
	observer MetricsObserver
}

// SetMetricsObserver installs the process-wide observer used by new and
// existing database connections.
func SetMetricsObserver(observer MetricsObserver) {
	observerState.Lock()
	observerState.observer = observer
	observerState.Unlock()
}

type instrumentedDriver struct {
	base driver.Driver
}

func (d *instrumentedDriver) Open(name string) (driver.Conn, error) {
	connection, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return instrumentedConn{Conn: connection}, nil
}

type instrumentedConn struct {
	driver.Conn
}

func (c instrumentedConn) Prepare(query string) (driver.Stmt, error) {
	statement, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return instrumentedStmt{Stmt: statement, query: query}, nil
}

func (c instrumentedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if connection, ok := c.Conn.(driver.ConnPrepareContext); ok {
		statement, err := connection.PrepareContext(ctx, query)
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
	result, err := connection.ExecContext(ctx, query, args)
	recordDatabaseOperation(query, started, err)
	return result, err
}

func (c instrumentedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	connection, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	rows, err := connection.QueryContext(ctx, query, args)
	recordDatabaseOperation(query, started, err)
	return rows, err
}

func (c instrumentedConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	if connection, ok := c.Conn.(driver.ConnBeginTx); ok {
		return connection.BeginTx(ctx, options)
	}
	//lint:ignore SA1019 Legacy-driver fallback; the driver lacks ConnBeginTx.
	return c.Conn.Begin() //nolint:staticcheck
}

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
	if appmode.DebugEnabled() {
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
