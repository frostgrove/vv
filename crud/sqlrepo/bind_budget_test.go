package sqlrepo_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type limitedPostgres struct {
	crud.Postgres
	limit int
}

func (this limitedPostgres) Name() string       { return "limited-postgres" }
func (this limitedPostgres) MaxBindValues() int { return this.limit }

// budgetSource makes transaction ownership observable without changing the
// production recorder. Successful statements still pass through Recorder, so
// the SQL and bind order remain asserted through the normal test seam.
type budgetSource struct {
	*crudtest.Recorder

	mu       sync.Mutex
	begin    int
	commit   int
	rollback int
	executed int
	failAt   int
	failure  error
}

func newBudgetSource(limit int) *budgetSource {
	d := limitedPostgres{limit: limit}
	return &budgetSource{Recorder: crudtest.New(d)}
}

func (this *budgetSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	this.mu.Lock()
	this.executed++
	n := this.executed
	fail := this.failAt == n
	err := this.failure
	this.mu.Unlock()
	if fail {
		return crud.Result{}, err
	}
	return this.Recorder.Exec(ctx, query, args...)
}

func (this *budgetSource) Begin(context.Context) (crud.Tx, error) {
	this.mu.Lock()
	this.begin++
	this.mu.Unlock()
	return &budgetTx{source: this}, nil
}

func (this *budgetSource) counts() (begin, commit, rollback, executed int) {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.begin, this.commit, this.rollback, this.executed
}

type budgetTx struct{ source *budgetSource }

func (this *budgetTx) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return this.source.Exec(ctx, query, args...)
}

func (this *budgetTx) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return this.source.Query(ctx, query, args...)
}

func (this *budgetTx) Commit(context.Context) error {
	this.source.mu.Lock()
	defer this.source.mu.Unlock()
	this.source.commit++
	return nil
}

func (this *budgetTx) Rollback(context.Context) error {
	this.source.mu.Lock()
	defer this.source.mu.Unlock()
	this.source.rollback++
	return nil
}

type noBeginSource struct{ recorder *crudtest.Recorder }

func (this noBeginSource) Dialect() crud.Dialect { return this.recorder.Dialect() }
func (this noBeginSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return this.recorder.Exec(ctx, query, args...)
}
func (this noBeginSource) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return this.recorder.Query(ctx, query, args...)
}

func TestDirectInOverTheDialectBudgetFailsBeforeTheDatabase(t *testing.T) {
	source := newBudgetSource(3)
	_, err := Users.Bind(source).GetAll(context.Background(), crud.Where(crud.And(
		crud.Eq("TenantID", int64(7)),
		crud.InAny("ID", []int64{1, 2, 3}),
	)))
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("err = %T %v, want *crud.SchemaError", err, err)
	}
	if !strings.Contains(schemaErr.Reason, "needs 4") || !strings.Contains(schemaErr.Reason, "at most 3") {
		t.Fatalf("reason = %q, want the complete statement budget", schemaErr.Reason)
	}
	if _, _, _, executed := source.counts(); executed != 0 || len(source.Statements()) != 0 {
		t.Fatalf("executed = %d, statements = %v: an oversized IN reached the database", executed, source.SQL())
	}
}

func TestSaveAllChunksAtTheDialectBudgetAndKeepsInputOrder(t *testing.T) {
	source := newBudgetSource(10)
	source.ExecResult(crud.Result{RowsAffected: 1})
	models := []*User{
		{ID: 1, Email: "1@x", Name: "one"},
		{ID: 2, Email: "2@x", Name: "two"},
		{ID: 3, Email: "3@x", Name: "three"},
		{ID: 4, Email: "4@x", Name: "four"},
		{ID: 5, Email: "5@x", Name: "five"},
	}
	if err := Users.Bind(source).SaveAll(context.Background(), models); err != nil {
		t.Fatal(err)
	}
	statements := source.Statements()
	if len(statements) != 3 {
		t.Fatalf("statements = %d, want three chunks", len(statements))
	}
	wantIDs := [][]int64{{1, 2}, {3, 4}, {5}}
	for i, statement := range statements {
		if len(statement.Args) > 10 {
			t.Fatalf("chunk %d has %d binds, limit is 10", i, len(statement.Args))
		}
		width := 5 // id, email, name, age, tenant_id; created_at is generated.
		for row, want := range wantIDs[i] {
			if got := statement.Args[row*width]; got != want {
				t.Fatalf("chunk %d row %d id = %v, want %d", i, row, got, want)
			}
		}
		if !strings.Contains(statement.SQL, "ON CONFLICT") {
			t.Fatalf("chunk %d lost assigned-key upsert semantics: %s", i, statement.SQL)
		}
	}
	begin, commit, rollback, _ := source.counts()
	if begin != 1 || commit != 1 || rollback != 0 {
		t.Fatalf("transactions = begin:%d commit:%d rollback:%d, want 1/1/0", begin, commit, rollback)
	}
}

func TestGeneratedKeySaveAllKeepsItsWriteOnlySemanticsAcrossChunks(t *testing.T) {
	source := newBudgetSource(8)
	models := []*User{
		{Email: "1@x"}, {Email: "2@x"}, {Email: "3@x"}, {Email: "4@x"}, {Email: "5@x"},
	}
	if err := Users.Bind(source).SaveAll(context.Background(), models); err != nil {
		t.Fatal(err)
	}
	statements := source.Statements()
	if len(statements) != 3 {
		t.Fatalf("statements = %d, want three generated-key chunks", len(statements))
	}
	wantEmails := [][]string{{"1@x", "2@x"}, {"3@x", "4@x"}, {"5@x"}}
	for i, statement := range statements {
		if strings.Contains(statement.SQL, "ON CONFLICT") {
			t.Fatalf("chunk %d became an assigned-key upsert: %s", i, statement.SQL)
		}
		width := 4 // email, name, age, tenant_id; id and created_at are generated.
		for row, want := range wantEmails[i] {
			if got := statement.Args[row*width]; got != want {
				t.Fatalf("chunk %d row %d email = %v, want %q", i, row, got, want)
			}
		}
	}
	for i, model := range models {
		if model.ID != 0 {
			t.Fatalf("model %d id = %d: SaveAll must stay write-only", i, model.ID)
		}
	}
}

func TestSaveAllAtOneStatementDoesNotOpenATransaction(t *testing.T) {
	source := newBudgetSource(10)
	models := []*User{{ID: 1, Email: "1@x"}, {ID: 2, Email: "2@x"}}
	if err := Users.Bind(source).SaveAll(context.Background(), models); err != nil {
		t.Fatal(err)
	}
	begin, commit, rollback, executed := source.counts()
	if begin != 0 || commit != 0 || rollback != 0 || executed != 1 {
		t.Fatalf("begin:%d commit:%d rollback:%d executed:%d, want 0/0/0/1", begin, commit, rollback, executed)
	}
}

func TestSaveAllRollsEveryChunkBackWhenALaterChunkFails(t *testing.T) {
	source := newBudgetSource(10)
	boom := errors.New("second chunk failed")
	source.failAt, source.failure = 2, boom
	models := []*User{
		{ID: 1, Email: "1@x"}, {ID: 2, Email: "2@x"}, {ID: 3, Email: "3@x"},
	}
	err := Users.Bind(source).SaveAll(context.Background(), models)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the second-chunk failure", err)
	}
	begin, commit, rollback, executed := source.counts()
	if begin != 1 || commit != 0 || rollback != 1 || executed != 2 {
		t.Fatalf("begin:%d commit:%d rollback:%d executed:%d, want 1/0/1/2", begin, commit, rollback, executed)
	}
}

func TestChunkedSaveAllDoesNotMistakeAnAmbientPoolForATransaction(t *testing.T) {
	source := newBudgetSource(10)
	boom := errors.New("second chunk failed")
	source.failAt, source.failure = 2, boom
	ctx := crud.WithExecutorFor(context.Background(), source, source)
	models := []*User{
		{ID: 1, Email: "1@x"}, {ID: 2, Email: "2@x"}, {ID: 3, Email: "3@x"},
	}
	if err := Users.Bind(source).SaveAll(ctx, models); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want second-chunk failure", err)
	}
	begin, commit, rollback, executed := source.counts()
	if begin != 1 || commit != 0 || rollback != 1 || executed != 2 {
		t.Fatalf("begin:%d commit:%d rollback:%d executed:%d, want new atomic boundary 1/0/1/2", begin, commit, rollback, executed)
	}
}

func TestChunkedSaveAllJoinsAKnownAmbientTransaction(t *testing.T) {
	source := newBudgetSource(10)
	tx := &budgetTx{source: source}
	ctx := crud.WithExecutorFor(context.Background(), source, tx)
	models := []*User{
		{ID: 1, Email: "1@x"}, {ID: 2, Email: "2@x"}, {ID: 3, Email: "3@x"},
	}
	if err := Users.Bind(source).SaveAll(ctx, models); err != nil {
		t.Fatal(err)
	}
	begin, commit, rollback, executed := source.counts()
	if begin != 0 || commit != 0 || rollback != 0 || executed != 2 {
		t.Fatalf("begin:%d commit:%d rollback:%d executed:%d, want joined 0/0/0/2", begin, commit, rollback, executed)
	}
}

func TestSingleSaveRefusesAnOversizedStatementBeforeTheDatasource(t *testing.T) {
	source := newBudgetSource(4) // User's assigned-key insert carries five binds.
	err := Users.Bind(source).SaveOnly(context.Background(), &User{ID: 1, Email: "one@x"})
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) || !strings.Contains(schemaErr.Reason, "Save needs 5 bound values") {
		t.Fatalf("err = %T %v, want statement-wide Save budget refusal", err, err)
	}
	begin, _, _, executed := source.counts()
	if begin != 0 || executed != 0 || len(source.Statements()) != 0 {
		t.Fatalf("begin:%d executed:%d statements:%v: oversized Save reached datasource", begin, executed, source.SQL())
	}
}

func TestChunkedWriteWithoutTransactionSupportRunsNoStatement(t *testing.T) {
	recorder := crudtest.New(limitedPostgres{limit: 10})
	source := noBeginSource{recorder: recorder}
	err := Users.Bind(source).SaveAll(context.Background(), []*User{
		{ID: 1, Email: "1@x"}, {ID: 2, Email: "2@x"}, {ID: 3, Email: "3@x"},
	})
	if !errors.Is(err, crud.ErrNoTxSupport) {
		t.Fatalf("err = %v, want ErrNoTxSupport", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("statements = %v: a non-atomic chunked write must be refused before execution", recorder.SQL())
	}
}

func TestChunkedDeleteWithoutTransactionSupportRunsNoStatement(t *testing.T) {
	recorder := crudtest.New(limitedPostgres{limit: 2})
	source := noBeginSource{recorder: recorder}
	_, err := Docs.Bind(source).Delete(context.Background(), "a", "b", "c")
	if !errors.Is(err, crud.ErrNoTxSupport) {
		t.Fatalf("err = %v, want ErrNoTxSupport", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("statements = %v: a non-atomic chunked delete must be refused before execution", recorder.SQL())
	}
}

func TestSaveAllRefusesARowWiderThanTheBudgetBeforeOpeningATransaction(t *testing.T) {
	source := newBudgetSource(4)
	err := Users.Bind(source).SaveAll(context.Background(), []*User{{ID: 1, Email: "1@x"}})
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) || !strings.Contains(schemaErr.Reason, "one SaveAll row needs 5") {
		t.Fatalf("err = %T %v, want an actionable row-width SchemaError", err, err)
	}
	begin, _, _, executed := source.counts()
	if begin != 0 || executed != 0 || len(source.Statements()) != 0 {
		t.Fatalf("begin:%d executed:%d statements:%v: preflight ran too late", begin, executed, source.SQL())
	}
}

type budgetSoftDoc struct {
	ID        string              `db:"id,pk"`
	Tenant    int64               `db:"tenant"`
	DeletedAt crud.Opt[time.Time] `db:"deleted_at"`
}

var budgetSoftDocs = sqlrepo.Define[budgetSoftDoc, string, struct{}]("budget_soft_docs",
	sqlrepo.Scope(crud.Eq("Tenant", int64(7))),
	sqlrepo.SoftDelete("DeletedAt"),
)

func TestDeleteChunksAfterChargingScopeAndSoftDeleteBinds(t *testing.T) {
	source := newBudgetSource(4)
	source.ExecResult(crud.Result{RowsAffected: 1})
	before := crud.NowFunc
	stamp := time.Date(2026, 8, 30, 11, 22, 33, 0, time.UTC)
	crud.NowFunc = func() time.Time { return stamp }
	t.Cleanup(func() { crud.NowFunc = before })

	n, err := budgetSoftDocs.Bind(source).Delete(context.Background(), "a", "b", "c", "d", "e")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("affected = %d, want the sum from three chunks", n)
	}
	statements := source.Statements()
	if len(statements) != 3 {
		t.Fatalf("statements = %d, want three chunks", len(statements))
	}
	wantIDs := [][]string{{"a", "b"}, {"c", "d"}, {"e"}}
	for i, statement := range statements {
		if len(statement.Args) > 4 {
			t.Fatalf("chunk %d has %d binds, limit is 4", i, len(statement.Args))
		}
		if statement.Args[0] != stamp || statement.Args[1] != int64(7) {
			t.Fatalf("chunk %d args = %#v, want one stable tombstone then the permanent scope", i, statement.Args)
		}
		for j, id := range wantIDs[i] {
			if got := statement.Args[j+2]; got != id {
				t.Fatalf("chunk %d id %d = %v, want %q", i, j, got, id)
			}
		}
	}
	begin, commit, rollback, _ := source.counts()
	if begin != 1 || commit != 1 || rollback != 0 {
		t.Fatalf("transactions = begin:%d commit:%d rollback:%d, want 1/1/0", begin, commit, rollback)
	}
}

func TestDeleteRefusesWhenScopesExhaustTheBudgetBeforeOpeningATransaction(t *testing.T) {
	source := newBudgetSource(2) // tombstone + tenant scope leave no bind for an id
	_, err := budgetSoftDocs.Bind(source).Delete(context.Background(), "a")
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) ||
		!strings.Contains(schemaErr.Reason, "one id bind in addition to 2 scope/write binds") ||
		!strings.Contains(schemaErr.Reason, "at most 2") {
		t.Fatalf("err = %T %v, want an actionable exhausted-budget SchemaError", err, err)
	}
	begin, _, _, executed := source.counts()
	if begin != 0 || executed != 0 || len(source.Statements()) != 0 {
		t.Fatalf("begin:%d executed:%d statements:%v: preflight ran too late", begin, executed, source.SQL())
	}
}

func TestDeleteRollsEveryChunkBackWhenALaterChunkFails(t *testing.T) {
	source := newBudgetSource(2)
	boom := errors.New("second delete chunk failed")
	source.failAt, source.failure = 2, boom
	_, err := Docs.Bind(source).Delete(context.Background(), "a", "b", "c")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the second-chunk failure", err)
	}
	begin, commit, rollback, executed := source.counts()
	if begin != 1 || commit != 0 || rollback != 1 || executed != 2 {
		t.Fatalf("begin:%d commit:%d rollback:%d executed:%d, want 1/0/1/2", begin, commit, rollback, executed)
	}
}

func TestSecurityGateKeepsScopedInspectedDeleteChunking(t *testing.T) {
	source := newBudgetSource(8) // tenant + id + the six-field inspected snapshot
	source.ExecResult(crud.Result{RowsAffected: 1})
	rows := make([][]any, 0, 5)
	for id := int64(1); id <= 5; id++ {
		rows = append(rows, userRow(id, fmt.Sprintf("%d@x", id), "user", 20, 7))
	}
	source.Push(crudtest.Rows(rows...))
	policy := security.ScopeField[User, int64]("TenantID", func(context.Context) (any, error) {
		return int64(7), nil
	})
	repository := Users.Bind(source, security.Gate(policy))

	n, err := repository.Delete(context.Background(), 1, 2, 3, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("deleted = %d, want 5", n)
	}
	deletes := 0
	for _, statement := range source.Statements() {
		if strings.HasPrefix(statement.SQL, "DELETE ") {
			deletes++
			if len(statement.Args) > 8 {
				t.Fatalf("gated delete has %d binds, limit is 8: %s", len(statement.Args), statement.SQL)
			}
		}
	}
	if deletes != 5 {
		t.Fatalf("delete chunks = %d, want one inspected row per budgeted statement", deletes)
	}
	begin, commit, rollback, _ := source.counts()
	if begin != 1 || commit != 1 || rollback != 0 {
		t.Fatalf("transactions = begin:%d commit:%d rollback:%d, want 1/1/0", begin, commit, rollback)
	}
}

func TestEmptyBulkWritesRemainNoOps(t *testing.T) {
	source := newBudgetSource(1)
	if err := Users.Bind(source).SaveAll(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if n, err := Users.Bind(source).Delete(context.Background()); err != nil || n != 0 {
		t.Fatalf("Delete = (%d, %v), want (0, nil)", n, err)
	}
	begin, commit, rollback, executed := source.counts()
	if begin != 0 || commit != 0 || rollback != 0 || executed != 0 {
		t.Fatalf("begin:%d commit:%d rollback:%d executed:%d, want a complete no-op", begin, commit, rollback, executed)
	}
}
