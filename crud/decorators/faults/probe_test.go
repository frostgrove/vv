package faults_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/probe"
	"github.com/frostgrove/vv/errs"
)

// docsCatalog is the schema the probe reads. Two unique keys, so a test can tell
// "the probe ran" from "the probe found the one thing it was told about".
func docsCatalog() catalog.Catalog {
	return &fakeCatalog{tables: []catalog.Table{{
		Name:   "docs",
		Schema: "public",
		Columns: []catalog.Column{
			{Name: "id"}, {Name: "tenant_id"}, {Name: "title"}, {Name: "body"},
		},
		PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{
			{Name: "docs_pkey", Table: "docs", Kind: catalog.KindPrimaryKey, Columns: []string{"id"}},
			{Name: "docs_title_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"title"}},
			{Name: "docs_body_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"body"}},
		},
	}}}
}

type fakeCatalog struct{ tables []catalog.Table }

func (this *fakeCatalog) Dialect() string { return "postgres" }

func (this *fakeCatalog) Table(name string) (*catalog.Table, bool) {
	for i := range this.tables {
		if this.tables[i].Name == name {
			return &this.tables[i], true
		}
	}
	return nil, false
}

func (this *fakeCatalog) Constraint(table, name string) (*catalog.Constraint, bool) {
	t, ok := this.Table(table)
	if !ok {
		return nil, false
	}
	return t.Constraint(name)
}

// stub is a datasource that refuses writes and answers the probe. It is written
// out rather than driven off crudtest.Recorder because these tests have to tell
// the write's own statement from the probe's, and both arrive through Query on
// a dialect with RETURNING.
type stub struct {
	mu     sync.Mutex
	d      crud.Dialect
	stmts  []string
	probes int
	depth  int
	fail   error
	cells  []any
}

func newStub(fail error, cells ...any) *stub {
	return &stub{d: crud.Postgres{}, fail: fail, cells: cells}
}

func (this *stub) Dialect() crud.Dialect { return this.d }

// The probe's own statement, told from the write's. A bulk probe leads with the
// row index rather than with the first term, so a prefix test misses it.
func (this *stub) isProbe(q string) bool { return strings.Contains(q, "EXISTS(SELECT 1 FROM") }

func (this *stub) record(q string) bool {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.stmts = append(this.stmts, q)
	if this.isProbe(q) {
		this.probes++
		return true
	}
	return false
}

func (this *stub) Exec(_ context.Context, q string, _ ...any) (crud.Result, error) {
	if this.record(q) {
		return crud.Result{}, nil
	}
	return crud.Result{}, this.fail
}

func (this *stub) Query(_ context.Context, q string, _ ...any) (crud.Rows, error) {
	if this.record(q) {
		return &oneRow{cells: this.cells}, nil
	}
	return nil, this.fail
}

func (this *stub) Begin(context.Context) (crud.Tx, error) {
	this.mu.Lock()
	this.depth++
	this.mu.Unlock()
	return stubTx{this}, nil
}

func (this *stub) probeCount() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.probes
}

func (this *stub) beginCount() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.depth
}

type stubTx struct{ *stub }

func (this stubTx) Commit(context.Context) error   { return nil }
func (this stubTx) Rollback(context.Context) error { return nil }

type oneRow struct {
	cells []any
	done  bool
}

func (this *oneRow) Next() bool {
	if this.done {
		return false
	}
	this.done = true
	return true
}

func (this *oneRow) Err() error { return nil }
func (this *oneRow) Close()     {}

func (this *oneRow) Scan(dest ...any) error {
	for i := range dest {
		if p, ok := dest[i].(*any); ok && i < len(this.cells) {
			*p = this.cells[i]
		}
	}
	return nil
}

func uniqueFault() error {
	return errs.Conflict().Code(errs.CodeUnique).
		General().Code(errs.CodeUnique).Origin(errs.OriginState).
		Source(errs.Source{Table: "docs", Constraint: "docs_title_uk", Columns: []string{"title"}}).
		Wrapping(crud.ErrConflict).Fault()
}

func paths(err error) []string {
	f, ok := errs.AsFault(err)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(f.Violations))
	for _, v := range f.Violations {
		out = append(out, string(v.Code)+"@"+v.Path.String())
	}
	return out
}

func TestFullIsTheDefaultForSaveAndUpdateAndSimpleForTheBulkVerbs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []faults.Option
		save    int
		batch   int
	}{
		{"the default set", []faults.Option{faults.WithProbe(probe.Full(docsCatalog()))}, 1, 0},
		// The control: naming the verb flips exactly one of them.
		{"a bulk override", []faults.Option{
			faults.WithProbe(probe.Full(docsCatalog())),
			faults.WithProbeFor("SaveAll", probe.Full(docsCatalog())),
		}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newStub(uniqueFault(), false, false)
			repository := Docs.Bind(source, faults.Enrich[Doc, int64](tc.options...))
			if err := repository.SaveOnly(context.Background(), &Doc{Title: "a", Body: "b"}); err == nil {
				t.Fatal("the write was supposed to fail")
			}
			if got := source.probeCount(); got != tc.save {
				t.Fatalf("Save ran %d probes, want %d", got, tc.save)
			}

			batchSrc := newStub(uniqueFault(), int64(0), false, false)
			batch := Docs.Bind(batchSrc, faults.Enrich[Doc, int64](tc.options...))
			if err := batch.SaveAll(context.Background(), []*Doc{{Title: "a", Body: "b"}}); err == nil {
				t.Fatal("the batch write was supposed to fail")
			}
			if got := batchSrc.probeCount(); got != tc.batch {
				t.Fatalf("SaveAll ran %d probes, want %d", got, tc.batch)
			}
		})
	}
}

// The probe's violations go through the same column-to-field hop the driver's
// do, and exactly once.
func TestTheProbesViolationsGetTheSameFieldHopTheDriversDoes(t *testing.T) {
	source := newStub(uniqueFault(), false, true) // the body key, not the title one
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(docsCatalog()))))

	err := repository.SaveOnly(context.Background(), &Doc{Title: "a", Body: "b"})
	got := paths(err)
	if len(got) != 2 {
		t.Fatalf("violations = %v, want the driver's and the probe's", got)
	}
	want := map[string]bool{"unique@Title": true, "unique@Body": true}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("violations = %v: %s is not a model field path", got, g)
		}
	}
}

// The control for the hop: a probe violation over another table is marked
// approximate rather than translated onto a field of this model.
func TestAProbeViolationNamingAnotherTableIsMarkedApproximate(t *testing.T) {
	source := newStub(uniqueFault(), false, false)
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(docsCatalog()))))
	_ = repository

	// The decorator's own hop, exercised through the fault it produces for a
	// driver violation on a table this model is not bound to.
	other := errs.Conflict().Code(errs.CodeUnique).
		General().Code(errs.CodeUnique).Origin(errs.OriginState).
		Source(errs.Source{Table: "audit_log", Constraint: "x", Columns: []string{"title"}}).
		Wrapping(crud.ErrConflict).Fault()
	src2 := newStub(other, false, false)
	err := Docs.Bind(src2, faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(docsCatalog())))).
		SaveOnly(context.Background(), &Doc{Title: "a", Body: "b"})
	f, ok := errs.AsFault(err)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	for _, v := range f.Violations {
		if v.Source.Table == "audit_log" {
			if len(v.Path) > 0 || !v.Approximate {
				t.Fatalf("a violation on another table was translated onto this model: %+v", v)
			}
			return
		}
	}
	t.Fatal("the driver's violation disappeared")
}

func TestAProbeIsNotRunForAnErrorThatIsNotAFault(t *testing.T) {
	boom := errors.New("the pool is closed")
	source := newStub(boom, false, false)
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(docsCatalog()))))

	err := repository.SaveOnly(context.Background(), &Doc{Title: "a", Body: "b"})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the original", err)
	}
	if _, ok := errs.AsFault(err); ok {
		t.Fatal("the decorator invented a fault for an error that was not one")
	}
	if n := source.probeCount(); n != 0 {
		t.Fatalf("%d probes ran for an error nothing classified", n)
	}
}

// A probe finds its datasource through a decorator sitting between it and the
// repository, so the order the two are listed in is not what decides whether it
// works.
//
// This used to refuse. The probe asserted crud.Sourced on the layer directly
// below it, and an interface embedded in a struct promotes only its own method
// set — so the gate, which forwards everything a Core names and nothing else,
// answered no. The chain always knew; only the type system did not. crud.SourceOf
// walks it through Next().
func TestAProbeFindsItsSourceThroughADecoratorAboveTheRepository(t *testing.T) {
	source := newStub(uniqueFault(), false, false)
	repository := Docs.Bind(source,
		faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(docsCatalog()))),
		security.Gate(security.Freeze[Doc, int64]("Title")))

	// Binding is half the claim. The probe has to actually run, or a source
	// that resolved to something unusable would pass this just as well.
	if err := repository.SaveOnly(context.Background(), &Doc{Title: "a", Body: "b"}); err == nil {
		t.Fatal("the write was supposed to fail with the stub's unique violation")
	}
	if n := source.probeCount(); n == 0 {
		t.Fatal("the probe bound but never ran its own statement")
	}
}

// A probe over a chain that genuinely cannot say what it wraps still refuses at
// Bind time.
//
// The control on the walk. Without it, every assertion above would hold for a
// SourceOf that answered "yes" to anything, and a probe with no datasource would
// go back to failing at the first collision instead of at start-up ([[D-021]]).
func TestADeclaredProbeWithNoReachableSourceRefusesAtBindTime(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a probe with nothing to run its statement on bound quietly")
		}
	}()
	source := newStub(uniqueFault(), false, false)
	Docs.Bind(source,
		faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(docsCatalog()))),
		opaque[Doc, int64]())
}

// opaque is a decorator that forwards no optional interface and does not say
// what it wraps — the shape crud.Base exists to stop anyone writing by accident.
func opaque[M any, ID comparable]() crud.Middleware[M, ID] {
	return func(next crud.Core[M, ID]) crud.Core[M, ID] { return opaqueCore[M, ID]{next} }
}

type opaqueCore[M any, ID comparable] struct{ crud.Core[M, ID] }

// The control: named explicitly, any order binds, including through opaque.
func TestAProbeWithAnExplicitSourceBindsWhereverItSits(t *testing.T) {
	source := newStub(uniqueFault(), false, false)
	Docs.Bind(source,
		faults.Enrich[Doc, int64](
			faults.WithProbe(probe.Full(docsCatalog())),
			faults.WithSource(source)),
		opaque[Doc, int64]())
}

func TestADeclarationAgainstACatalogWithoutTheTableRefusesAtBindTime(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a probe over a table the catalog does not know bound quietly")
		}
	}()
	source := newStub(uniqueFault(), false, false)
	Docs.Bind(source, faults.Enrich[Doc, int64](faults.WithProbe(probe.Full(&fakeCatalog{}))))
}

func TestPastTheSavepointBudgetTheAnswerIsPartial(t *testing.T) {
	cat := docsCatalog()
	source := newStub(uniqueFault(), false, false)
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](
		faults.WithProbe(probe.Full(cat, probe.WithSavepoints(), probe.WithMaxSavepoints(1)))))

	var first, second error
	err := crud.InTx(context.Background(), source, func(ctx context.Context) error {
		first = repository.SaveOnly(ctx, &Doc{Title: "a", Body: "b"})
		second = repository.SaveOnly(ctx, &Doc{Title: "c", Body: "d"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f1, _ := errs.AsFault(first)
	f2, _ := errs.AsFault(second)
	if f1 == nil || f2 == nil {
		t.Fatalf("one of the writes did not fail: %v / %v", first, second)
	}
	if f1.Partial {
		t.Fatal("the first write fitted inside the budget and was reported as incomplete")
	}
	if !f2.Partial {
		t.Fatal("the second write was past the savepoint budget and said nothing about it")
	}
	// The control that the budget did the refusing: one savepoint was taken and
	// not two. The outer count is InTx's own Begin.
	if got := source.beginCount(); got != 2 {
		t.Fatalf("%d transactions were begun, want the outer one plus exactly one savepoint", got)
	}
}

// A foreign transaction is never given a savepoint, whatever WithSavepoints
// says.
func TestAForeignTransactionIsNeverGivenASavepoint(t *testing.T) {
	source := newStub(uniqueFault(), false, false)
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](
		faults.WithProbe(probe.Full(docsCatalog(), probe.WithSavepoints()))))

	ctx := crud.WithExecutor(context.Background(), source)
	if err := repository.SaveOnly(ctx, &Doc{Title: "a", Body: "b"}); err == nil {
		t.Fatal("the write was supposed to fail")
	}
	if got := source.beginCount(); got != 0 {
		t.Fatalf("%d savepoints were taken inside somebody else's transaction", got)
	}
	if got := source.probeCount(); got != 0 {
		t.Fatalf("the probe ran %d times inside a foreign transaction on an engine that poisons it", got)
	}
}

// The control: our own transaction does get one, and the probe does run.
func TestOurOwnTransactionIsGivenASavepointAndTheProbeRuns(t *testing.T) {
	source := newStub(uniqueFault(), false, true)
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](
		faults.WithProbe(probe.Full(docsCatalog(), probe.WithSavepoints()))))

	var got error
	if err := crud.InTx(context.Background(), source, func(ctx context.Context) error {
		got = repository.SaveOnly(ctx, &Doc{Title: "a", Body: "b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if source.beginCount() != 2 {
		t.Fatalf("%d transactions were begun, want the outer one plus a savepoint", source.beginCount())
	}
	if source.probeCount() != 1 {
		t.Fatalf("the probe ran %d times inside a restored transaction", source.probeCount())
	}
	if p := paths(got); len(p) != 2 {
		t.Fatalf("violations = %v, want the driver's and the probe's", p)
	}
}

// Without the savepoint mode the same write inside the same transaction
// degrades to one violation rather than erroring.
func TestWithoutSavepointsATransactionDegradesRatherThanErroring(t *testing.T) {
	source := newStub(uniqueFault(), false, true)
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](
		faults.WithProbe(probe.Full(docsCatalog()))))

	var got error
	if err := crud.InTx(context.Background(), source, func(ctx context.Context) error {
		got = repository.SaveOnly(ctx, &Doc{Title: "a", Body: "b"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if source.beginCount() != 1 {
		t.Fatalf("%d transactions were begun with the savepoint mode off", source.beginCount())
	}
	if source.probeCount() != 0 {
		t.Fatal("the probe ran inside a poisoned transaction")
	}
	if p := paths(got); len(p) != 1 {
		t.Fatalf("violations = %v, want the driver's alone", p)
	}
	if !errors.Is(got, crud.ErrConflict) {
		t.Fatalf("the classification was lost: %v", got)
	}
}

func TestAProbeFailureIsHandedToTheCallerAndNotToTheClient(t *testing.T) {
	source := &failingProbe{stub: newStub(uniqueFault(), false, false)}
	var seen error
	repository := Docs.Bind(source, faults.Enrich[Doc, int64](
		faults.WithProbe(probe.Full(docsCatalog())),
		faults.WithProbeError(func(_ string, err error) { seen = err })))

	err := repository.SaveOnly(context.Background(), &Doc{Title: "a", Body: "b"})
	if seen == nil {
		t.Fatal("the probe's failure reached nobody")
	}
	f, ok := errs.AsFault(err)
	if !ok || len(f.Violations) != 1 || !f.Partial {
		t.Fatalf("a failed probe did not keep the 409 and mark it incomplete: %v", err)
	}
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatalf("a failed probe downgraded the classification: %v", err)
	}
}

type failingProbe struct{ *stub }

func (this *failingProbe) Query(ctx context.Context, q string, args ...any) (crud.Rows, error) {
	if this.isProbe(q) {
		this.record(q)
		return nil, errors.New("the probe statement was refused")
	}
	return this.stub.Query(ctx, q, args...)
}
