package sqlrepo_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type nativeBatchCall struct {
	table   crud.TableRef
	columns []string
	rows    [][]any
}

type nativeBatchSource struct {
	*crudtest.Recorder
	attempts    int
	calls       []nativeBatchCall
	bulkErr     error
	unsupported bool
	tx          *nativeBatchTx
}

func newNativeBatchSource(d crud.Dialect) *nativeBatchSource {
	return &nativeBatchSource{Recorder: crudtest.New(d)}
}

func (this *nativeBatchSource) DataSource() any { return this }

func cloneBatchCall(table crud.TableRef, columns []string, rows [][]any) nativeBatchCall {
	copyRows := make([][]any, len(rows))
	for i := range rows {
		copyRows[i] = slices.Clone(rows[i])
	}
	return nativeBatchCall{table: table, columns: slices.Clone(columns), rows: copyRows}
}

func (this *nativeBatchSource) UnsafeBulkInsert(_ context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	if target != nil && target != this {
		tx, ok := target.(*nativeBatchTx)
		if !ok {
			return 0, crud.ErrNoBulkInsertSupport
		}
		return tx.insert(table, columns, rows)
	}
	this.attempts++
	if this.unsupported {
		return 0, crud.ErrNoBulkInsertSupport
	}
	this.calls = append(this.calls, cloneBatchCall(table, columns, rows))
	if this.bulkErr != nil {
		return 0, this.bulkErr
	}
	return int64(len(rows)), nil
}

func (this *nativeBatchSource) Begin(context.Context) (crud.Tx, error) {
	this.tx = &nativeBatchTx{source: this}
	return this.tx, nil
}

type nativeBatchTx struct {
	source     *nativeBatchSource
	calls      []nativeBatchCall
	committed  bool
	rolledBack bool
}

func (this *nativeBatchTx) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	return this.source.Recorder.Exec(ctx, query, args...)
}

func (this *nativeBatchTx) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return this.source.Recorder.Query(ctx, query, args...)
}

func (this *nativeBatchTx) insert(table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	this.calls = append(this.calls, cloneBatchCall(table, columns, rows))
	if this.source.bulkErr != nil {
		return 0, this.source.bulkErr
	}
	return int64(len(rows)), nil
}

func (this *nativeBatchTx) Commit(context.Context) error {
	this.committed = true
	return nil
}

func (this *nativeBatchTx) Rollback(context.Context) error {
	this.rolledBack = true
	return nil
}

type observingSource struct {
	crud.Source
	execs int
}

func (this *observingSource) Exec(ctx context.Context, query string, args ...any) (crud.Result, error) {
	this.execs++
	return this.Source.Exec(ctx, query, args...)
}

func (this *observingSource) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	return this.Source.Query(ctx, query, args...)
}

func (this *observingSource) UnwrapSource() crud.Source { return this.Source }

type refusingBulkSource struct {
	*observingSource
	attempts int
}

func (this *refusingBulkSource) UnsafeBulkInsert(context.Context, crud.Executor, crud.TableRef, []string, [][]any) (int64, error) {
	this.attempts++
	return 0, crud.ErrNoBulkInsertSupport
}

type forwardingBulkSource struct {
	*observingSource
	attempts int
}

func (this *forwardingBulkSource) UnsafeBulkInsert(ctx context.Context, target crud.Executor, table crud.TableRef, columns []string, rows [][]any) (int64, error) {
	this.attempts++
	bulk, ok := crud.UnsafeBulkInserterOf(this.Source)
	if !ok {
		return 0, crud.ErrNoBulkInsertSupport
	}
	return bulk.UnsafeBulkInsert(ctx, target, table, columns, rows)
}

type plainBatchExecutor struct{ execs int }

func (this *plainBatchExecutor) Exec(context.Context, string, ...any) (crud.Result, error) {
	this.execs++
	return crud.Result{}, nil
}

func (this *plainBatchExecutor) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, nil
}

type batchManaged struct {
	ID       int64  `db:"id,pk,auto"`
	Name     string `db:"name"`
	Secret   string `db:"secret,serverowned"`
	Computed string `db:"computed,generated"`
}

type batchManagedUpdate struct{ Name *string }

var batchManagedRows = sqlrepo.DefineInSchema[batchManaged, int64, batchManagedUpdate]("imports", "managed_rows")

type batchDefaultOnly struct {
	ID int64 `db:"id,pk,auto"`
}

var batchDefaultRows = sqlrepo.Define[batchDefaultOnly, int64, struct{}]("batch_default_rows")

var portableDeclaredUsers = sqlrepo.Define[User, int64, UserUpdate]("portable_declared_users",
	sqlrepo.IndependentTable(), sqlrepo.PortableBatch())

func TestInsertBatchDerivesTheNativeTableColumnsAndOptValuesFromTheModel(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	models := []*User{
		{Email: "set@example.test", Age: crud.Set(21), TenantID: 7},
		{Email: "null@example.test", Age: crud.Null[int](), TenantID: 7},
		{Email: "undefined@example.test", TenantID: 7},
	}

	if err := Users.Bind(source).InsertBatch(context.Background(), models); err != nil {
		t.Fatal(err)
	}
	if source.attempts != 1 || len(source.calls) != 1 || len(source.Statements()) != 0 {
		t.Fatalf("native attempts/calls/sql = %d/%d/%d, want 1/1/0", source.attempts, len(source.calls), len(source.Statements()))
	}
	call := source.calls[0]
	if call.table != (crud.TableRef{Name: "users"}) {
		t.Fatalf("table = %#v, want users metadata", call.table)
	}
	if !slices.Equal(call.columns, []string{"email", "name", "age", "tenant_id"}) {
		t.Fatalf("columns = %v; auto/generated columns leaked or model columns were lost", call.columns)
	}
	if len(call.rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(call.rows))
	}
	set, ok := call.rows[0][2].(crud.Opt[int])
	if !ok || !set.IsSet() || set.IsNull() {
		t.Fatalf("set Opt changed before adapter: %#v", call.rows[0][2])
	}
	null, ok := call.rows[1][2].(crud.Opt[int])
	if !ok || !null.IsNull() {
		t.Fatalf("null Opt changed before adapter: %#v", call.rows[1][2])
	}
	undefined, ok := call.rows[2][2].(crud.Opt[int])
	if !ok || undefined.IsDefined() {
		t.Fatalf("undefined Opt changed before adapter: %#v", call.rows[2][2])
	}
	for i, model := range models {
		if model.ID != 0 {
			t.Fatalf("input %d id = %d; write-only batch mutated its command", i, model.ID)
		}
	}
}

func TestInsertBatchUsesExactQualifiedMetadataAndOmitsServerOwnedFields(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	if err := batchManagedRows.Bind(source).InsertBatch(context.Background(), []*batchManaged{{
		Name: "public", Secret: "must-not-write", Computed: "must-not-write",
	}}); err != nil {
		t.Fatal(err)
	}
	call := source.calls[0]
	if call.table != (crud.TableRef{Schema: "imports", Name: "managed_rows"}) {
		t.Fatalf("qualified table = %#v", call.table)
	}
	if !slices.Equal(call.columns, []string{"name"}) || len(call.rows[0]) != 1 || call.rows[0][0] != "public" {
		t.Fatalf("derived batch = columns:%v rows:%#v", call.columns, call.rows)
	}
}

func TestPortableInsertBatchKeepsAssignedKeysCreateOnly(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	if err := Docs.Bind(source).InsertBatch(context.Background(), []*Doc{
		{ID: "a", Title: "one"}, {ID: "b", Title: "two"},
	}, crud.PortableBatch()); err != nil {
		t.Fatal(err)
	}
	if source.attempts != 0 {
		t.Fatalf("portable call attempted native bulk %d times", source.attempts)
	}
	statements := source.Statements()
	if len(statements) != 1 {
		t.Fatalf("statements = %d, want one fitting batch", len(statements))
	}
	if strings.Contains(statements[0].SQL, "ON CONFLICT") {
		t.Fatalf("insert-only assigned keys became an upsert: %s", statements[0].SQL)
	}
	if !slices.Equal(statements[0].Args, []any{"a", "one", "b", "two"}) {
		t.Fatalf("args = %#v", statements[0].Args)
	}
}

func TestPortableBatchDeclarationCannotBeReenabledByACall(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	if err := portableDeclaredUsers.Bind(source).InsertBatch(context.Background(), []*User{{Email: "declared@example.test"}}); err != nil {
		t.Fatal(err)
	}
	if source.attempts != 0 || len(source.Statements()) != 1 {
		t.Fatalf("declared portable batch used native=%d sql=%v", source.attempts, source.SQL())
	}
}

func TestPortableBatchSurvivesEveryBuiltInDecoratorOrder(t *testing.T) {
	policy := security.Policy[User, int64]{
		Authorize: func(context.Context, security.Action) error { return nil },
	}
	for _, tc := range []struct {
		name       string
		middleware []crud.Middleware[User, int64]
	}{
		{"gate outside faults", []crud.Middleware[User, int64]{
			security.Gate(policy), faults.Enrich[User, int64](),
		}},
		{"faults outside gate", []crud.Middleware[User, int64]{
			faults.Enrich[User, int64](), security.Gate(policy),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newNativeBatchSource(crud.Postgres{})
			repository := Users.Bind(source, tc.middleware...)
			if err := repository.InsertBatch(context.Background(), []*User{{
				Email: "portable-through-chain@example.test",
			}}, crud.PortableBatch()); err != nil {
				t.Fatal(err)
			}
			if source.attempts != 0 || len(source.Statements()) != 1 {
				t.Fatalf("portable option was lost: native=%d sql=%v", source.attempts, source.SQL())
			}
		})
	}
}

func TestInsertBatchResolvesTheNativeCapabilityInsideRepositoryTransaction(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	repository := Users.Bind(source)
	rollback := errors.New("roll the import back")
	err := repository.Tx(context.Background(), func(ctx context.Context) error {
		if err := repository.InsertBatch(ctx, []*User{{Email: "inside@example.test"}}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("Tx returned %v, want rollback sentinel", err)
	}
	if source.attempts != 0 || source.tx == nil || len(source.tx.calls) != 1 {
		t.Fatalf("pool attempts=%d tx=%#v; native insert escaped the transaction", source.attempts, source.tx)
	}
	if !source.tx.rolledBack || source.tx.committed {
		t.Fatalf("tx committed/rolled back = %v/%v", source.tx.committed, source.tx.rolledBack)
	}
}

func TestAmbientExecutorWithoutNativeBulkUsesPortableSQLOnThatExecutor(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	ambient := new(plainBatchExecutor)
	ctx := crud.BindExecutor(context.Background(), source, ambient)

	if err := Users.Bind(source).InsertBatch(ctx, []*User{{Email: "portable-in-session@example.test"}}); err != nil {
		t.Fatal(err)
	}
	if source.attempts != 0 || len(source.Statements()) != 0 {
		t.Fatalf("source pool was used: native=%d sql=%v", source.attempts, source.SQL())
	}
	if ambient.execs != 1 {
		t.Fatalf("ambient executor SQL calls = %d, want 1", ambient.execs)
	}
}

func TestUnknownSourceWrapperSeesSingleStatementPortableSQL(t *testing.T) {
	inner := newNativeBatchSource(crud.Postgres{})
	wrapper := &observingSource{Source: inner}
	if err := Users.Bind(wrapper).InsertBatch(context.Background(), []*User{{Email: "observed@example.test"}}); err != nil {
		t.Fatal(err)
	}
	if inner.attempts != 0 {
		t.Fatalf("native capability was invoked under an unknown wrapper %d times", inner.attempts)
	}
	if wrapper.execs != 1 {
		t.Fatalf("wrapper observed %d SQL statements, want 1", wrapper.execs)
	}
}

func TestUnknownSourceWrapperCannotGainNativeBulkInsideRepositoryTransaction(t *testing.T) {
	inner := newNativeBatchSource(crud.Postgres{})
	wrapper := &observingSource{Source: inner}
	repository := Users.Bind(wrapper)

	if err := repository.Tx(context.Background(), func(ctx context.Context) error {
		return repository.InsertBatch(ctx, []*User{{Email: "portable-in-tx@example.test"}})
	}); err != nil {
		t.Fatal(err)
	}
	if inner.attempts != 0 || inner.tx == nil || len(inner.tx.calls) != 0 {
		t.Fatalf("pool attempts=%d tx=%#v; wrapper-hidden native bulk reappeared", inner.attempts, inner.tx)
	}
	if len(inner.Statements()) != 1 || !inner.tx.committed || inner.tx.rolledBack {
		t.Fatalf("portable tx statements=%v tx=%#v", inner.SQL(), inner.tx)
	}
}

func TestRefusingBulkWrapperRemainsAuthoritativeInsideRepositoryTransaction(t *testing.T) {
	inner := newNativeBatchSource(crud.Postgres{})
	wrapper := &refusingBulkSource{observingSource: &observingSource{Source: inner}}
	repository := Users.Bind(wrapper)

	if err := repository.Tx(context.Background(), func(ctx context.Context) error {
		return repository.InsertBatch(ctx, []*User{{Email: "refused-native-in-tx@example.test"}})
	}); err != nil {
		t.Fatal(err)
	}
	if wrapper.attempts != 1 || inner.attempts != 0 || inner.tx == nil || len(inner.tx.calls) != 0 {
		t.Fatalf("wrapper/pool/tx native = %d/%d/%#v", wrapper.attempts, inner.attempts, inner.tx)
	}
	if len(inner.Statements()) != 1 || !inner.tx.committed || inner.tx.rolledBack {
		t.Fatalf("portable tx statements=%v tx=%#v", inner.SQL(), inner.tx)
	}
}

func TestForwardingBulkWrapperKeepsItsInterceptionAndTheRepositoryTransaction(t *testing.T) {
	inner := newNativeBatchSource(crud.Postgres{})
	wrapper := &forwardingBulkSource{observingSource: &observingSource{Source: inner}}
	repository := Users.Bind(wrapper)

	if err := repository.Tx(context.Background(), func(ctx context.Context) error {
		return repository.InsertBatch(ctx, []*User{{Email: "forwarded-native-in-tx@example.test"}})
	}); err != nil {
		t.Fatal(err)
	}
	if wrapper.attempts != 1 || inner.attempts != 0 || inner.tx == nil || len(inner.tx.calls) != 1 {
		t.Fatalf("wrapper/pool/tx native = %d/%d/%#v", wrapper.attempts, inner.attempts, inner.tx)
	}
	if len(inner.Statements()) != 0 || !inner.tx.committed || inner.tx.rolledBack {
		t.Fatalf("native tx statements=%v tx=%#v", inner.SQL(), inner.tx)
	}
}

func TestForwardingBulkWrapperUsesItsReceiverWithoutABinding(t *testing.T) {
	inner := newNativeBatchSource(crud.Postgres{})
	wrapper := &forwardingBulkSource{observingSource: &observingSource{Source: inner}}

	if err := Users.Bind(wrapper).InsertBatch(context.Background(), []*User{{Email: "forwarded-native@example.test"}}); err != nil {
		t.Fatal(err)
	}
	if wrapper.attempts != 1 || inner.attempts != 1 || len(inner.calls) != 1 {
		t.Fatalf("wrapper/receiver native = %d/%d/%d", wrapper.attempts, inner.attempts, len(inner.calls))
	}
	if len(inner.Statements()) != 0 {
		t.Fatalf("unbound native call fell back to SQL: %v", inner.SQL())
	}
}

func TestReadWriteExplicitlyForwardsBulkInsertionToThePrimary(t *testing.T) {
	primary := newNativeBatchSource(crud.Postgres{})
	replica := crudtest.Postgres()
	source := crud.ReadWrite(primary, replica)
	if err := Users.Bind(source).InsertBatch(context.Background(), []*User{{Email: "primary@example.test"}}); err != nil {
		t.Fatal(err)
	}
	if primary.attempts != 1 || len(primary.calls) != 1 {
		t.Fatalf("primary native calls = %d/%d", primary.attempts, len(primary.calls))
	}
	if len(replica.Statements()) != 0 {
		t.Fatalf("batch reached replica: %v", replica.SQL())
	}
}

func TestAdvertisedButUnavailableNativeBulkFallsBackBeforeAnyNativeWrite(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	source.unsupported = true
	if err := Users.Bind(source).InsertBatch(context.Background(), []*User{{Email: "fallback@example.test"}}); err != nil {
		t.Fatal(err)
	}
	if source.attempts != 1 || len(source.calls) != 0 {
		t.Fatalf("native attempts/effects = %d/%d, want one before-I/O refusal", source.attempts, len(source.calls))
	}
	if len(source.Statements()) != 1 {
		t.Fatalf("portable fallback statements = %v", source.SQL())
	}
}

func TestNativeServerFailureNeverFallsThroughToSQL(t *testing.T) {
	source := newNativeBatchSource(crud.Postgres{})
	boom := errors.New("native statement failed")
	source.bulkErr = boom
	err := Users.Bind(source).InsertBatch(context.Background(), []*User{{Email: "failed@example.test"}})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want native failure", err)
	}
	if len(source.Statements()) != 0 {
		t.Fatalf("SQL fallback ran after native I/O failed: %v", source.SQL())
	}
}

func TestInsertBatchPreflightsTheWholeShapeBeforeNativeIO(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []*User
	}{
		{"nil later row", []*User{{Email: "valid@example.test"}, nil}},
		{"mixed key modes", []*User{{Email: "generated@example.test"}, {ID: 9, Email: "assigned@example.test"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newNativeBatchSource(crud.Postgres{})
			if err := Users.Bind(source).InsertBatch(context.Background(), tc.models); err == nil {
				t.Fatal("invalid batch was accepted")
			}
			if source.attempts != 0 || len(source.Statements()) != 0 {
				t.Fatalf("invalid batch reached storage: native=%d sql=%v", source.attempts, source.SQL())
			}
		})
	}
}

func TestGateTreatsEveryInsertBatchRowAsCreateBeforeNativeIO(t *testing.T) {
	type tenantKey struct{}
	var actions []security.Action
	policy := security.Combine(
		security.ScopeField[User, int64]("TenantID", func(ctx context.Context) (any, error) {
			return ctx.Value(tenantKey{}).(int64), nil
		}),
		security.Policy[User, int64]{Authorize: func(_ context.Context, action security.Action) error {
			actions = append(actions, action)
			return nil
		}},
	)
	ctx := context.WithValue(context.Background(), tenantKey{}, int64(7))
	source := newNativeBatchSource(crud.Postgres{})
	repository := Users.Bind(source, security.Gate(policy))

	if err := repository.InsertBatch(ctx, []*User{{ID: 42, Email: "assigned@example.test", TenantID: 7}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(actions, []security.Action{security.Create}) {
		t.Fatalf("actions = %v; assigned insert batch must never become Update", actions)
	}
	if source.attempts != 1 {
		t.Fatalf("allowed batch native attempts = %d", source.attempts)
	}
	call := source.calls[0]
	if !slices.Equal(call.columns, []string{"id", "email", "name", "age", "tenant_id"}) || call.rows[0][0] != int64(42) {
		t.Fatalf("assigned key was lost from native insert: columns=%v row=%#v", call.columns, call.rows[0])
	}

	bad := newNativeBatchSource(crud.Postgres{})
	gated := Users.Bind(bad, security.Gate(policy))
	if err := gated.InsertBatch(ctx, []*User{{Email: "foreign@example.test", TenantID: 8}}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("foreign tenant err = %v, want forbidden", err)
	}
	if bad.attempts != 0 || len(bad.Statements()) != 0 {
		t.Fatalf("refused row reached storage: native=%d sql=%v", bad.attempts, bad.SQL())
	}
}

func TestDefaultOnlyBatchesUsePortableDialectSyntaxAtomically(t *testing.T) {
	postgres := newNativeBatchSource(crud.Postgres{})
	if err := batchDefaultRows.Bind(postgres).InsertBatch(context.Background(), []*batchDefaultOnly{{}, {}}); err != nil {
		t.Fatal(err)
	}
	if postgres.attempts != 0 {
		t.Fatalf("default-only postgres batch attempted native bulk %d times", postgres.attempts)
	}
	if len(postgres.Statements()) != 2 {
		t.Fatalf("postgres default statements = %v", postgres.SQL())
	}
	for _, statement := range postgres.Statements() {
		if statement.SQL != `INSERT INTO "batch_default_rows" DEFAULT VALUES` {
			t.Fatalf("postgres default insert = %s", statement.SQL)
		}
	}
	if postgres.tx == nil || !postgres.tx.committed || postgres.tx.rolledBack {
		t.Fatalf("default-row batch transaction = %#v", postgres.tx)
	}

	mysql := newNativeBatchSource(crud.MySQL{})
	if err := batchDefaultRows.Bind(mysql).InsertBatch(context.Background(), []*batchDefaultOnly{{}}); err != nil {
		t.Fatal(err)
	}
	if mysql.attempts != 0 {
		t.Fatalf("default-only mysql batch attempted native bulk %d times", mysql.attempts)
	}
	if got := mysql.Last().SQL; got != "INSERT INTO `batch_default_rows` () VALUES ()" {
		t.Fatalf("mysql default insert = %s", got)
	}
}
