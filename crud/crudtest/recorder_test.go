package crudtest_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

type Widget struct {
	ID     int64            `db:"id,pk,auto"`
	Name   string           `db:"name"`
	Weight *float64         `db:"weight"`
	Note   crud.Opt[string] `db:"note"`
	Made   time.Time        `db:"made"`
}

func widgetSchema(t *testing.T) *crud.Schema {
	t.Helper()
	s, err := crud.SchemaOf[Widget]()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecorderRecordsEveryStatementInOrder(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres()

	if _, err := rec.Exec(ctx, "DELETE FROM widgets WHERE id = $1", int64(1)); err != nil {
		t.Fatal(err)
	}
	rows, err := rec.Query(ctx, "SELECT id FROM widgets WHERE name = $1", "a")
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()

	st := rec.Statements()
	if len(st) != 2 {
		t.Fatalf("recorded %d statements, want 2: %v", len(st), rec.SQL())
	}
	if st[0].SQL != "DELETE FROM widgets WHERE id = $1" || st[0].Query {
		t.Errorf("first statement = %+v, want the DELETE recorded as an Exec", st[0])
	}
	if len(st[0].Args) != 1 || st[0].Args[0] != int64(1) {
		t.Errorf("first args = %#v, want the bound id", st[0].Args)
	}
	if !st[1].Query {
		t.Errorf("second statement = %+v, want it marked as a Query", st[1])
	}
	if rec.Last().SQL != st[1].SQL {
		t.Errorf("Last = %q, want the most recent statement", rec.Last().SQL)
	}
	if got := rec.SQL(); len(got) != 2 || got[0] != st[0].SQL || got[1] != st[1].SQL {
		t.Errorf("SQL() = %v, want just the two statement texts", got)
	}
	if got := st[1].String(); got != `SELECT id FROM widgets WHERE name = $1 [a]` {
		t.Errorf("Statement.String() = %q", got)
	}
}

func TestLastAndSQLOnAFreshRecorder(t *testing.T) {
	rec := crudtest.Postgres()
	if last := rec.Last(); last.SQL != "" || last.Args != nil || last.Query {
		t.Errorf("Last on an empty recorder = %+v, want the zero statement", last)
	}
	if got := rec.SQL(); got == nil || len(got) != 0 {
		t.Errorf("SQL on an empty recorder = %#v, want an empty slice", got)
	}
}

func TestQueriesAreAnsweredFromTheQueueInOrder(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(1)}, []any{int64(2)}),
		crudtest.Rows([]any{int64(9)}),
	)

	for _, want := range [][]int64{{1, 2}, {9}, {}} {
		rows, err := rec.Query(ctx, "SELECT id FROM widgets")
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if len(got) != len(want) {
			t.Fatalf("read %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("read %v, want %v", got, want)
			}
		}
	}
}

func TestAQueuedErrorSurfacesAndTheStatementIsStillRecorded(t *testing.T) {
	boom := errors.New("connection reset")
	rec := crudtest.Postgres().Push(crudtest.Result{Err: boom})

	_, err := rec.Query(context.Background(), "SELECT 1")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the queued error", err)
	}
	if len(rec.Statements()) != 1 {
		t.Fatal("a failing query must still be recorded, or the failure cannot be explained")
	}
}

func TestARowsErrorArrivesAfterTheRowsRatherThanInsteadOfThem(t *testing.T) {
	ctx := context.Background()
	cut := errors.New("terminating connection due to administrator command")
	rec := crudtest.Postgres().Push(
		crudtest.RowsFailing(cut, []any{int64(1)}, []any{int64(2)}),
		crudtest.Rows([]any{int64(3)}),
	)

	rows, err := rec.Query(ctx, "SELECT n FROM t")
	if err != nil {
		t.Fatalf("Query refused outright: %v — RowsErr is the failure that arrives after the rows", err)
	}
	seen := 0
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		seen++
	}
	if seen != 2 {
		t.Errorf("the failing result set yielded %d rows, want the 2 it was given", seen)
	}
	if !errors.Is(rows.Err(), cut) {
		t.Errorf("Rows.Err answered %v after the rows ran out, want the queued failure", rows.Err())
	}
	rows.Close()

	plain, err := rec.Query(ctx, "SELECT n FROM t")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	for plain.Next() {
	}
	if plain.Err() != nil {
		t.Errorf("a plain Rows result reports %v from Err, so the failure above says nothing", plain.Err())
	}
}

func TestExecResultAndOneShotFailure(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 3, LastInsertID: 7, HasLastInsertID: true})

	response, err := rec.Exec(ctx, "UPDATE widgets SET name = $1", "a")
	if err != nil {
		t.Fatal(err)
	}
	if response.RowsAffected != 3 || response.LastInsertID != 7 || !response.HasLastInsertID {
		t.Fatalf("result = %+v, want the configured one", response)
	}

	boom := errors.New("deadlock")
	rec.Fail(boom)
	if _, err := rec.Exec(ctx, "UPDATE widgets SET name = $1", "b"); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the configured failure", err)
	}

	if response, err := rec.Exec(ctx, "UPDATE widgets SET name = $1", "c"); err != nil || response.RowsAffected != 3 {
		t.Fatalf("Exec after the failure = (%+v, %v), want the configured result back", response, err)
	}
	if len(rec.Statements()) != 3 {
		t.Fatalf("recorded %d statements, want all three including the failed one", len(rec.Statements()))
	}
}

func TestScanFillsAModelThroughTheSchema(t *testing.T) {
	s := widgetSchema(t)
	made := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	weight := 2.5

	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(1), "spanner", weight, "handy", made},
		[]any{int64(2), "hammer", nil, nil, made},
	))

	rows, err := rec.Query(context.Background(), "SELECT * FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []Widget
	for rows.Next() {
		var w Widget
		dest, err := s.Pointers(&w, s.Fields)
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, w)
	}
	if len(got) != 2 {
		t.Fatalf("scanned %d rows, want 2", len(got))
	}

	if got[0].ID != 1 || got[0].Name != "spanner" || !got[0].Made.Equal(made) {
		t.Errorf("row 1 = %+v, want the plain columns filled", got[0])
	}
	if got[0].Weight == nil || *got[0].Weight != 2.5 {
		t.Errorf("weight = %v, want a pointer column allocated on the way in", got[0].Weight)
	}
	if v, ok := got[0].Note.Get(); !ok || v != "handy" {
		t.Errorf("note = %v, want a set Opt", got[0].Note)
	}

	if got[1].Weight != nil {
		t.Errorf("weight = %v, want NULL to leave the pointer nil", got[1].Weight)
	}
	if !got[1].Note.IsNull() {
		t.Errorf("note = %v, want an explicit null", got[1].Note)
	}
}

func TestScanUsesDatabaseSQLConversions(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{1, []byte("spanner"), 3, []byte("handy")}))

	rows, err := rec.Query(ctx, "SELECT id, name, weight, note FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no row")
	}

	var (
		id     int64
		name   string
		weight float64
		note   crud.Opt[string]
	)
	if err := rows.Scan(&id, &name, &weight, &note); err != nil {
		t.Fatal(err)
	}
	if id != 1 || name != "spanner" || weight != 3 {
		t.Fatalf("scanned (%d, %q, %v), want (1, spanner, 3)", id, name, weight)
	}
	if v, _ := note.Get(); v != "handy" {
		t.Fatalf("note = %v, want the bytes decoded through sql.Scanner", note)
	}
}

type scanConnector struct{ value driver.Value }

func (c scanConnector) Connect(context.Context) (driver.Conn, error) {
	return &scanConn{value: c.value}, nil
}
func (c scanConnector) Driver() driver.Driver { return scanDriver{} }

type scanDriver struct{}

func (scanDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type scanConn struct{ value driver.Value }

func (*scanConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("not supported") }
func (*scanConn) Close() error                        { return nil }
func (*scanConn) Begin() (driver.Tx, error)           { return nil, errors.New("not supported") }
func (c *scanConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &scanRows{value: c.value}, nil
}

type scanRows struct {
	value driver.Value
	done  bool
}

func (*scanRows) Columns() []string { return []string{"value"} }
func (*scanRows) Close() error      { return nil }
func (r *scanRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = r.value
	return nil
}

func TestRecorderScanMatchesDatabaseSQL(t *testing.T) {
	type label string
	now := time.Date(2026, 8, 30, 9, 10, 11, 12, time.UTC)
	cases := []struct {
		name   string
		source driver.Value
		dest   func() any
	}{
		{"integer to decimal string, not a rune", int64(65), func() any { return new(string) }},
		{"overflow is refused", int64(300), func() any { return new(int8) }},
		{"negative signed to unsigned is refused", int64(-1), func() any { return new(uint64) }},
		{"text to integer", "42", func() any { return new(int32) }},
		{"fraction to integer is refused", float64(1.5), func() any { return new(int64) }},
		{"bytes to string", []byte("hello"), func() any { return new(string) }},
		{"boolean to string", true, func() any { return new(string) }},
		{"time to RFC3339 string", now, func() any { return new(string) }},
		{"NULL to scalar is refused", nil, func() any { return new(int64) }},
		{"NULL to pointer is accepted", nil, func() any { return new(*int64) }},
		{"same-kind named value", "named", func() any { return new(label) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			standardDest := tc.dest()
			db := sql.OpenDB(scanConnector{value: tc.source})
			standardRows, err := db.QueryContext(context.Background(), "SELECT value")
			if err != nil {
				t.Fatal(err)
			}
			if !standardRows.Next() {
				t.Fatal("database/sql returned no control row")
			}
			standardErr := standardRows.Scan(standardDest)
			standardRows.Close()
			db.Close()

			recorderDest := tc.dest()
			rec := crudtest.Postgres().Push(crudtest.Rows([]any{tc.source}))
			recorderRows, err := rec.Query(context.Background(), "SELECT value")
			if err != nil {
				t.Fatal(err)
			}
			recorderRows.Next()
			recorderErr := recorderRows.Scan(recorderDest)
			recorderRows.Close()

			if (recorderErr != nil) != (standardErr != nil) {
				t.Fatalf("recorder err = %v; database/sql err = %v", recorderErr, standardErr)
			}
			if standardErr == nil {
				standardValue := reflect.ValueOf(standardDest).Elem().Interface()
				recorderValue := reflect.ValueOf(recorderDest).Elem().Interface()
				if !reflect.DeepEqual(recorderValue, standardValue) {
					t.Fatalf("recorder value = %#v; database/sql value = %#v", recorderValue, standardValue)
				}
			}
		})
	}
}

type panicScanner struct{}

func (*panicScanner) Scan(any) error { panic("typed-nil scanner invoked") }

func TestScanRefusesATypedNilScannerBeforeInvokingIt(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{"value"}))
	rows, err := rec.Query(context.Background(), "SELECT value")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	rows.Next()
	var destination *panicScanner
	if err := rows.Scan(destination); err == nil {
		t.Fatal("typed-nil scanner was accepted")
	}
}

func TestScanRefusesRowsItCannotFill(t *testing.T) {
	ctx := context.Background()

	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(1), "a"}))
	rows, err := rec.Query(ctx, "SELECT id FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	rows.Next()
	var id int64
	if err := rows.Scan(&id); err == nil {
		t.Error("scanning a two-column row into one destination was accepted")
	}
	rows.Close()

	rec = crudtest.Postgres().Push(crudtest.Rows([]any{"not a number"}))
	rows, err = rec.Query(ctx, "SELECT id FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	rows.Next()
	if err := rows.Scan(&id); err == nil {
		t.Error("scanning a string into an int64 destination was accepted")
	}

	rec = crudtest.Postgres().Push(crudtest.Rows([]any{int64(1)}))
	rows, err = rec.Query(ctx, "SELECT id FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	rows.Next()
	if err := rows.Scan(id); err == nil {
		t.Error("scanning into a non-pointer was accepted")
	}
	rows.Close()
}

func TestStatementsIsACopy(t *testing.T) {
	rec := crudtest.Postgres()
	if _, err := rec.Exec(context.Background(), "DELETE FROM widgets"); err != nil {
		t.Fatal(err)
	}

	got := rec.Statements()
	got[0].SQL = "TRUNCATE widgets"

	if rec.Last().SQL != "DELETE FROM widgets" {
		t.Fatalf("the recording changed under the caller: %q", rec.Last().SQL)
	}
}

func TestResetClearsTheRecordingAndTheQueue(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(1)}))
	if _, err := rec.Exec(ctx, "DELETE FROM widgets"); err != nil {
		t.Fatal(err)
	}

	rec.Reset()

	if len(rec.Statements()) != 0 {
		t.Errorf("statements = %v, want none after Reset", rec.SQL())
	}
	rows, err := rec.Query(ctx, "SELECT id FROM widgets")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("the queued result survived Reset")
	}
}

func TestBeginRecordsIntoTheSameRecorder(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres()

	if rec.TxDepth() != 0 {
		t.Fatalf("TxDepth = %d before any transaction", rec.TxDepth())
	}
	tx, err := rec.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO widgets DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if rec.TxDepth() != 1 {
		t.Errorf("TxDepth = %d, want 1", rec.TxDepth())
	}
	if got := rec.SQL(); len(got) != 1 || got[0] != "INSERT INTO widgets DEFAULT VALUES" {
		t.Errorf("statements = %v, want the transaction's statement in the same recording", got)
	}
}

func TestRecorderDrivesInTx(t *testing.T) {
	rec := crudtest.Postgres()
	err := crud.InTx(context.Background(), rec, func(ctx context.Context) error {
		ex, ok := crud.ExecutorFrom(ctx)
		if !ok {
			t.Fatal("InTx did not push its transaction into the context")
		}
		_, err := ex.Exec(ctx, "UPDATE widgets SET name = $1", "a")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.TxDepth() != 1 || len(rec.Statements()) != 1 {
		t.Fatalf("TxDepth = %d, statements = %v, want one of each", rec.TxDepth(), rec.SQL())
	}
}

func TestDialectShorthands(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  *crudtest.Recorder
		want string
	}{
		{"Postgres", crudtest.Postgres(), "postgres"},
		{"MySQL", crudtest.MySQL(), "mysql"},
		{"New", crudtest.New(crud.SQLite{}), "sqlite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rec.Dialect().Name(); got != tc.want {
				t.Fatalf("dialect = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	got := crudtest.Normalize("SELECT  \"id\",\n\t\"name\"\nFROM \"widgets\"  WHERE id = $1 ")
	if got != `SELECT "id", "name" FROM "widgets" WHERE id = $1` {
		t.Fatalf("normalized = %q", got)
	}
}

func TestTheRecorderNamesItselfAsItsDatasource(t *testing.T) {
	ctx := context.Background()
	rec := crudtest.Postgres()
	other := crudtest.Postgres()

	if _, ok := any(rec).(crud.Identified); !ok {
		t.Fatal("the recorder cannot safely scope a transaction")
	}
	if crud.KeyOf(rec) != any(rec) {
		t.Errorf("crud.KeyOf answered %v for a recorder, want the recorder itself", crud.KeyOf(rec))
	}

	var sameJoined, otherJoined bool
	if err := crud.InTx(ctx, rec, func(ctx context.Context) error {
		_, sameJoined = crud.ExecutorFor(ctx, rec)
		_, otherJoined = crud.ExecutorFor(ctx, other)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !sameJoined {
		t.Error("the recorder that opened a transaction did not join it")
	}
	if otherJoined {
		t.Error("an independent recorder adopted another recorder's transaction")
	}
}
