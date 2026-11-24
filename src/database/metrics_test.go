package database

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	appmode "github.com/apimgr/countries/src/mode"
	"modernc.org/sqlite"
)

type recordedDBOperation struct {
	operation string
	table     string
	errorType string
}

type dbOperationRecorder struct {
	operations []recordedDBOperation
}

func (r *dbOperationRecorder) ObserveDB(operation, table string, _ time.Duration, errorType string) {
	r.operations = append(r.operations, recordedDBOperation{operation: operation, table: table, errorType: errorType})
}

func TestRecordDatabaseOperationDebugLogging(t *testing.T) {
	previousMode := appmode.DebugEnabled()
	appmode.SetDebugEnabled(true)
	t.Cleanup(func() { appmode.SetDebugEnabled(previousMode) })
	previousLogger := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	recordDatabaseOperation("SELECT * FROM owners WHERE id = ?", time.Now().Add(-time.Millisecond), errors.New("constraint violation"))
	for _, expected := range []string{"db query", "operation=select", "table=owners", "error_type=constraint"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("debug log %q does not contain %q", output.String(), expected)
		}
	}
}

func TestInstrumentedSQLiteDriverRecordsOperations(t *testing.T) {
	connection, err := (&sqlite.Driver{}).Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close SQLite connection: %v", err)
		}
	})
	recorder := &dbOperationRecorder{}
	SetMetricsObserver(recorder)
	t.Cleanup(func() { SetMetricsObserver(nil) })
	ctx := context.Background()
	instrumented := instrumentedConn{Conn: connection}
	if _, err := instrumented.ExecContext(ctx, "CREATE TABLE owners (id TEXT)", nil); err != nil {
		t.Fatalf("ExecContext() error = %v", err)
	}
	rows, err := instrumented.QueryContext(ctx, "SELECT id FROM owners", nil)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close QueryContext() rows: %v", err)
	}
	statement, err := instrumented.Prepare("INSERT INTO owners (id) VALUES (?)")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	//lint:ignore SA1019 Exercise the legacy driver compatibility path.
	if _, err := statement.Exec([]driver.Value{"legacy"}); err != nil { //nolint:staticcheck
		t.Fatalf("Exec() error = %v", err)
	}
	//lint:ignore SA1019 Exercise the legacy driver compatibility path.
	rows, err = statement.Query([]driver.Value{"legacy"}) //nolint:staticcheck
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close Query() rows: %v", err)
	}
	if err := statement.Close(); err != nil {
		t.Fatalf("close statement: %v", err)
	}
	statement, err = instrumented.PrepareContext(ctx, "SELECT id FROM owners")
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if _, err := statement.(driver.StmtExecContext).ExecContext(ctx, nil); err != nil {
		t.Fatalf("ExecContext() statement error = %v", err)
	}
	rows, err = statement.(driver.StmtQueryContext).QueryContext(ctx, nil)
	if err != nil {
		t.Fatalf("QueryContext() statement error = %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close QueryContext() statement rows: %v", err)
	}
	if err := statement.Close(); err != nil {
		t.Fatalf("close context statement: %v", err)
	}
	transaction, err := instrumented.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	if len(recorder.operations) < 5 {
		t.Fatalf("recorded %d operations, want at least five", len(recorder.operations))
	}
	if recorder.operations[0].operation != "other" || recorder.operations[1].operation != "select" || recorder.operations[1].table != "owners" {
		t.Fatalf("recorded operations = %#v", recorder.operations[:2])
	}
}

func TestDescribeDatabaseQueryAndClassifyErrors(t *testing.T) {
	for _, test := range []struct {
		query, operation, table string
	}{
		{"", "other", "other"},
		{"UPDATE owners SET name = ?", "update", "owners"},
		{"INSERT INTO owners (id) VALUES (?)", "insert", "owners"},
		{"DELETE FROM owners WHERE id = ?", "delete", "owners"},
		{"WITH records AS (SELECT id FROM owners) SELECT id FROM records", "select", "owners"},
		{"CREATE TABLE owners (id TEXT)", "other", "other"},
		{"SELECT 1", "select", "other"},
	} {
		operation, table := describeDatabaseQuery(test.query)
		if operation != test.operation || table != test.table {
			t.Errorf("describeDatabaseQuery(%q) = (%q, %q), want (%q, %q)", test.query, operation, table, test.operation, test.table)
		}
	}
	for _, test := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "timeout"},
		{errors.New("request timeout"), "timeout"},
		{errors.New("unique violation"), "duplicate"},
		{errors.New("connection closed"), "connection"},
		{errors.New("disk full"), "other"},
	} {
		if got := classifyDatabaseError(test.err); got != test.want {
			t.Errorf("classifyDatabaseError(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
