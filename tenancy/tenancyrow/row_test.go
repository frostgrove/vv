package tenancyrow_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancyrow"
)

type Invoice struct {
	ID       int64  `db:"id,pk,auto"`
	TenantID string `db:"tenant_id"`
	Number   string `db:"number"`
	Total    int64  `db:"total"`
	Lines    []Line `rel:"has_many,fk=InvoiceID"`
}

type InvoiceUpdate struct {
	Number   *string
	Total    *int64
	TenantID *string
}

var Invoices = sqlrepo.Define[Invoice, int64, InvoiceUpdate]("invoices")

func ownedInvoices(t *testing.T, recorder *crudtest.Recorder, mode tenancyrow.Mode) (*crud.Repo[Invoice, int64, InvoiceUpdate], *tenancy.Authority) {
	t.Helper()
	authority := activeAuthority(t, secretReference)
	return Invoices.Bind(recorder, tenancyrow.Repository[Invoice, int64](authority,
		tenancyrow.Column[Invoice]("TenantID", mode, nil))), authority
}

func bound(t *testing.T, authority *tenancy.Authority) context.Context {
	t.Helper()
	ctx, err := authority.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatalf("cannot bind the scope the test is built on: %v", err)
	}
	return ctx
}

func clauseOf(statement string) string {
	_, clause, _ := strings.Cut(statement, " WHERE ")
	return clause
}

func where(recorder *crudtest.Recorder) string {
	_, clause, _ := strings.Cut(recorder.Last().SQL, " WHERE ")
	for _, tail := range []string{" ORDER BY ", " LIMIT ", " OFFSET "} {
		clause, _, _ = strings.Cut(clause, tail)
	}
	return clause
}

func TestEveryVerbNarrowsToTheScopesOwnTenantAndNoOther(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := bound(t, authority)
	row := []any{int64(1), secretReference, "INV-1", int64(100)}

	for name, call := range map[string]struct {
		push []crudtest.Result
		run  func() error
	}{
		"GetByID": {push: []crudtest.Result{crudtest.Rows(row)}, run: func() error { _, err := invoices.GetByID(ctx, 1); return err }},
		"Get":     {push: []crudtest.Result{crudtest.Rows(row), crudtest.Rows([]any{int64(1)})}, run: func() error { _, err := invoices.Get(ctx); return err }},
		"GetAll":  {push: []crudtest.Result{crudtest.Rows(row)}, run: func() error { _, err := invoices.GetAll(ctx); return err }},
		"First":   {push: []crudtest.Result{crudtest.Rows(row)}, run: func() error { _, err := invoices.First(ctx); return err }},
		"Count":   {push: []crudtest.Result{crudtest.Rows([]any{int64(1)})}, run: func() error { _, err := invoices.Count(ctx); return err }},
		"Exists":  {push: []crudtest.Result{crudtest.Rows([]any{int64(1)})}, run: func() error { _, err := invoices.Exists(ctx); return err }},
		"Aggregate": {push: []crudtest.Result{crudtest.Rows([]any{secretReference, int64(1)})}, run: func() error {
			_, err := invoices.Aggregate(ctx, crud.Aggregate(crud.CountAll("n")), crud.GroupBy("TenantID"))
			return err
		}},
		"UpdateAll": {push: []crudtest.Result{crudtest.Rows(row), crudtest.Rows(row)}, run: func() error {
			number := "INV-rewritten"
			_, err := invoices.UpdateAll(ctx, InvoiceUpdate{Number: &number})
			return err
		}},
		"DeleteAll": {push: []crudtest.Result{crudtest.Rows(row), crudtest.Rows(row)}, run: func() error {
			_, err := invoices.DeleteAll(ctx)
			return err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			recorder.Reset()
			recorder.Push(call.push...)
			if err := call.run(); err != nil && !errors.Is(err, crud.ErrNotFound) {
				t.Fatalf("%s: %v", name, err)
			}
			statements := recorder.Statements()
			if len(statements) == 0 {
				t.Fatalf("%s ran no statement, so this proves nothing", name)
			}
			for _, statement := range statements {
				clause := clauseOf(statement.SQL)
				if !strings.Contains(clause, `"tenant_id" = $`) {
					t.Fatalf("%s narrows on %q, which does not name the ownership column", name, clause)
				}
				if !slices.Contains(statement.Args, any(secretReference)) {
					t.Fatalf("%s narrows to %v, and the scope names %q", name, statement.Args, secretReference)
				}
			}
		})
	}
}

// The mutating statement of a bulk verb is compiled as the scope AND the identity
// of every row that was read first, and that identity spells out each column of
// the rows the recorder handed back — the ownership column among them. So the
// assertion is on the scope conjunct the narrowing prepends, and on the argument
// bound to it: "the clause mentions tenant_id somewhere" is satisfied by the
// fixture the test itself pushed.
func mutatingStatement(t *testing.T, recorder *crudtest.Recorder) crudtest.Statement {
	t.Helper()
	var mutating crudtest.Statement
	for _, statement := range recorder.Statements() {
		if strings.HasPrefix(statement.SQL, "DELETE") || strings.HasPrefix(statement.SQL, "UPDATE") {
			mutating = statement
		}
	}
	if mutating.SQL == "" {
		t.Fatalf("no mutating statement was issued at all, so a narrowing assertion over it would prove nothing: %v", recorder.SQL())
	}
	return mutating
}

func assertNarrowedToTheScope(t *testing.T, statement crudtest.Statement) {
	t.Helper()
	clause := clauseOf(statement.SQL)
	rest, scoped := strings.CutPrefix(clause, `("tenant_id" = $`)
	if !scoped {
		t.Fatalf("the narrowing is not the leading conjunct, so the statement is not scoped by it: %s", statement.SQL)
	}
	placeholder, _, _ := strings.Cut(rest, " ")
	at, err := strconv.Atoi(placeholder)
	if err != nil || at > len(statement.Args) {
		t.Fatalf("the leading conjunct binds $%s and the statement carries %d arguments: %s", placeholder, len(statement.Args), statement.SQL)
	}
	if statement.Args[at-1] != any(secretReference) {
		t.Fatalf("the leading conjunct binds %v, not the scope's own tenant", statement.Args[at-1])
	}
}

func TestABulkMutationReachesTheDatabaseNarrowed(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := bound(t, authority)
	row := []any{int64(1), secretReference, "INV-1", int64(100)}
	number := "INV-rewritten"

	for name, verb := range map[string]func() error{
		"DeleteAll": func() error { _, err := invoices.DeleteAll(ctx); return err },
		"UpdateAll": func() error { _, err := invoices.UpdateAll(ctx, InvoiceUpdate{Number: &number}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			recorder.Reset()
			recorder.Push(crudtest.Rows(row), crudtest.Rows(row))
			if err := verb(); err != nil {
				t.Fatal(err)
			}
			assertNarrowedToTheScope(t, mutatingStatement(t, recorder))
		})
	}
}

func TestNoVerbRunsWithoutAVerifiedScope(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, _ := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := context.Background()

	number := "INV-1"
	for name, call := range map[string]func() error{
		"GetByID":     func() error { _, err := invoices.GetByID(ctx, 1); return err },
		"Get":         func() error { _, err := invoices.Get(ctx); return err },
		"GetAll":      func() error { _, err := invoices.GetAll(ctx); return err },
		"First":       func() error { _, err := invoices.First(ctx); return err },
		"Count":       func() error { _, err := invoices.Count(ctx); return err },
		"Exists":      func() error { _, err := invoices.Exists(ctx); return err },
		"Aggregate":   func() error { _, err := invoices.Aggregate(ctx, crud.Aggregate(crud.CountAll("n"))); return err },
		"Save":        func() error { _, err := invoices.Save(ctx, &Invoice{Number: "x"}); return err },
		"SaveOnly":    func() error { return invoices.SaveOnly(ctx, &Invoice{Number: "x"}) },
		"SaveAll":     func() error { return invoices.SaveAll(ctx, []*Invoice{{Number: "x"}}) },
		"InsertBatch": func() error { return invoices.InsertBatch(ctx, []*Invoice{{Number: "x"}}) },
		"Update":      func() error { _, err := invoices.Update(ctx, 1, InvoiceUpdate{Number: &number}); return err },
		"UpdateAll":   func() error { _, err := invoices.UpdateAll(ctx, InvoiceUpdate{Number: &number}); return err },
		"Delete":      func() error { _, err := invoices.Delete(ctx, 1); return err },
		"DeleteAll":   func() error { _, err := invoices.DeleteAll(ctx); return err },
	} {
		t.Run(name, func(t *testing.T) {
			recorder.Reset()
			recorder.Push(crudtest.Rows(), crudtest.Rows(), crudtest.Rows())

			err := call()
			if !errors.Is(err, tenancy.ErrNoScope) {
				t.Fatalf("%s answered %v, want ErrNoScope", name, err)
			}
			if len(recorder.Statements()) != 0 {
				t.Fatalf("%s reached the database with no tenant: %v", name, recorder.SQL())
			}
		})
	}
}

func TestACreateIsStampedWithTheScopesTenant(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := bound(t, authority)

	recorder.Push(crudtest.Rows([]any{int64(7), secretReference, "INV-1", int64(100)}))
	stored, err := invoices.Save(ctx, &Invoice{Number: "INV-1", Total: 100})
	if err != nil {
		t.Fatalf("a create with no tenant on the row was refused instead of being stamped: %v", err)
	}
	if stored.TenantID != secretReference {
		t.Fatalf("the stored row is owned by %q", stored.TenantID)
	}
	if !strings.Contains(recorder.Last().String(), secretReference) {
		t.Fatalf("the INSERT does not carry the tenant: %s", recorder.Last())
	}
}

func TestACreateForAnotherTenantIsRefusedWhicheverModeIsDeclared(t *testing.T) {
	for name, mode := range map[string]tenancyrow.Mode{"derive": tenancyrow.Derive, "validate": tenancyrow.Validate} {
		t.Run(name, func(t *testing.T) {
			recorder := crudtest.Postgres()
			invoices, authority := ownedInvoices(t, recorder, mode)
			ctx := bound(t, authority)

			recorder.Push(crudtest.Rows([]any{int64(7), "globex-1a2b", "INV-1", int64(0)}))
			if _, err := invoices.Save(ctx, &Invoice{TenantID: "globex-1a2b", Number: "INV-1"}); !errors.Is(err, crud.ErrForbidden) {
				t.Fatalf("err = %v — a client planted a row in another tenant by setting a field", err)
			}
			if len(recorder.Statements()) != 0 {
				t.Fatalf("the row reached the database: %v", recorder.SQL())
			}
		})
	}
}

func TestValidateRefusesACreateThatOmitsOwnership(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Validate)
	ctx := bound(t, authority)

	recorder.Push(crudtest.Rows([]any{int64(7), "", "INV-1", int64(0)}))
	if _, err := invoices.Save(ctx, &Invoice{Number: "INV-1"}); !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("err = %v — a policy that says it validates ownership stamped it instead", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("the row reached the database: %v", recorder.SQL())
	}
}

func TestAMutationCannotMoveARowBetweenTenants(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := bound(t, authority)
	foreign := "globex-1a2b"

	recorder.Push(crudtest.Rows([]any{int64(7), secretReference, "INV-1", int64(100)}))
	if _, err := invoices.Update(ctx, 7, InvoiceUpdate{TenantID: &foreign}); !errors.Is(err, crud.ErrForbidden) {
		t.Fatalf("err = %v — an update moved a row out of its tenant", err)
	}
	for _, statement := range recorder.SQL() {
		if strings.HasPrefix(statement, "UPDATE") {
			t.Fatalf("the move reached the database: %s", statement)
		}
	}
}

func TestAnUnscopedBulkMutationIsRefusedRatherThanWidened(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Derive)
	number := "INV-rewritten"
	row := []any{int64(1), secretReference, "INV-1", int64(100)}

	recorder.Push(crudtest.Rows(row), crudtest.Rows(row))
	if _, err := invoices.UpdateAll(context.Background(), InvoiceUpdate{Number: &number}); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — a bulk update with no tenant was widened to every tenant instead of refused", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("an unscoped bulk update reached the database: %v", recorder.SQL())
	}

	recorder.Reset()
	recorder.Push(crudtest.Rows(row), crudtest.Rows(row))
	if _, err := invoices.UpdateAll(bound(t, authority), InvoiceUpdate{Number: &number}); err != nil {
		t.Fatalf("the control refuses too, so the assertion above proves nothing: %v", err)
	}
	assertNarrowedToTheScope(t, mutatingStatement(t, recorder))
}

type Line struct {
	ID        int64    `db:"id,pk,auto"`
	TenantID  string   `db:"tenant_id"`
	InvoiceID int64    `db:"invoice_id"`
	Owner     *Invoice `rel:"belongs_to,fk=InvoiceID"`
	Text      string   `db:"text"`
}

type LineUpdate struct{ Text *string }

var Lines = sqlrepo.Define[Line, int64, LineUpdate]("lines")

func TestARowOwnedThroughARelationIsNarrowedByThatRelation(t *testing.T) {
	recorder := crudtest.Postgres()
	authority := activeAuthority(t, secretReference)
	lines := Lines.Bind(recorder, tenancyrow.Repository[Line, int64](authority,
		tenancyrow.Through[Line]("Owner", "TenantID", nil)))
	ctx := bound(t, authority)

	recorder.Push(crudtest.Rows(), crudtest.Rows())
	if _, err := lines.GetAll(ctx); err != nil {
		t.Fatalf("a read of rows owned through a relation was refused: %v", err)
	}
	statement := recorder.Last()
	if !strings.Contains(statement.SQL, "tenant_id") || !strings.Contains(statement.SQL, "invoices") {
		t.Fatalf("the read does not reach the owner through the relation: %s", statement.SQL)
	}
	// The owner's tenant is the only value this read binds — a `Through` strategy
	// contributes both the scope predicate and the relation scope, and each
	// compiles into the same correlated `EXISTS`. Asserting that the tenant is
	// among the arguments would therefore pass while one of the two named
	// somebody else, which is the whole failure being ruled out here.
	if len(statement.Args) == 0 {
		t.Fatalf("the correlated read binds nothing, so it narrows to nobody: %s", statement.SQL)
	}
	for _, argument := range statement.Args {
		if argument != any(secretReference) {
			t.Fatalf("the correlated read binds %v — one of its conjuncts names another tenant", statement.Args)
		}
	}

	t.Run("and a create is refused rather than admitted unchecked", func(t *testing.T) {
		recorder.Reset()
		recorder.Push(crudtest.Rows([]any{int64(1), int64(7), "x"}))
		if _, err := lines.Save(ctx, &Line{InvoiceID: 7, Text: "x"}); !errors.Is(err, crud.ErrForbidden) {
			t.Fatalf("err = %v — a child row was created without its parent being read", err)
		}
		if len(recorder.Statements()) != 0 {
			t.Fatalf("the create reached the database: %v", recorder.SQL())
		}
	})
}

func TestATenantSeesEveryRowItOwns(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, authority := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := bound(t, authority)

	recorder.Push(crudtest.Rows(
		[]any{int64(1), secretReference, "INV-1", int64(100)},
		[]any{int64(2), secretReference, "INV-2", int64(200)},
		[]any{int64(3), secretReference, "INV-3", int64(300)},
	))
	rows, err := invoices.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("the tenant was handed %d of its own three rows — a predicate that narrows to nothing satisfies every isolation assertion in this file", len(rows))
	}
	if clause := where(recorder); clause != `"tenant_id" = $1` {
		t.Fatalf("the read carries %q — anything beyond the ownership equality is a row of its own the tenant cannot see", clause)
	}
}

func TestAPreloadOfADeclaredRelationCarriesTheTenant(t *testing.T) {
	recorder := crudtest.Postgres()
	authority := activeAuthority(t, secretReference)
	invoices := Invoices.Bind(recorder, tenancyrow.Repository[Invoice, int64](authority,
		tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil,
			tenancyrow.Relation{Path: "Lines", Field: "TenantID"})))
	ctx := bound(t, authority)

	recorder.Push(
		crudtest.Rows([]any{int64(1), secretReference, "INV-1", int64(100)}),
		crudtest.Rows([]any{int64(9), "globex-1a2b", int64(1), "a line another tenant owns"}),
	)
	if _, err := invoices.GetAll(ctx, crud.Preload("Lines")); err != nil {
		t.Fatal(err)
	}
	statements := recorder.Statements()
	if len(statements) < 2 {
		t.Fatalf("the preload issued no second statement: %v", recorder.SQL())
	}
	if !strings.Contains(clauseOf(statements[1].SQL), "tenant_id") {
		t.Fatalf("the preload read the child table raw: %s", statements[1].SQL)
	}
	if !slices.Contains(statements[1].Args, any(secretReference)) {
		t.Fatalf("the preload narrows the child table to %v rather than to the scope's own tenant", statements[1].Args)
	}

	// The consequence the interface documents, pinned rather than described: a
	// strategy that narrows relations and answers "no" is given no relation-scope
	// function, and its children are read whole. It is the one obligation the gate
	// below cannot check for a strategy somebody else wrote.
	t.Run("and a strategy that narrows relations while denying it is believed", func(t *testing.T) {
		dishonest := crudtest.Postgres()
		lying := Invoices.Bind(dishonest, tenancyrow.Repository[Invoice, int64](authority,
			silentOwnership{tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil,
				tenancyrow.Relation{Path: "Lines", Field: "TenantID"})}))

		dishonest.Push(
			crudtest.Rows([]any{int64(1), secretReference, "INV-1", int64(100)}),
			crudtest.Rows([]any{int64(9), "globex-1a2b", int64(1), "a line another tenant owns"}),
		)
		if _, err := lying.GetAll(bound(t, authority), crud.Preload("Lines")); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(clauseOf(dishonest.SQL()[1]), "tenant_id") {
			t.Fatalf("the preload was narrowed anyway, so NarrowsRelations is not what decides it: %s", dishonest.SQL()[1])
		}
	})

	t.Run("and an undeclared relation is read whole, which is why the declaration is mandatory", func(t *testing.T) {
		plain := crudtest.Postgres()
		undeclared := Invoices.Bind(plain, tenancyrow.Repository[Invoice, int64](authority,
			tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil)))

		plain.Push(
			crudtest.Rows([]any{int64(1), secretReference, "INV-1", int64(100)}),
			crudtest.Rows([]any{int64(9), "globex-1a2b", int64(1), "a line another tenant owns"}),
		)
		if _, err := undeclared.GetAll(bound(t, authority), crud.Preload("Lines")); err != nil {
			t.Fatal(err)
		}
		second := plain.SQL()[1]
		if strings.Contains(clauseOf(second), "tenant_id") {
			t.Fatalf("the control is narrowed too, so the assertion above proves nothing: %s", second)
		}
	})
}

func TestATransactionOpensButCarriesNoTenantWorkWithoutAScope(t *testing.T) {
	recorder := crudtest.Postgres()
	invoices, _ := ownedInvoices(t, recorder, tenancyrow.Derive)
	ctx := context.Background()

	// Tx is the one Core verb the gate inherits rather than overrides, with the
	// reason written down in D-030: it touches no row itself, and everything the
	// closure does reaches the database through the same gated repository. The
	// assertion is therefore not that Tx refuses, but that nothing inside it can
	// act for a tenant nobody verified.
	reached := false
	err := invoices.Tx(ctx, func(inner context.Context) error {
		reached = true
		_, err := invoices.GetAll(inner)
		return err
	})
	if !reached {
		t.Fatal("the closure never ran, so this asserts nothing about what is inside it")
	}
	if !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — a statement inside an unscoped transaction reached the database", err)
	}
	for _, statement := range recorder.SQL() {
		if strings.Contains(statement, "invoices") {
			t.Fatalf("a tenant statement ran inside an unscoped transaction: %s", statement)
		}
	}
}

type erroringOwnership struct{ tenancyrow.Ownership[Invoice] }

func (this erroringOwnership) Relations(tenancy.Reference) (*crud.RelationScopes, error) {
	return nil, errors.New("the directory this strategy reads is unavailable")
}

func TestAStrategyWhoseRelationsFailAreARefusalRatherThanNoNarrowing(t *testing.T) {
	recorder := crudtest.Postgres()
	authority := activeAuthority(t, secretReference)
	declared := tenancyrow.Column[Invoice]("TenantID", tenancyrow.Derive, nil,
		tenancyrow.Relation{Path: "Lines", Field: "TenantID"})
	invoices := Invoices.Bind(recorder, tenancyrow.Repository[Invoice, int64](authority,
		erroringOwnership{Ownership: declared}))

	recorder.Push(crudtest.Rows(), crudtest.Rows())
	if _, err := invoices.GetAll(bound(t, authority), crud.Preload("Lines")); err == nil {
		t.Fatal("a strategy that could not say how to narrow its relations read them anyway")
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("a statement ran for a narrowing nobody could compute: %v", recorder.SQL())
	}
}

type silentOwnership struct{ tenancyrow.Ownership[Invoice] }

func (silentOwnership) NarrowsRelations() bool { return false }
