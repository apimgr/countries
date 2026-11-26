package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ReplicaOptions struct {
	Enabled      bool
	Path         string
	SyncInterval string
	OfflineMode  bool
	MaxAge       string
}

type ReadReplica struct {
	store    *SQLiteStore
	remote   *sql.DB
	interval time.Duration
	maxAge   time.Duration
	offline  bool
	mu       sync.RWMutex
	dirty    map[string]bool
	updated  map[string]time.Time
	stop     chan struct{}
	done     chan struct{}
	started  bool
	ready    bool
	warnOnce sync.Once
}

type replicaSnapshot struct {
	columns []string
	records [][]any
}

var replicaRegistry sync.Map

var replicaTables = []string{
	"owners", "api_tokens", "app_secrets", "audit_log", "backups", "captcha_challenges", "config", "config_meta",
	"health_request_hours", "health_request_stats", "pgp_keys", "rate_limits", "scheduler_history", "scheduler_tasks",
	"security_reports", "security_thanks",
}

var replicaPrimaryKeys = map[string][]string{
	"owners": {"id"}, "api_tokens": {"id"}, "app_secrets": {"name"}, "audit_log": {"id"},
	"backups": {"id"}, "captcha_challenges": {"id"}, "config": {"key"}, "config_meta": {"id"},
	"health_request_hours": {"hour"}, "health_request_stats": {"id"}, "pgp_keys": {"fingerprint"},
	"rate_limits": {"key"}, "scheduler_history": {"id"}, "scheduler_tasks": {"id"},
	"security_reports": {"tracking_id"}, "security_thanks": {"tracking_id"},
}

func OpenReadReplica(path string, options ReplicaOptions) (*ReadReplica, error) {
	interval, err := time.ParseDuration(options.SyncInterval)
	if err != nil || interval <= 0 {
		interval = 30 * time.Second
	}
	maxAge, err := time.ParseDuration(options.MaxAge)
	if err != nil || maxAge <= 0 {
		maxAge = time.Hour
	}
	store, err := OpenSQLiteStore(path, 4)
	if err != nil {
		return nil, err
	}
	replica := &ReadReplica{store: store, interval: interval, maxAge: maxAge, offline: options.OfflineMode, dirty: make(map[string]bool), updated: make(map[string]time.Time), stop: make(chan struct{}), done: make(chan struct{})}
	if err := store.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE IF NOT EXISTS replica_state (table_name TEXT PRIMARY KEY, updated_at INTEGER NOT NULL, dirty INTEGER NOT NULL DEFAULT 0)")
		return err
	}); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("initialize replica state: %w", err)
	}
	rows, err := store.readDB.Query("SELECT table_name, updated_at, dirty FROM replica_state")
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	for rows.Next() {
		var table string
		var updated int64
		var dirty bool
		if err := rows.Scan(&table, &updated, &dirty); err != nil {
			_ = rows.Close()
			_ = store.Close()
			return nil, err
		}
		replica.updated[table] = time.Unix(updated, 0)
		if dirty {
			replica.dirty[table] = true
		}
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return replica, nil
}

func (replica *ReadReplica) SetRemote(remote *sql.DB) { replica.remote = remote }
func (replica *ReadReplica) SetReady()                { replica.mu.Lock(); replica.ready = true; replica.mu.Unlock() }

func (replica *ReadReplica) Start() {
	replica.mu.Lock()
	if replica.started {
		replica.mu.Unlock()
		return
	}
	replica.started = true
	replica.mu.Unlock()
	go func() {
		defer close(replica.done)
		ticker := time.NewTicker(replica.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), replica.interval)
				if err := replica.Synchronize(ctx); err != nil {
					replica.warn(err)
				}
				cancel()
			case <-replica.stop:
				return
			}
		}
	}()
}

func (replica *ReadReplica) Close() error {
	if replica == nil {
		return nil
	}
	select {
	case <-replica.stop:
	default:
		close(replica.stop)
	}
	replica.mu.RLock()
	started := replica.started
	replica.mu.RUnlock()
	if started {
		<-replica.done
	}
	return replica.store.Close()
}

func (replica *ReadReplica) warn(err error) {
	replica.warnOnce.Do(func() { slog.Warn("remote database read cache is stale", "error", err) })
}

func (replica *ReadReplica) MarkAllDirty() error {
	err := replica.store.Write(context.Background(), func(tx *sql.Tx) error {
		for _, table := range replicaTables {
			if _, err := tx.Exec("INSERT INTO replica_state(table_name,updated_at,dirty) VALUES(?,0,1) ON CONFLICT(table_name) DO UPDATE SET dirty=1", table); err != nil {
				return err
			}
		}
		return nil
	})
	replica.mu.Lock()
	for _, table := range replicaTables {
		replica.dirty[table] = true
	}
	replica.mu.Unlock()
	return err
}

func (replica *ReadReplica) cached(ctx context.Context, query string, args []any, table string) (*sqliteGatewayRows, error) {
	if table == "" {
		recordCacheMetric(false)
		return nil, errors.New("query is not eligible for replica reads")
	}
	replica.mu.RLock()
	dirty := replica.dirty[table]
	updated := replica.updated[table]
	replica.mu.RUnlock()
	if dirty || updated.IsZero() || time.Since(updated) > replica.maxAge {
		recordCacheMetric(false)
		return nil, errors.New("replica table is stale")
	}
	rows, err := replica.store.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		recordCacheMetric(false)
		return nil, err
	}
	materialized, err := materializeGatewayRows(rows)
	if err != nil {
		recordCacheMetric(false)
		return nil, err
	}
	recordCacheMetric(true)
	return &materialized, nil
}

func (replica *ReadReplica) query(ctx context.Context, query string, args []any, table string, remote func() (driverRows, error)) (driverRows, error) {
	replica.mu.RLock()
	ready := replica.ready
	replica.mu.RUnlock()
	if !ready {
		return remote()
	}
	if rows, err := replica.cached(ctx, query, args, table); err == nil {
		return rows, nil
	}
	rows, err := remote()
	if err == nil {
		return rows, nil
	}
	if !replica.offline {
		return nil, err
	}
	if cached, cacheErr := replica.cached(ctx, query, args, table); cacheErr == nil {
		return cached, nil
	}
	return nil, err
}

// Synchronize atomically replaces every cached table from the authoritative remote database.
func (replica *ReadReplica) Synchronize(ctx context.Context) error {
	if replica == nil || replica.remote == nil {
		return errors.New("replica has no remote database")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	remoteTx, err := replica.remote.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = remoteTx.Rollback() }()
	snapshots := make(map[string]replicaSnapshot, len(replicaTables))
	for _, table := range replicaTables {
		remoteName := DatabaseIdentifier("libsql", table)
		rows, queryErr := remoteTx.QueryContext(ctx, "SELECT * FROM \""+remoteName+"\"")
		if queryErr != nil {
			return fmt.Errorf("read remote replica table %s: %w", table, queryErr)
		}
		columns, colErr := rows.Columns()
		if colErr != nil {
			_ = rows.Close()
			return colErr
		}
		data := replicaSnapshot{columns: columns}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if scanErr := rows.Scan(pointers...); scanErr != nil {
				_ = rows.Close()
				return scanErr
			}
			for i, value := range values {
				if bytes, ok := value.([]byte); ok {
					values[i] = append([]byte(nil), bytes...)
				}
			}
			data.records = append(data.records, values)
		}
		rowsErr := rows.Err()
		if closeErr := rows.Close(); rowsErr == nil {
			rowsErr = closeErr
		}
		if rowsErr != nil {
			return rowsErr
		}
		snapshots[table] = data
	}
	err = replica.store.Write(ctx, func(localTx *sql.Tx) error {
		// Remove remote-deleted child rows before parents to respect the replica's
		// foreign keys. A table is only changed when its primary-keyed contents differ.
		for _, table := range []string{"api_tokens", "security_reports", "security_thanks", "scheduler_history", "scheduler_tasks", "owners", "app_secrets", "audit_log", "backups", "captcha_challenges", "config", "config_meta", "health_request_hours", "health_request_stats", "pgp_keys", "rate_limits"} {
			if err := reconcileReplicaTable(ctx, localTx, table, snapshots[table]); err != nil {
				return err
			}
		}
		now := time.Now().Unix()
		for _, table := range replicaTables {
			if _, err := localTx.ExecContext(ctx, "INSERT INTO replica_state(table_name,updated_at,dirty) VALUES(?,?,0) ON CONFLICT(table_name) DO UPDATE SET updated_at=excluded.updated_at,dirty=0", table, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		replica.synchronized()
	}
	return err
}

func reconcileReplicaTable(ctx context.Context, tx *sql.Tx, table string, remote replicaSnapshot) error {
	primaryKeys, ok := replicaPrimaryKeys[table]
	if !ok {
		return fmt.Errorf("replica table %s has no primary-key mapping", table)
	}
	if len(remote.columns) == 0 {
		return fmt.Errorf("replica table %s has no remote columns", table)
	}
	columnIndexes := make(map[string]int, len(remote.columns))
	quotedColumns := make([]string, len(remote.columns))
	for i, column := range remote.columns {
		columnIndexes[strings.ToLower(column)] = i
		quotedColumns[i] = quoteReplicaIdentifier(column)
	}
	keyIndexes := make([]int, len(primaryKeys))
	for i, key := range primaryKeys {
		index, exists := columnIndexes[key]
		if !exists {
			return fmt.Errorf("replica table %s is missing primary key column %s", table, key)
		}
		keyIndexes[i] = index
	}
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoteReplicaIdentifier(table))
	if err != nil {
		return fmt.Errorf("read local replica table %s: %w", table, err)
	}
	local := make(map[string][]any)
	for rows.Next() {
		values := make([]any, len(remote.columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return errors.Join(err, rows.Close())
		}
		for i, value := range values {
			if data, ok := value.([]byte); ok {
				values[i] = append([]byte(nil), data...)
			}
		}
		local[replicaRowKey(values, keyIndexes)] = values
	}
	if err := rows.Err(); err != nil {
		return errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	remoteKeys := make(map[string]struct{}, len(remote.records))
	for _, values := range remote.records {
		remoteKeys[replicaRowKey(values, keyIndexes)] = struct{}{}
	}
	quotedTable := quoteReplicaIdentifier(table)
	whereParts := make([]string, len(keyIndexes))
	for i, index := range keyIndexes {
		whereParts[i] = quotedColumns[index] + " = ?"
	}
	for key, values := range local {
		if _, exists := remoteKeys[key]; exists {
			continue
		}
		args := make([]any, len(keyIndexes))
		for i, index := range keyIndexes {
			args[i] = values[index]
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+quotedTable+" WHERE "+strings.Join(whereParts, " AND "), args...); err != nil {
			return fmt.Errorf("delete stale replica row from %s: %w", table, err)
		}
	}
	placeholders := make([]string, len(remote.columns))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insertSQL := "INSERT INTO " + quotedTable + " (" + strings.Join(quotedColumns, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	setParts := make([]string, 0, len(remote.columns)-len(keyIndexes))
	keySet := make(map[int]struct{}, len(keyIndexes))
	for _, index := range keyIndexes {
		keySet[index] = struct{}{}
	}
	for i, column := range quotedColumns {
		if _, isKey := keySet[i]; !isKey {
			setParts = append(setParts, column+" = ?")
		}
	}
	updateSQL := "UPDATE " + quotedTable + " SET " + strings.Join(setParts, ",") + " WHERE " + strings.Join(whereParts, " AND ")
	for _, remoteValues := range remote.records {
		key := replicaRowKey(remoteValues, keyIndexes)
		localValues, exists := local[key]
		if !exists {
			if _, err := tx.ExecContext(ctx, insertSQL, remoteValues...); err != nil {
				return fmt.Errorf("insert replica row into %s: %w", table, err)
			}
			continue
		}
		if replicaRowsEqual(localValues, remoteValues) {
			continue
		}
		args := make([]any, 0, len(remoteValues)+len(keyIndexes))
		for i, value := range remoteValues {
			if _, isKey := keySet[i]; !isKey {
				args = append(args, value)
			}
		}
		for _, index := range keyIndexes {
			args = append(args, remoteValues[index])
		}
		if len(setParts) > 0 {
			if _, err := tx.ExecContext(ctx, updateSQL, args...); err != nil {
				return fmt.Errorf("update replica row in %s: %w", table, err)
			}
		}
	}
	return nil
}

func quoteReplicaIdentifier(value string) string {
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}

func replicaRowKey(values []any, indexes []int) string {
	var key strings.Builder
	for _, index := range indexes {
		value := replicaValueString(values[index])
		key.WriteString(strconv.Itoa(len(value)))
		key.WriteByte(':')
		key.WriteString(value)
		key.WriteByte('|')
	}
	return key.String()
}

func replicaRowsEqual(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if replicaValueString(left[i]) != replicaValueString(right[i]) {
			return false
		}
	}
	return true
}

func replicaValueString(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case []byte:
		return "bytes:" + string(typed)
	case string:
		return "string:" + typed
	case int64:
		return "int:" + strconv.FormatInt(typed, 10)
	case int:
		return "int:" + strconv.Itoa(typed)
	case float64:
		return "float:" + strconv.FormatFloat(typed, 'g', -1, 64)
	case bool:
		if typed {
			return "bool:true"
		}
		return "bool:false"
	case time.Time:
		return "time:" + typed.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%T:%v", value, value)
	}
}

func (replica *ReadReplica) synchronized() {
	now := time.Now()
	replica.mu.Lock()
	for _, table := range replicaTables {
		delete(replica.dirty, table)
		replica.updated[table] = now
	}
	replica.mu.Unlock()
}

func (replica *ReadReplica) refreshAfterWrite(ctx context.Context) {
	if err := replica.Synchronize(ctx); err != nil {
		evictErr := replica.evictAll()
		replica.warn(errors.Join(err, evictErr))
		return
	}
}

func (replica *ReadReplica) evictAll() error {
	err := replica.store.Write(context.Background(), func(tx *sql.Tx) error {
		for _, table := range replicaTables {
			if _, err := tx.Exec("DELETE FROM \"" + table + "\""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return errors.Join(err, replica.MarkAllDirty())
	} else {
		recordCacheEvictionMetric()
	}
	if err == nil {
		replica.mu.Lock()
		for _, table := range replicaTables {
			replica.updated[table] = time.Time{}
			replica.dirty[table] = false
		}
		replica.mu.Unlock()
	}
	return err
}

func recordCacheMetric(hit bool) {
	observerState.RLock()
	observer := observerState.observer
	observerState.RUnlock()
	if metrics, ok := observer.(interface{ RecordCache(string, bool) }); ok {
		metrics.RecordCache("database", hit)
	}
}

func recordCacheEvictionMetric() {
	observerState.RLock()
	observer := observerState.observer
	observerState.RUnlock()
	if metrics, ok := observer.(interface{ RecordCacheEviction(string) }); ok {
		metrics.RecordCacheEviction("database")
	}
}

func replicaTableForQuery(query string) string {
	tokens := tokenizeSQL(query)
	if len(tokens) == 0 || !strings.EqualFold(query[tokens[0].start:tokens[0].end], "select") {
		return ""
	}
	for i, token := range tokens {
		if token.kind != 'w' || !strings.EqualFold(query[token.start:token.end], "from") || i+1 >= len(tokens) {
			continue
		}
		next := tokens[i+1]
		if next.kind != 'w' && next.kind != 'q' {
			return ""
		}
		name := strings.ToLower(unquoteSQLIdentifier(query[next.start:next.end]))
		name = strings.TrimPrefix(name, "srv_")
		for _, known := range replicaTables {
			if name == known {
				return name
			}
		}
		return ""
	}
	return ""
}

type driverRows interface {
	Columns() []string
	Close() error
	Next([]driver.Value) error
}
