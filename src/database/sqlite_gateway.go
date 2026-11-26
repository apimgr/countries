package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type sqliteGatewayConnector struct{ store *SQLiteStore }

var sqliteStores sync.Map

// OpenSQLiteDatabase exposes a compatibility database handle while routing
// reads and writes to the store's separate pools.
func OpenSQLiteDatabase(path string, readers int) (*sql.DB, error) {
	return OpenSQLiteDatabaseWithPool(path, PoolOptions{MaxOpen: readers, MaxIdle: readers, MaxLifetime: 5 * time.Minute, MaxIdleTime: time.Minute})
}

// OpenSQLiteDatabaseWithPool exposes a compatibility handle over the separate SQLite pools.
func OpenSQLiteDatabaseWithPool(path string, pool PoolOptions) (*sql.DB, error) {
	pool = normalizePoolOptions(pool)
	store, err := OpenSQLiteStoreWithPool(path, pool)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(sqliteGatewayConnector{store: store})
	db.SetMaxOpenConns(pool.MaxOpen)
	db.SetMaxIdleConns(pool.MaxIdle)
	db.SetConnMaxLifetime(pool.MaxLifetime)
	db.SetConnMaxIdleTime(pool.MaxIdleTime)
	sqliteStores.Store(db, store)
	return db, nil
}

// Close closes a database handle and its owned SQLite store when applicable.
func Close(db *sql.DB) error {
	if db == nil {
		return nil
	}
	var err error
	if value, ok := replicaRegistry.LoadAndDelete(db); ok {
		if closeErr := value.(*ReadReplica).Close(); err == nil {
			err = closeErr
		}
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if value, ok := sqliteStores.LoadAndDelete(db); ok {
		if closeErr := value.(*SQLiteStore).Close(); err == nil {
			err = closeErr
		}
	}
	return err
}

// Write runs a transaction through the owned SQLite writer or the remote DB.
func Write(ctx context.Context, db *sql.DB, callback func(*sql.Tx) error) error {
	if value, ok := sqliteStores.Load(db); ok {
		return value.(*SQLiteStore).Write(ctx, callback)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := callback(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if value, ok := replicaRegistry.Load(db); ok {
		value.(*ReadReplica).refreshAfterWrite(ctx)
	}
	return nil
}

func (connector sqliteGatewayConnector) Connect(context.Context) (driver.Conn, error) {
	return &sqliteGatewayConn{store: connector.store}, nil
}

func (connector sqliteGatewayConnector) Driver() driver.Driver { return sqliteGatewayDriver{} }

type sqliteGatewayDriver struct{}

func (sqliteGatewayDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("SQLite gateway requires sql.OpenDB")
}

type sqliteGatewayConn struct {
	mu       sync.Mutex
	store    *SQLiteStore
	tx       *sql.Tx
	txCancel context.CancelFunc
	closed   bool
}

func (connection *sqliteGatewayConn) Prepare(query string) (driver.Stmt, error) {
	return connection.PrepareContext(context.Background(), query)
}

func (connection *sqliteGatewayConn) PrepareContext(_ context.Context, query string) (driver.Stmt, error) {
	if connection.closed {
		return nil, driver.ErrBadConn
	}
	return sqliteGatewayStmt{connection: connection, query: query}, nil
}

func (connection *sqliteGatewayConn) Close() error {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	connection.closed = true
	if connection.tx != nil {
		err := connection.tx.Rollback()
		connection.tx = nil
		if connection.txCancel != nil {
			connection.txCancel()
			connection.txCancel = nil
		}
		if !errors.Is(err, sql.ErrTxDone) {
			return err
		}
	}
	return nil
}

func (connection *sqliteGatewayConn) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection *sqliteGatewayConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.closed {
		return nil, driver.ErrBadConn
	}
	if connection.tx != nil {
		return nil, errors.New("transaction already active")
	}
	txCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	tx, err := connection.store.writeDB.BeginTx(txCtx, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	connection.tx = tx
	connection.txCancel = cancel
	return sqliteGatewayTx{connection: connection, tx: tx}, nil
}

func (connection *sqliteGatewayConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if handled, result, err := connection.transactionCommand(ctx, query); handled {
		return result, err
	}
	values := namedValues(args)
	connection.mu.Lock()
	tx := connection.tx
	connection.mu.Unlock()
	if tx != nil {
		return tx.ExecContext(ctx, query, values...)
	}
	var result sql.Result
	err := connection.store.Write(ctx, func(tx *sql.Tx) error {
		var execErr error
		result, execErr = tx.ExecContext(ctx, query, values...)
		return execErr
	})
	return result, err
}

func (connection *sqliteGatewayConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values := namedValues(args)
	connection.mu.Lock()
	tx := connection.tx
	connection.mu.Unlock()
	var rows *sql.Rows
	var err error
	if tx != nil {
		rows, err = tx.QueryContext(ctx, query, values...)
	} else if isWriteStatement(query) {
		var materialized sqliteGatewayRows
		err = connection.store.Write(ctx, func(writeTx *sql.Tx) error {
			resultRows, queryErr := writeTx.QueryContext(ctx, query, values...)
			if queryErr != nil {
				return queryErr
			}
			materialized, queryErr = materializeGatewayRows(resultRows)
			return queryErr
		})
		if err != nil {
			return nil, err
		}
		return &materialized, nil
	} else {
		rows, err = connection.store.readDB.QueryContext(ctx, query, values...)
	}
	if err != nil {
		return nil, err
	}
	columns, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		return nil, err
	}
	return &sqliteGatewayRows{rows: rows, columns: columns}, nil
}

func (connection *sqliteGatewayConn) transactionCommand(ctx context.Context, query string) (bool, driver.Result, error) {
	command := strings.ToUpper(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";")))
	switch command {
	case "BEGIN", "BEGIN IMMEDIATE", "BEGIN DEFERRED", "BEGIN EXCLUSIVE":
		_, err := connection.BeginTx(ctx, driver.TxOptions{})
		return true, sqliteGatewayResult(0), err
	case "COMMIT", "END":
		connection.mu.Lock()
		tx := connection.tx
		connection.tx = nil
		cancel := connection.txCancel
		connection.txCancel = nil
		connection.mu.Unlock()
		if cancel != nil {
			defer cancel()
		}
		if tx == nil {
			return true, nil, errors.New("no transaction is active")
		}
		return true, sqliteGatewayResult(0), tx.Commit()
	case "ROLLBACK":
		connection.mu.Lock()
		tx := connection.tx
		connection.tx = nil
		cancel := connection.txCancel
		connection.txCancel = nil
		connection.mu.Unlock()
		if cancel != nil {
			defer cancel()
		}
		if tx == nil {
			return true, nil, errors.New("no transaction is active")
		}
		return true, sqliteGatewayResult(0), tx.Rollback()
	default:
		return false, nil, nil
	}
}

func namedValues(values []driver.NamedValue) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value.Value
	}
	return result
}

func isWriteStatement(query string) bool {
	tokens := tokenizeSQL(query)
	if len(tokens) == 0 {
		return false
	}
	first := strings.ToLower(query[tokens[0].start:tokens[0].end])
	if first == "with" {
		for _, token := range tokens[1:] {
			value := strings.ToLower(query[token.start:token.end])
			if value == "insert" || value == "update" || value == "delete" || value == "replace" {
				return true
			}
		}
		return false
	}
	switch first {
	case "insert", "update", "delete", "replace", "create", "alter", "drop", "vacuum", "attach", "detach", "reindex", "analyze":
		return true
	case "pragma":
		return strings.Contains(query, "=")
	default:
		return false
	}
}

func materializeGatewayRows(rows *sql.Rows) (result sqliteGatewayRows, resultErr error) {
	defer func() {
		if err := rows.Close(); resultErr == nil && err != nil {
			resultErr = err
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		return sqliteGatewayRows{}, err
	}
	result = sqliteGatewayRows{columns: columns}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return sqliteGatewayRows{}, err
		}
		converted := make([]driver.Value, len(values))
		for index, value := range values {
			if value == nil {
				converted[index] = nil
				continue
			}
			switch typed := value.(type) {
			case int64, float64, bool, []byte, string, time.Time:
				converted[index] = typed
			case int:
				converted[index] = int64(typed)
			default:
				converted[index] = fmt.Sprint(typed)
			}
		}
		result.values = append(result.values, converted)
	}
	return result, rows.Err()
}

type sqliteGatewayStmt struct {
	connection *sqliteGatewayConn
	query      string
}

func (statement sqliteGatewayStmt) Close() error  { return nil }
func (statement sqliteGatewayStmt) NumInput() int { return -1 }
func (statement sqliteGatewayStmt) Exec(values []driver.Value) (driver.Result, error) {
	named := make([]driver.NamedValue, len(values))
	for index, value := range values {
		named[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return statement.connection.ExecContext(context.Background(), statement.query, named)
}
func (statement sqliteGatewayStmt) Query(values []driver.Value) (driver.Rows, error) {
	named := make([]driver.NamedValue, len(values))
	for index, value := range values {
		named[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return statement.connection.QueryContext(context.Background(), statement.query, named)
}
func (statement sqliteGatewayStmt) ExecContext(ctx context.Context, values []driver.NamedValue) (driver.Result, error) {
	return statement.connection.ExecContext(ctx, statement.query, values)
}
func (statement sqliteGatewayStmt) QueryContext(ctx context.Context, values []driver.NamedValue) (driver.Rows, error) {
	return statement.connection.QueryContext(ctx, statement.query, values)
}

type sqliteGatewayTx struct {
	connection *sqliteGatewayConn
	tx         *sql.Tx
}

func (transaction sqliteGatewayTx) Commit() error {
	err := transaction.tx.Commit()
	transaction.connection.mu.Lock()
	transaction.connection.tx = nil
	cancel := transaction.connection.txCancel
	transaction.connection.txCancel = nil
	transaction.connection.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return err
}
func (transaction sqliteGatewayTx) Rollback() error {
	err := transaction.tx.Rollback()
	transaction.connection.mu.Lock()
	transaction.connection.tx = nil
	cancel := transaction.connection.txCancel
	transaction.connection.txCancel = nil
	transaction.connection.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return err
}

type sqliteGatewayRows struct {
	rows    *sql.Rows
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *sqliteGatewayRows) Columns() []string { return rows.columns }
func (rows *sqliteGatewayRows) Close() error {
	if rows.rows == nil {
		return nil
	}
	return rows.rows.Close()
}
func (rows *sqliteGatewayRows) Next(destination []driver.Value) error {
	if rows.rows == nil {
		if rows.index >= len(rows.values) {
			return io.EOF
		}
		copy(destination, rows.values[rows.index])
		rows.index++
		return nil
	}
	if !rows.rows.Next() {
		if err := rows.rows.Err(); err != nil {
			return err
		}
		return io.EOF
	}
	values := make([]any, len(destination))
	pointers := make([]any, len(destination))
	for index := range values {
		pointers[index] = &values[index]
	}
	if err := rows.rows.Scan(pointers...); err != nil {
		return err
	}
	for index, value := range values {
		switch typed := value.(type) {
		case nil, int64, float64, bool, []byte, string, time.Time:
			destination[index] = typed
		case int:
			destination[index] = int64(typed)
		default:
			destination[index] = fmt.Sprint(typed)
		}
	}
	return nil
}

type sqliteGatewayResult int64

func (result sqliteGatewayResult) LastInsertId() (int64, error) { return 0, nil }
func (result sqliteGatewayResult) RowsAffected() (int64, error) { return int64(result), nil }
