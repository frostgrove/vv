package probe

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/crudtest"
)

func TestADeclarationWhoseTableTheCatalogDoesNotKnowRefusesToStart(t *testing.T) {
	// An empty catalog is exactly what a MySQL user with no information_schema
	// grants reads: zero rows rather than a refusal, so Load succeeds. The first
	// declaration that names a table is what tells the two apart.
	_, err := Full(newFakeCatalog("mysql")).(*full).Declare(docMeta(t))
	if !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("a declaration against an empty catalog started anyway: %v", err)
	}
}

// The control: the loaded twin starts.
func TestADeclarationWhoseTableTheCatalogKnowsStarts(t *testing.T) {
	if _, err := Full(fixture()).(*full).Declare(docMeta(t)); err != nil {
		t.Fatalf("a declaration against a catalog holding the table was refused: %v", err)
	}
}

func TestAQualifiedDeclarationRefusesACatalogThatCanOnlyGuessByBareName(t *testing.T) {
	meta, err := crud.NewMetaInSchema[Doc]("analytics", "docs")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Full(fixture()).(*full).Declare(meta)
	if !errors.Is(err, ErrQualifiedCatalog) {
		t.Fatalf("a qualified declaration over a bare-only catalog answered %v, want ErrQualifiedCatalog", err)
	}
}

func TestAQualifiedDeclarationUsesOnlyItsSchemasTableAndForeignKeys(t *testing.T) {
	pk := func(schema string) catalog.Constraint {
		return catalog.Constraint{Name: "docs_pkey", Schema: schema, Table: "docs", Kind: catalog.KindPrimaryKey, Columns: []string{"id"}}
	}
	public := catalog.Table{
		Schema: "public", Name: "docs", PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{
			pk("public"),
			{Name: "public_only", Schema: "public", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"email"}},
		},
	}
	analytics := catalog.Table{
		Schema: "analytics", Name: "docs", PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{
			pk("analytics"),
			{Name: "analytics_email", Schema: "analytics", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"email"}},
			{Name: "analytics_org", Schema: "analytics", Table: "docs", Kind: catalog.KindForeignKey,
				Columns: []string{"org_id"}, RefSchema: "analytics", RefTable: "orgs", RefColumns: []string{"id"}},
		},
	}
	notes := catalog.Table{
		Schema: "analytics", Name: "notes", PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{{
			Name: "analytics_notes_doc", Schema: "analytics", Table: "notes", Kind: catalog.KindForeignKey,
			Columns: []string{"doc_id"}, RefSchema: "analytics", RefTable: "docs", RefColumns: []string{"id"},
			OnUpdate: "NO ACTION",
		}},
	}
	cat := &qualifiedFakeCatalog{newFakeCatalog("postgres", public, analytics, notes)}
	meta, err := crud.NewMetaInSchema[Doc]("analytics", "docs")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := Full(cat).(*full).Declare(meta)
	if err != nil {
		t.Fatal(err)
	}
	got := handler.(*full)
	if got.tbl.Schema != "analytics" {
		t.Fatalf("qualified declaration bound to %s.%s", got.tbl.Schema, got.tbl.Name)
	}
	byName := map[string]candidate{}
	for _, cand := range got.cands {
		byName[cand.name] = cand
	}
	if _, exists := byName["public_only"]; exists {
		t.Fatal("the analytics declaration inherited public.docs constraints")
	}
	if cand, ok := byName["analytics_email"]; !ok || cand.table != (crud.TableRef{Schema: "analytics", Name: "docs"}) {
		t.Fatalf("analytics unique candidate = %+v", cand)
	}
	if cand, ok := byName["analytics_org"]; !ok || cand.table != (crud.TableRef{Schema: "analytics", Name: "orgs"}) {
		t.Fatalf("analytics parent candidate = %+v", cand)
	}
	if cand, ok := byName["analytics_notes_doc"]; !ok || cand.table != (crud.TableRef{Schema: "analytics", Name: "notes"}) {
		t.Fatalf("analytics inbound candidate = %+v", cand)
	}
}

func TestADeclarationOverAKeyThatDoesNotIdentifyARowRefusesToStart(t *testing.T) {
	cat := fixture()
	docs, _ := cat.Table("docs")
	// The table's real key is composite. The model maps one field onto one half
	// of it, which is already wrong for the repository's own WHERE pk = ?.
	docs.PrimaryKey = []string{"id", "tenant_id"}
	docs.Constraints[0].Columns = []string{"id", "tenant_id"}

	_, err := Full(cat).(*full).Declare(docMeta(t))
	if !errors.Is(err, ErrKeyDoesNotIdentify) {
		t.Fatalf("a model over half a composite key started anyway: %v", err)
	}
}

// The control: the same table with the same model, where the key really is the
// one column. Without it a Declare that refused everything would pass.
func TestADeclarationOverASingleColumnKeyStarts(t *testing.T) {
	if _, err := Full(fixture()).(*full).Declare(docMeta(t)); err != nil {
		t.Fatalf("a single-column primary key was refused: %v", err)
	}
}

// A unique key over the column is a row identity too, which is what a model
// bound to a table whose primary key the engine does not report needs.
func TestAUniqueKeyOverTheColumnIdentifiesARow(t *testing.T) {
	cat := fixture()
	docs, _ := cat.Table("docs")
	docs.PrimaryKey = nil
	docs.Constraints[0].Kind = catalog.KindUnique
	if _, err := Full(cat).(*full).Declare(docMeta(t)); err != nil {
		t.Fatalf("a unique key over the model's key column was not accepted as a row identity: %v", err)
	}
}

func TestASkipNamingNoConstraintRefusesToStart(t *testing.T) {
	_, err := Full(fixture(), Skip("docs_email_uk_renamed")).(*full).Declare(docMeta(t))
	if !errors.Is(err, ErrUnknownConstraint) {
		t.Fatalf("an opt-out naming nothing was accepted, so a rename turns the control off silently: %v", err)
	}
	// The control: the name as it really is.
	if _, err := Full(fixture(), Skip("docs_email_uk")).(*full).Declare(docMeta(t)); err != nil {
		t.Fatalf("an opt-out naming a real constraint was refused: %v", err)
	}
}

func TestAnUndeclaredHandlerProbesNothingAndSaysSo(t *testing.T) {
	rec := crudtest.Postgres()
	f := Full(fixture()).(*full)
	got, err := f.Enrich(ctx, request(conflict("docs_email_uk", "email"), rec, docMeta(t), row(insert())))
	if !errors.Is(err, ErrNotDeclared) {
		t.Fatalf("an undeclared handler ran anyway: %v", err)
	}
	if got == nil || len(got.Violations) != 1 {
		t.Fatalf("an undeclared handler lost the driver's violation: %+v", got)
	}
	if n := len(rec.Statements()); n != 0 {
		t.Fatalf("an undeclared handler issued %d statements", n)
	}
}

func TestDeclaringTwoModelsFromOneFullValueGivesTwoHandlers(t *testing.T) {
	cat := fixture()
	base := Full(cat).(*full)
	a, err := base.Declare(docMeta(t))
	if err != nil {
		t.Fatal(err)
	}
	other, err := crud.NewMeta[Doc]("orgs")
	if err != nil {
		t.Fatal(err)
	}
	b, err := base.Declare(other)
	if err != nil {
		t.Fatal(err)
	}
	if a.(*full).tbl.Name != "docs" {
		t.Fatalf("declaring a second model moved the first onto %s", a.(*full).tbl.Name)
	}
	if b.(*full).tbl.Name != "orgs" {
		t.Fatalf("the second declaration bound to %s", b.(*full).tbl.Name)
	}
}

// The transaction matrix, unit half. The live half is in test/integration.
func TestTheTransactionMatrixDecidesWhetherTheProbeRunsAtAll(t *testing.T) {
	type arm struct {
		name      string
		dialect   crud.Dialect
		tx        string // "none", "own", "foreign"
		recovered bool
		want      bool
	}
	arms := []arm{
		{"postgres outside a transaction", crud.Postgres{}, "none", false, true},
		{"mysql outside a transaction", crud.MySQL{}, "none", false, true},

		// The engine that poisons: nothing runs until the transaction is
		// restored, so Simple is the default there.
		{"postgres in our own transaction", crud.Postgres{}, "own", false, false},
		{"postgres in our own transaction, restored", crud.Postgres{}, "own", true, true},
		// A foreign transaction is never given a savepoint, so it is never
		// restored — and even if something claimed it was, it is not ours.
		{"postgres in a foreign transaction", crud.Postgres{}, "foreign", false, false},
		{"postgres in a foreign transaction, claiming restored", crud.Postgres{}, "foreign", true, false},

		// The engines that roll back the statement alone need nothing.
		{"mysql in our own transaction", crud.MySQL{}, "own", false, true},
		{"mysql in a foreign transaction", crud.MySQL{}, "foreign", false, true},
		{"sqlite in our own transaction", crud.SQLite{}, "own", false, true},
	}
	walked := 0
	for _, a := range arms {
		t.Run(a.name, func(t *testing.T) {
			rec := crudtest.New(a.dialect)
			f := declared(t, fixture())
			request := request(conflict("", ""), rec, docMeta(t), row(insert()))
			request.Recovered = a.recovered

			run := func(ctx context.Context) {
				walked++
				if got := f.runs(ctx, request); got != a.want {
					t.Fatalf("the probe would run = %v, want %v", got, a.want)
				}
			}
			switch a.tx {
			case "none":
				run(ctx)
			case "own":
				if err := crud.InTx(ctx, rec, func(inner context.Context) error {
					run(inner)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				run(crud.BindExecutor(ctx, rec, rec))
			}
		})
	}
	if walked != len(arms) {
		t.Fatalf("walked %d arms of %d: a case was skipped rather than asserted", walked, len(arms))
	}
}
