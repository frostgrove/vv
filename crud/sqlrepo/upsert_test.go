package sqlrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

func noStatementMentions(t *testing.T, rec *crudtest.Recorder, fragment, why string) {
	t.Helper()
	for _, statement := range rec.SQL() {
		if strings.Contains(statement, fragment) {
			t.Fatalf("%s\nstatement: %s", why, statement)
		}
	}
}

type budgetedMySQL struct {
	crud.MySQL
	limit int
}

func (this budgetedMySQL) MaxBindValues() int { return this.limit }

func TestAConditionalSaveIsRefusedOverBudgetBeforeAnyStatement(t *testing.T) {
	rec := crudtest.New(budgetedMySQL{limit: 4})

	err := Users.Bind(rec).SaveOnly(context.Background(), &User{ID: 1, Email: "one@x"})
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) || !strings.Contains(schemaErr.Reason, "Save needs 5 bound values") {
		t.Fatalf("err = %T %v, want the statement-wide Save budget refusal", err, err)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("an oversized Save reached the datasource with %d statements: %v", n, rec.SQL())
	}
}

func TestSaveNeverReachesARowByAKeyTheCallerDidNotName(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(crudtest.Rows([]any{"abc", "stored"}))

	d := Doc{ID: "abc", Title: "mine"}
	if _, err := Docs.Bind(rec).Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	noStatementMentions(t, rec, "ON DUPLICATE KEY UPDATE",
		"a targetless upsert fires on every unique index, so a row with a different id but the same email is overwritten in place of the row the caller named")
	wantSQL(t, mustSQL(t, rec, 0).SQL, "UPDATE `docs` SET `title` = ? WHERE `id` = ?")
	if got := mustSQL(t, rec, 0).Args[1]; got != "abc" {
		t.Fatalf("the update was aimed at %#v, want the primary key the caller set", got)
	}
	if rec.TxDepth() != 1 {
		t.Fatalf("the update/probe/insert sequence ran in %d transactions, want exactly one", rec.TxDepth())
	}
}

func TestSaveInsertsWhenNoRowCarriesTheKeyOnADialectThatCannotTargetTheKey(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 0}).
		Push(crudtest.Rows(), crudtest.Rows([]any{"abc", "mine"}))

	d := Doc{ID: "abc", Title: "mine"}
	saved, err := Docs.Bind(rec).Save(context.Background(), &d)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL, "UPDATE `docs` SET `title` = ? WHERE `id` = ?")
	wantSQL(t, mustSQL(t, rec, 1).SQL, "SELECT 1 FROM `docs` WHERE `id` = ? LIMIT 1")
	wantSQL(t, mustSQL(t, rec, 2).SQL, "INSERT INTO `docs` (`id`, `title`) VALUES (?, ?)")
	if saved.Title != "mine" {
		t.Fatalf("the caller was handed %q instead of the row that was just written", saved.Title)
	}
}

func TestSaveLeavesAChangelessRowAloneRatherThanInsertingASecondOne(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 0}).
		Push(crudtest.Rows([]any{int64(1)}))

	d := Doc{ID: "abc", Title: "same"}
	if err := Docs.Bind(rec).SaveOnly(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	noStatementMentions(t, rec, "INSERT INTO",
		"an engine that counts changed rows reports zero for an update that rewrote a row with the same values; taking that for `no such row` inserts a duplicate key")
}

func TestSaveOnlyRunsItsWholeSequenceUnderOneTransaction(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 0}).
		Push(crudtest.Rows())

	d := Doc{ID: "abc", Title: "mine"}
	if err := Docs.Bind(rec).SaveOnly(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	if rec.TxDepth() != 1 {
		t.Fatalf("SaveOnly opened %d transactions; a probe and an insert that are not one unit let a concurrent writer land between them unnoticed", rec.TxDepth())
	}
	noStatementMentions(t, rec, "ON DUPLICATE KEY UPDATE", "SaveOnly kept the targetless upsert")
}

func TestSaveCannotOverwriteARowOutsideTheDeclaredScope(t *testing.T) {
	rec := crudtest.Postgres().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 1)))

	u := User{ID: 9, Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 1}
	if _, err := scopedUsers.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	noStatementMentions(t, rec, "ON CONFLICT",
		"an upsert has no WHERE clause, so a scoped repository handed a key it does not own rewrites another tenant's row")
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`UPDATE "users" SET "email" = $1, "name" = $2, "age" = $3 WHERE ("id" = $4 AND "tenant_id" = $5)`)
	if got := mustSQL(t, rec, 0).Args[4]; got != int64(1) {
		t.Fatalf("the write was narrowed to tenant %#v, want the declared 1", got)
	}
}

func TestSaveAllCannotOverwriteARowOutsideTheDeclaredScope(t *testing.T) {
	rec := crudtest.Postgres().ExecResult(crud.Result{RowsAffected: 1})

	first := User{ID: 9, Email: "a@x", Name: "A", TenantID: 1}
	second := User{ID: 10, Email: "b@x", Name: "B", TenantID: 1}
	if err := scopedUsers.Bind(rec).SaveAll(context.Background(), []*User{&first, &second}); err != nil {
		t.Fatal(err)
	}
	noStatementMentions(t, rec, "ON CONFLICT", "a batch upsert is still an upsert and still ignores the scope")
	for i, statement := range rec.SQL() {
		if !strings.Contains(statement, `"tenant_id" = `) {
			t.Fatalf("statement %d wrote outside the declared scope: %s", i, statement)
		}
	}
}

func TestSaveReadsTheRowBackThroughTheDeclaredScope(t *testing.T) {
	rec := crudtest.MySQL().
		ExecResult(crud.Result{RowsAffected: 1, LastInsertID: 77, HasLastInsertID: true}).
		Push(crudtest.Rows(userRow(77, "n@x", "New", 18, 1)))

	u := User{Email: "n@x", Name: "New", Age: crud.Set(18), TenantID: 1}
	if _, err := scopedUsers.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	read := mustSQL(t, rec, 1)
	if !strings.Contains(read.SQL, "`tenant_id` = ?") {
		t.Fatalf("the read-back ignored the scope and can hand back a row this repository may not see: %s", read.SQL)
	}
}

func TestCreateInsertsAndLetsAnExistingKeyCollide(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(noteRow(7, "x", 1)))

	n := Note{ID: 7, Title: "x", Version: 1}
	if _, err := Notes.Bind(rec).Create(context.Background(), &n); err != nil {
		t.Fatal(err)
	}
	noStatementMentions(t, rec, "ON CONFLICT",
		"Create that quietly upserts is Save under a name that promises the opposite")
	wantSQL(t, rec.Last().SQL,
		`INSERT INTO "notes" ("id", "title", "version") VALUES ($1, $2, $3) RETURNING "id", "title", "version"`)
}

func TestReplaceRefusesAWriteAgainstARowSomebodyElseAdvanced(t *testing.T) {
	rec := crudtest.Postgres().
		ExecResult(crud.Result{RowsAffected: 0}).
		Push(crudtest.Rows([]any{int64(1)}))

	n := Note{ID: 7, Title: "mine", Version: 3}
	_, err := Notes.Bind(rec).Replace(context.Background(), &n)
	if !errors.Is(err, crud.ErrStaleVersion) {
		t.Fatalf("err = %v, want ErrStaleVersion", err)
	}
	if !errors.Is(err, crud.ErrConflict) {
		t.Fatal("a stale replace must read as a conflict, or a transport cannot answer 409")
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`UPDATE "notes" SET "title" = $1, "version" = "version" + 1 WHERE ("id" = $2 AND "version" = $3)`)
}

func TestReplaceCreatesTheRowWhenNobodyHoldsTheKey(t *testing.T) {
	rec := crudtest.Postgres().
		ExecResult(crud.Result{RowsAffected: 0}).
		Push(crudtest.Rows(), crudtest.Rows(noteRow(7, "mine", 3)))

	n := Note{ID: 7, Title: "mine", Version: 3}
	saved, err := Notes.Bind(rec).Replace(context.Background(), &n)
	if err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 2).SQL,
		`INSERT INTO "notes" ("id", "title", "version") VALUES ($1, $2, $3)`)
	if saved.Title != "mine" {
		t.Fatalf("Replace handed back %q", saved.Title)
	}
}

func TestReplaceRequiresTheKeyItIsReplacing(t *testing.T) {
	rec := crudtest.Postgres()
	n := Note{Title: "mine"}
	if _, err := Notes.Bind(rec).Replace(context.Background(), &n); !errors.Is(err, crud.ErrMissingID) {
		t.Fatalf("err = %v, want ErrMissingID", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a keyless Replace still issued %v", rec.SQL())
	}
}

func TestADecoratorThatDoesNotForwardTheExplicitVerbsRefusesThemOutLoud(t *testing.T) {
	rec := crudtest.Postgres()
	opaque := crud.Wrap[Note, int64, NoteUpdate](crud.Base[Note, int64]{Core: Notes.Bind(rec).Unwrap()})

	n := Note{ID: 7, Title: "x"}
	if _, err := opaque.Create(context.Background(), &n); !errors.Is(err, crud.ErrNoCreateSupport) {
		t.Fatalf("err = %v, want ErrNoCreateSupport", err)
	}
	if _, err := opaque.Replace(context.Background(), &n); !errors.Is(err, crud.ErrNoReplaceSupport) {
		t.Fatalf("err = %v, want ErrNoReplaceSupport", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a decorator that swallowed the capability still let a statement through: %v", rec.SQL())
	}
}

func TestADialectThatTargetsThePrimaryKeyKeepsTheSingleStatementUpsert(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{"abc", "stored"}))

	d := Doc{ID: "abc", Title: "mine"}
	if _, err := Docs.Bind(rec).Save(context.Background(), &d); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("an unscoped Save on PostgreSQL cost %d statements: %v", n, rec.SQL())
	}
	if !strings.Contains(rec.Last().SQL, `ON CONFLICT ("id") DO UPDATE`) {
		t.Fatalf("the key-targeted upsert was given up for nothing: %s", rec.Last().SQL)
	}
}
