package jobspg

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/jobs"
)

type transactionTestSource struct {
	database    *sql.DB
	extractable bool
	begins      int
	last        *transactionTestTx
}

func (source *transactionTestSource) DataSource() any { return source.database }
func (*transactionTestSource) Dialect() crud.Dialect  { return crud.Postgres{} }
func (*transactionTestSource) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, errors.New("unexpected database access")
}
func (*transactionTestSource) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, errors.New("unexpected database access")
}
func (source *transactionTestSource) Begin(context.Context) (crud.Tx, error) {
	source.begins++
	transaction := &transactionTestTx{database: source.database}
	if source.extractable {
		transaction.sql = &sql.Tx{}
	}
	source.last = transaction
	return transaction, nil
}

type transactionTestTx struct {
	database  *sql.DB
	sql       *sql.Tx
	commits   int
	rollbacks int
}

func (transaction *transactionTestTx) DataSource() any { return transaction.database }
func (transaction *transactionTestTx) Tx() *sql.Tx     { return transaction.sql }
func (*transactionTestTx) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, errors.New("unexpected database access")
}
func (*transactionTestTx) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, errors.New("unexpected database access")
}
func (transaction *transactionTestTx) Commit(context.Context) error {
	transaction.commits++
	return nil
}
func (transaction *transactionTestTx) Rollback(context.Context) error {
	transaction.rollbacks++
	return nil
}

type transactionTestController struct {
	events *[]string
	err    error
}

func (transactionTestController) Pulse(context.Context) error { return nil }
func (controller transactionTestController) Guard(context.Context, jobs.LeaseFence) error {
	*controller.events = append(*controller.events, "guard")
	return controller.err
}

func TestSpecSourceMustNameTheConfiguredDatabase(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	database := &sql.DB{}
	source := &transactionTestSource{database: database}
	if _, err := New(Spec{DB: database, Source: source, Namespace: namespace, Catalog: catalog}); err != nil {
		t.Fatal(err)
	}
	foreign := &transactionTestSource{database: &sql.DB{}}
	if _, err := New(Spec{DB: database, Source: foreign, Namespace: namespace, Catalog: catalog}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("foreign source = %v", err)
	}
}

func TestPlaceRefusesAnUnextractableAmbientCRUDTransaction(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	database := &sql.DB{}
	source := &transactionTestSource{database: database}
	driver, err := New(Spec{DB: database, Source: source, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	transaction := &transactionTestTx{database: database}
	ctx := crud.WithExecutorFor(t.Context(), source, transaction)
	definition := catalog.Definitions()[0].(*jobs.Definition[string])
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(ctx, queue, definition, "payload"); !errors.Is(err, jobs.ErrUnsupported) {
		t.Fatalf("Enqueue = %v", err)
	}
	if source.begins != 0 || transaction.commits != 0 || transaction.rollbacks != 0 {
		t.Fatalf("ambient transaction calls = begin %d, commit %d, rollback %d", source.begins, transaction.commits, transaction.rollbacks)
	}
}

func TestPlaceRefusesAnAmbientNonTransaction(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	database := &sql.DB{}
	source := &transactionTestSource{database: database}
	driver, err := New(Spec{DB: database, Source: source, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	ctx := crud.WithExecutorFor(t.Context(), source, source)
	definition := catalog.Definitions()[0].(*jobs.Definition[string])
	queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: driver})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(ctx, queue, definition, "payload"); !errors.Is(err, jobs.ErrUnsupported) {
		t.Fatalf("Enqueue = %v", err)
	}
}

func TestInFencedTxOrdersBeforeGuardAndEffect(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	database := &sql.DB{}
	source := &transactionTestSource{database: database, extractable: true}
	driver, err := New(Spec{DB: database, Source: source, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	events := []string{}
	controller := transactionTestController{events: &events}
	err = driver.InFencedTx(t.Context(), controller,
		func(context.Context) error {
			events = append(events, "before")
			return nil
		},
		func(context.Context) error {
			events = append(events, "effect")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"before", "guard", "effect"}) {
		t.Fatalf("events = %v", events)
	}
	if source.begins != 1 || source.last.commits != 1 || source.last.rollbacks != 0 {
		t.Fatalf("transaction calls = begin %d, commit %d, rollback %d", source.begins, source.last.commits, source.last.rollbacks)
	}
}

func TestInFencedTxRollsBackBeforeAnEffectAfterGuardFailure(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	database := &sql.DB{}
	source := &transactionTestSource{database: database, extractable: true}
	driver, err := New(Spec{DB: database, Source: source, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	refusal := errors.New("lease refused")
	events := []string{}
	controller := transactionTestController{events: &events, err: refusal}
	err = driver.InFencedTx(t.Context(), controller,
		func(context.Context) error {
			events = append(events, "before")
			return nil
		},
		func(context.Context) error {
			events = append(events, "effect")
			return nil
		},
	)
	if !errors.Is(err, refusal) {
		t.Fatalf("InFencedTx = %v", err)
	}
	if !slices.Equal(events, []string{"before", "guard"}) {
		t.Fatalf("events = %v", events)
	}
	if source.last.commits != 0 || source.last.rollbacks != 1 {
		t.Fatalf("transaction calls = commit %d, rollback %d", source.last.commits, source.last.rollbacks)
	}
}

func TestInFencedTxRequiresAnExtractableConfiguredSource(t *testing.T) {
	namespace, catalog, _ := testPlacement(t)
	database := &sql.DB{}
	events := []string{}
	controller := transactionTestController{events: &events}
	driver, err := New(Spec{DB: database, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	if err := driver.InFencedTx(t.Context(), controller, nil, func(context.Context) error { return nil }); !errors.Is(err, jobs.ErrUnsupported) {
		t.Fatalf("without source = %v", err)
	}
	source := &transactionTestSource{database: database}
	driver, err = New(Spec{DB: database, Source: source, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	if err := driver.InFencedTx(t.Context(), controller, nil, func(context.Context) error { return nil }); !errors.Is(err, jobs.ErrUnsupported) {
		t.Fatalf("unextractable source = %v", err)
	}
	if source.last == nil || source.last.rollbacks != 1 {
		t.Fatal("unextractable transaction was not rolled back")
	}
}
