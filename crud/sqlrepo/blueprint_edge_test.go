package sqlrepo_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
)

type core022CycleA struct {
	ID  int64          `db:"id,pk,auto"`
	BID int64          `db:"b_id"`
	B   *core022CycleB `rel:"belongs_to,fk=BID"`
}

type core022CycleB struct {
	ID  int64          `db:"id,pk,auto"`
	AID int64          `db:"a_id"`
	A   *core022CycleA `rel:"belongs_to,fk=AID"`
}

// ---------------------------------------------------------------------------
// declarations

// definePanic runs a declaration that cannot work and returns the error Define
// panicked with. A declaration that is quietly accepted is the failure: Define
// exists so a broken mapping dies at package initialisation instead of on the
// first request.
func definePanic(t *testing.T, define func()) error {
	t.Helper()
	var got error
	func() {
		defer func() {
			switch v := recover().(type) {
			case nil:
			case error:
				got = v
			default:
				t.Errorf("Define panicked with %T (%v); it must panic with an error a caller can inspect", v, v)
			}
		}()
		define()
	}()
	if got == nil {
		t.Fatal("Define accepted a declaration that cannot work")
	}
	return got
}

// Every way a declaration can be wrong, refused at declaration time and named
// in the message — a start-up panic is only useful if it says which part of the
// mapping is broken. TryDefine is the same check without the panic, so the two
// have to agree word for word.
func TestBadDeclarationsAreRefusedAndSayWhy(t *testing.T) {
	type NoKey struct {
		Name string `db:"name"`
	}
	type WrongType struct {
		Name *int
	}
	type Unknown struct {
		Nope *string
	}
	type Frozen struct {
		TenantID *int64
	}
	type Key struct {
		ID *int64
	}
	type Computed struct {
		CreatedAt *string
	}

	for _, tc := range []struct {
		name   string
		try    func() error
		define func()
		says   string
	}{
		{
			"a model that is not a struct",
			func() error { _, err := sqlrepo.TryDefine[int, int, struct{}]("counters"); return err },
			func() { sqlrepo.Define[int, int, struct{}]("counters") },
			"model must be a struct",
		},
		{
			"a model with no primary key",
			func() error { _, err := sqlrepo.TryDefine[NoKey, int64, struct{}]("nokeys"); return err },
			func() { sqlrepo.Define[NoKey, int64, struct{}]("nokeys") },
			"no primary key",
		},
		{
			"an ID type that is not the primary key's",
			func() error { _, err := sqlrepo.TryDefine[User, string, UserUpdate]("users"); return err },
			func() { sqlrepo.Define[User, string, UserUpdate]("users") },
			"repository ID type is string but the primary key is int64",
		},
		{
			"an update DTO that is not a struct",
			func() error { _, err := sqlrepo.TryDefine[User, int64, string]("users"); return err },
			func() { sqlrepo.Define[User, int64, string]("users") },
			"update DTO must be a struct",
		},
		{
			"a DTO field the model does not have",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Unknown]("users"); return err },
			func() { sqlrepo.Define[User, int64, Unknown]("users") },
			"no field Nope on model User",
		},
		{
			"a DTO field of the wrong type",
			func() error { _, err := sqlrepo.TryDefine[User, int64, WrongType]("users"); return err },
			func() { sqlrepo.Define[User, int64, WrongType]("users") },
			"type mismatch: DTO carries int, model field Name is string",
		},
		{
			"a DTO field the model froze",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Frozen]("users"); return err },
			func() { sqlrepo.Define[User, int64, Frozen]("users") },
			"model field TenantID is `immutable`",
		},
		{
			"a DTO that would rewrite the primary key",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Key]("users"); return err },
			func() { sqlrepo.Define[User, int64, Key]("users") },
			"the primary key cannot be updated",
		},
		{
			"a DTO field the database owns",
			func() error { _, err := sqlrepo.TryDefine[User, int64, Computed]("users"); return err },
			func() { sqlrepo.Define[User, int64, Computed]("users") },
			"model field CreatedAt is `generated` and never written",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.try()
			if err == nil {
				t.Fatal("TryDefine accepted a declaration that cannot work")
			}
			var se *crud.SchemaError
			if !errors.As(err, &se) {
				t.Fatalf("TryDefine reported %T (%v), want a *crud.SchemaError a caller can read", err, err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("TryDefine said %q, which never mentions %q", err, tc.says)
			}
			if got := definePanic(t, tc.define); got.Error() != err.Error() {
				t.Fatalf("Define panicked with %q but TryDefine reported %q; the two must be the same check", got, err)
			}
		})
	}
}

// An omitted table name is not a broken declaration: it means "the plural of the
// model", which is what lets the one-line Define in the package documentation be
// the common case.
func TestAnEmptyTableNameBecomesThePluralOfTheModel(t *testing.T) {
	type Story struct {
		ID    int64  `db:"id,pk,auto"`
		Title string `db:"title"`
	}
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := sqlrepo.New[Story, int64, struct{}](rec, "").GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `SELECT "id", "title" FROM "stories"`)
}

func TestTryDefineReturnsALateTableConflictInsteadOfPanicking(t *testing.T) {
	type Ledger struct {
		ID int64 `db:"id,pk,auto"`
	}
	type Entry struct {
		ID       int64   `db:"id,pk,auto"`
		LedgerID int64   `db:"ledger_id"`
		Ledger   *Ledger `rel:"belongs_to"`
	}
	entry, err := crud.NewMeta[Entry]("entries")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := entry.Relation("Ledger").Resolve(); err != nil {
		t.Fatal(err)
	}

	_, err = sqlrepo.TryDefine[Ledger, int64, struct{}]("accounting_ledgers")
	if err == nil || !strings.Contains(err.Error(), "already resolved") {
		t.Fatalf("TryDefine error = %v, want late table declaration refusal", err)
	}
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("TryDefine error = %T, want *crud.SchemaError", err)
	}
}

func TestAnIndependentBlueprintDoesNotCompeteForTheRelationTable(t *testing.T) {
	type Ledger struct {
		ID int64 `db:"id,pk,auto"`
	}
	if _, err := sqlrepo.TryDefine[Ledger, int64, struct{}]("accounting_ledgers"); err != nil {
		t.Fatal(err)
	}
	alternate, err := sqlrepo.TryDefine[Ledger, int64, struct{}]("archived_ledgers",
		sqlrepo.IndependentTable())
	if err != nil {
		t.Fatalf("independent blueprint: %v", err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := alternate.Bind(rec).GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL, `SELECT "id" FROM "archived_ledgers"`)
	if got := crud.TableNameOf(reflect.TypeFor[Ledger]()); got != "accounting_ledgers" {
		t.Fatalf("independent blueprint changed relation target to %q", got)
	}
	if _, err := sqlrepo.TryDefine[Ledger, int64, struct{}]("another_default"); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("ordinary conflicting blueprint = %v", err)
	}
}

func TestIndependentTableKeepsSelfRelationsInItsOwnPhysicalView(t *testing.T) {
	type Node struct {
		ID       int64 `db:"id,pk,auto"`
		ParentID int64 `db:"parent_id"`

		Parent          *Node `rel:"belongs_to,fk=ParentID"`
		CanonicalParent *Node `rel:"belongs_to,fk=ParentID,table=core022_nodes"`
	}

	if _, err := sqlrepo.TryDefine[Node, int64, struct{}]("core022_nodes"); err != nil {
		t.Fatal(err)
	}
	archive, err := sqlrepo.TryDefine[Node, int64, struct{}]("core022_archived_nodes", sqlrepo.IndependentTable())
	if err != nil {
		t.Fatal(err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows())
	repo := archive.Bind(rec)
	if _, err := repo.GetAll(context.Background(), crud.Where(crud.Eq("Parent.ID", int64(7)))); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`SELECT "id", "parent_id" FROM "core022_archived_nodes" WHERE `+
			`EXISTS (SELECT 1 FROM "core022_archived_nodes" AS rx1 `+
			`WHERE rx1."id" = "core022_archived_nodes"."parent_id" AND rx1."id" = $1)`)

	// `table=` remains the explicit low-level escape hatch for an edge that is
	// intentionally supposed to leave the archive and reach the live table.
	if _, err := repo.GetAll(context.Background(), crud.Where(crud.Eq("CanonicalParent.Parent.ID", int64(7)))); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, mustSQL(t, rec, 1).SQL,
		`SELECT "id", "parent_id" FROM "core022_archived_nodes" WHERE `+
			`EXISTS (SELECT 1 FROM "core022_nodes" AS rx1 `+
			`WHERE rx1."id" = "core022_archived_nodes"."parent_id" AND `+
			`EXISTS (SELECT 1 FROM "core022_nodes" AS rx2 `+
			`WHERE rx2."id" = rx1."parent_id" AND rx2."id" = $1))`)
}

func TestIndependentTableKeepsACycleOnItsStartingPhysicalView(t *testing.T) {
	if _, err := sqlrepo.TryDefine[core022CycleA, int64, struct{}]("core022_cycle_as"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlrepo.TryDefine[core022CycleB, int64, struct{}]("core022_cycle_bs"); err != nil {
		t.Fatal(err)
	}
	archive, err := sqlrepo.TryDefine[core022CycleA, int64, struct{}]("core022_archived_cycle_as", sqlrepo.IndependentTable())
	if err != nil {
		t.Fatal(err)
	}
	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := archive.Bind(rec).GetAll(context.Background(), crud.Where(crud.Eq("B.A.ID", int64(9)))); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "b_id" FROM "core022_archived_cycle_as" WHERE `+
			`EXISTS (SELECT 1 FROM "core022_cycle_bs" AS rx1 `+
			`WHERE rx1."id" = "core022_archived_cycle_as"."b_id" AND `+
			`EXISTS (SELECT 1 FROM "core022_archived_cycle_as" AS rx2 `+
			`WHERE rx2."id" = rx1."a_id" AND rx2."id" = $1))`)
}

func TestConcurrentIndependentRelationViewsAreStableAndBranchLocal(t *testing.T) {
	type Node struct {
		ID       int64 `db:"id,pk,auto"`
		ParentID int64 `db:"parent_id"`
		Parent   *Node `rel:"belongs_to,fk=ParentID"`
	}

	canonical, err := sqlrepo.TryDefine[Node, int64, struct{}]("core022_pointer_nodes")
	if err != nil {
		t.Fatal(err)
	}
	archiveA, err := sqlrepo.TryDefine[Node, int64, struct{}]("core022_pointer_nodes_a", sqlrepo.IndependentTable())
	if err != nil {
		t.Fatal(err)
	}
	archiveB, err := sqlrepo.TryDefine[Node, int64, struct{}]("core022_pointer_nodes_b", sqlrepo.IndependentTable())
	if err != nil {
		t.Fatal(err)
	}

	branches := []struct {
		name, table string
		meta        *crud.Meta
	}{
		{name: "canonical", table: "core022_pointer_nodes", meta: canonical.Meta()},
		{name: "archive a", table: "core022_pointer_nodes_a", meta: archiveA.Meta()},
		{name: "archive b", table: "core022_pointer_nodes_b", meta: archiveB.Meta()},
	}
	type observation struct {
		branch int
		rel    *crud.Relation
		target *crud.Meta
		local  *crud.Field
		remote *crud.Field
		err    error
	}
	const readers = 32
	start := make(chan struct{})
	results := make(chan observation, len(branches)*readers)
	for branchIndex, branch := range branches {
		branchIndex, meta := branchIndex, branch.meta
		for range readers {
			go func() {
				<-start
				relation := meta.Relation("Parent")
				target, local, remote, err := relation.Resolve()
				results <- observation{branch: branchIndex, rel: relation, target: target, local: local, remote: remote, err: err}
			}()
		}
	}
	close(start)

	byBranch := make([][]observation, len(branches))
	for range len(branches) * readers {
		got := <-results
		byBranch[got.branch] = append(byBranch[got.branch], got)
	}
	for branchIndex, branch := range branches {
		first := byBranch[branchIndex][0]
		if first.err != nil {
			t.Fatalf("%s first resolution: %v", branch.name, first.err)
		}
		if first.target.Table != branch.table {
			t.Fatalf("%s target table = %q, want %q", branch.name, first.target.Table, branch.table)
		}
		if first.local.Name != "ParentID" || first.remote.Name != "ID" {
			t.Fatalf("%s joins %s -> %s, want ParentID -> ID", branch.name, first.local.Name, first.remote.Name)
		}
		for reader, got := range byBranch[branchIndex] {
			if got.err != nil {
				t.Fatalf("%s reader %d: %v", branch.name, reader, got.err)
			}
			if got.rel != first.rel || got.target != first.target || got.local != first.local || got.remote != first.remote {
				t.Fatalf("%s reader %d saw unstable pointers: rel %p/%p target %p/%p local %p/%p remote %p/%p",
					branch.name, reader, got.rel, first.rel, got.target, first.target,
					got.local, first.local, got.remote, first.remote)
			}
		}
		if again := branch.meta.Relation("Parent"); again != first.rel {
			t.Fatalf("%s cached relation = %p, want %p", branch.name, again, first.rel)
		}
	}
	for left := range branches {
		for right := left + 1; right < len(branches); right++ {
			if byBranch[left][0].rel == byBranch[right][0].rel || byBranch[left][0].target == byBranch[right][0].target {
				t.Fatalf("%s and %s shared a contextual relation/target pointer", branches[left].name, branches[right].name)
			}
		}
	}
}

func TestFailedTryDefineDoesNotPublishItsTable(t *testing.T) {
	type Candidate struct {
		ID int64 `db:"id,pk,auto"`
	}

	if _, err := sqlrepo.TryDefine[Candidate, int64, struct{}]("core022_invalid_candidates",
		sqlrepo.RelationScope("Missing", crud.Eq("ID", int64(1)))); err == nil {
		t.Fatal("TryDefine accepted an unknown relation scope")
	}
	if _, err := sqlrepo.TryDefine[Candidate, int64, struct{}]("core022_candidates"); err != nil {
		t.Fatalf("a failed declaration leaked its table registration: %v", err)
	}
	if got := crud.TableNameOf(reflect.TypeFor[Candidate]()); got != "core022_candidates" {
		t.Fatalf("published table = %q, want only the successful declaration", got)
	}
}

func TestFailedTryDefineWithUnknownLocalFieldPublishesNeitherModel(t *testing.T) {
	type Target struct {
		ID int64 `db:"id,pk,auto"`
	}
	type Owner struct {
		ID     int64   `db:"id,pk,auto"`
		Target *Target `rel:"belongs_to,fk=Missing"`
	}

	_, err := sqlrepo.TryDefine[Owner, int64, struct{}]("core022_invalid_local_owners",
		sqlrepo.RelationScope("Target", crud.Eq("ID", int64(1))))
	if err == nil || !strings.Contains(err.Error(), "relation references unknown field Missing") {
		t.Fatalf("TryDefine error = %v, want the invalid local join field", err)
	}
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("TryDefine error = %T, want *crud.SchemaError", err)
	}

	if err := crud.TryRegisterTable[Owner]("core022_corrected_local_owners"); err != nil {
		t.Fatalf("failed declaration published its root table: %v", err)
	}
	if err := crud.TryRegisterTable[Target]("core022_corrected_local_targets"); err != nil {
		t.Fatalf("failed relation validation published its target table: %v", err)
	}
}

func TestLateRelationScopeFailurePublishesNeitherEarlierTargetNorRoot(t *testing.T) {
	type Target struct {
		ID int64 `db:"id,pk,auto"`
	}
	type Owner struct {
		ID       int64   `db:"id,pk,auto"`
		TargetID int64   `db:"target_id"`
		Target   *Target `rel:"belongs_to,fk=TargetID"`
	}

	_, err := sqlrepo.TryDefine[Owner, int64, struct{}]("core022_invalid_multiscope_owners",
		sqlrepo.RelationScope("Target", crud.Eq("ID", int64(1))),
		sqlrepo.RelationScope("Missing", crud.Eq("ID", int64(2))))
	if err == nil || !strings.Contains(err.Error(), `unknown field "Missing"`) {
		t.Fatalf("TryDefine error = %v, want the later unknown relation scope", err)
	}

	if err := crud.TryRegisterTable[Target]("core022_multiscope_targets"); err != nil {
		t.Fatalf("earlier valid scope published its target before the later failure: %v", err)
	}
	corrected, err := sqlrepo.TryDefine[Owner, int64, struct{}]("core022_multiscope_owners",
		sqlrepo.RelationScope("Target", crud.Eq("ID", int64(1))))
	if err != nil {
		t.Fatalf("failed declaration published its root or poisoned its target: %v", err)
	}
	if corrected.Meta().Table != "core022_multiscope_owners" {
		t.Fatalf("corrected root table = %q", corrected.Meta().Table)
	}
	if got := crud.TableNameOf(reflect.TypeFor[Target]()); got != "core022_multiscope_targets" {
		t.Fatalf("corrected target table = %q", got)
	}
}

// ---------------------------------------------------------------------------
// settings

// The two page-size settings are a floor and a ceiling, and the ceiling wins
// even when it is below the floor: a repository that says "20 by default, never
// more than 10" hands out 10, not 20.
func TestMaxLimitCapsEvenTheDefaultLimit(t *testing.T) {
	strict := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.DefaultLimit(50), sqlrepo.MaxLimit(10))

	for _, tc := range []struct {
		name    string
		options []crud.Option
		want    string
	}{
		{"a request with no limit gets the default, clamped", nil, "LIMIT 10"},
		{"a request under the cap is left alone", []crud.Option{crud.Limit(4)}, "LIMIT 4"},
		{"a request over the cap is clamped", []crud.Option{crud.Limit(1000)}, "LIMIT 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			if _, err := strict.Bind(rec).Get(context.Background(), tc.options...); err != nil {
				t.Fatal(err)
			}
			if got := mustSQL(t, rec, 0).SQL; !strings.Contains(got, tc.want) {
				t.Fatalf("the page was fetched with %s, want %s", got, tc.want)
			}
		})
	}
}

// A default page size of zero would mean LIMIT 0 — a page with no rows on it —
// so a non-positive setting is read as "not set" and the package default stands.
func TestANonPositiveDefaultLimitFallsBackToThePackageDefault(t *testing.T) {
	for _, n := range []int{0, -5} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			repository := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.DefaultLimit(n)).Bind(rec)
			if _, err := repository.Get(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "LIMIT " + strconv.Itoa(sqlrepo.DefaultPageSize)
			if got := mustSQL(t, rec, 0).SQL; !strings.Contains(got, want) {
				t.Fatalf("DefaultLimit(%d) produced %s, want %s", n, got, want)
			}
		})
	}
}

// Define validates the model, the ID and the update DTO, but not the sort terms
// — so a default sort naming a column that is not there survives declaration.
// It cannot survive a query: the statement is refused before it is sent, rather
// than handed to the database to reject.
func TestAnUnknownDefaultSortIsRefusedBeforeTheQueryIsSent(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repository := sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.DefaultSort(crud.Desc("Nope"))).Bind(rec)

	_, err := repository.Get(context.Background())

	var uf *crud.UnknownFieldError
	if !errors.As(err, &uf) {
		t.Fatalf("err = %v, want an UnknownFieldError naming the sort column", err)
	}
	if uf.Field != "Nope" {
		t.Fatalf("the error blames field %q, want Nope", uf.Field)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a sort that does not resolve still reached the database: %v", rec.SQL())
	}
}

// ---------------------------------------------------------------------------
// preload depth

type Author struct {
	ID    int64  `db:"id,pk,auto"`
	Name  string `db:"name"`
	Books []Book `rel:"has_many"`
}

type Book struct {
	ID       int64  `db:"id,pk,auto"`
	AuthorID int64  `db:"author_id"`
	Title    string `db:"title"`
	Pages    []Page `rel:"has_many"`
}

type Page struct {
	ID     int64 `db:"id,pk,auto"`
	BookID int64 `db:"book_id"`
	Number int   `db:"number"`
}

// PreloadDepth is the guard against a client turning one request into a dozen
// queries by asking for `a.b.a.b`. Zero cannot mean "no hops": Bind has no way
// to tell an explicit zero from an unset setting, so it means "the default".
func TestPreloadDepthCapsAPathAndZeroMeansUnset(t *testing.T) {
	authors := func() crudtest.Result { return crudtest.Rows([]any{int64(1), "Ann"}) }
	books := func() crudtest.Result { return crudtest.Rows([]any{int64(10), int64(1), "Dune"}) }
	pages := func() crudtest.Result { return crudtest.Rows([]any{int64(100), int64(10), 7}) }

	t.Run("one hop is allowed at a depth of one", func(t *testing.T) {
		rec := crudtest.Postgres().Push(authors(), books())
		repository := sqlrepo.Define[Author, int64, struct{}]("authors", sqlrepo.PreloadDepth(1)).Bind(rec)

		got, err := repository.GetAll(context.Background(), crud.Preload("Books"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got[0].Books) != 1 || got[0].Books[0].Title != "Dune" {
			t.Fatalf("the one allowed hop did not load: %+v", got)
		}
	})

	t.Run("the second hop is refused at a depth of one", func(t *testing.T) {
		rec := crudtest.Postgres().Push(authors())
		repository := sqlrepo.Define[Author, int64, struct{}]("authors", sqlrepo.PreloadDepth(1)).Bind(rec)

		_, err := repository.GetAll(context.Background(), crud.Preload("Books.Pages"))
		if err == nil {
			t.Fatal("a two-segment path was accepted by a repository that allows one")
		}
		if !strings.Contains(err.Error(), "deeper than the allowed 1") {
			t.Fatalf("err = %v, want a refusal naming the depth", err)
		}
		if n := len(rec.Statements()); n != 1 {
			t.Fatalf("%d statements ran, want only the root query: %v", n, rec.SQL())
		}
	})

	t.Run("zero leaves the default depth in place", func(t *testing.T) {
		rec := crudtest.Postgres().Push(authors(), books(), pages())
		repository := sqlrepo.Define[Author, int64, struct{}]("authors", sqlrepo.PreloadDepth(0)).Bind(rec)

		got, err := repository.GetAll(context.Background(), crud.Preload("Books.Pages"))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || len(got[0].Books) != 1 || len(got[0].Books[0].Pages) != 1 {
			t.Fatalf("PreloadDepth(0) refused a two-hop path: %+v", got)
		}
		if got[0].Books[0].Pages[0].Number != 7 {
			t.Fatalf("the second hop loaded the wrong row: %+v", got[0].Books[0].Pages)
		}
	})
}

// ---------------------------------------------------------------------------
// scope

var scopedUsers = sqlrepo.Define[User, int64, UserUpdate]("users", sqlrepo.Scope(crud.Eq("TenantID", int64(1))))

// A repository scope is permanent. Every statement that has a WHERE clause
// carries it, and a caller's own filter is ANDed onto it rather than replacing
// it.
func TestScopeIsANDedIntoEveryStatementWithAWhereClause(t *testing.T) {
	ctx := context.Background()
	const cols = `"id", "email", "name", "age", "tenant_id", "created_at"`

	for _, tc := range []struct {
		name string
		push []crudtest.Result
		call func(*crud.Repo[User, int64, UserUpdate]) error
		want string
	}{
		{"GetAll", []crudtest.Result{crudtest.Rows()},
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.GetAll(ctx); return err },
			`SELECT ` + cols + ` FROM "users" WHERE "tenant_id" = $1`},
		{"GetAll with a caller filter", []crudtest.Result{crudtest.Rows()},
			func(r *crud.Repo[User, int64, UserUpdate]) error {
				_, err := r.GetAll(ctx, crud.Where(crud.Eq("Email", "a@b.c")))
				return err
			},
			`SELECT ` + cols + ` FROM "users" WHERE ("tenant_id" = $1 AND "email" = $2)`},
		{"GetByID", []crudtest.Result{crudtest.Rows(userRow(5, "a@b.c", "Ann", 30, 1))},
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.GetByID(ctx, 5); return err },
			`SELECT ` + cols + ` FROM "users" WHERE ("tenant_id" = $1 AND "id" = $2) LIMIT 1`},
		{"Count", []crudtest.Result{crudtest.Rows([]any{int64(0)})},
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.Count(ctx); return err },
			`SELECT count(*) FROM "users" WHERE "tenant_id" = $1`},
		{"Exists", []crudtest.Result{crudtest.Rows()},
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.Exists(ctx); return err },
			`SELECT 1 FROM "users" WHERE "tenant_id" = $1 LIMIT 1`},
		{"DeleteAll", nil,
			func(r *crud.Repo[User, int64, UserUpdate]) error { _, err := r.DeleteAll(ctx); return err },
			`DELETE FROM "users" WHERE "tenant_id" = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(tc.push...)
			if err := tc.call(scopedUsers.Bind(rec)); err != nil {
				t.Fatal(err)
			}
			wantSQL(t, mustSQL(t, rec, 0).SQL, tc.want)
			if got := mustSQL(t, rec, 0).Args[0]; got != int64(1) {
				t.Fatalf("the scope bound %#v, want the tenant it was declared with", got)
			}
		})
	}
}

// The caller cannot argue with the scope. A filter on the very column the scope
// pins is ANDed in beside it, which narrows the query to nothing — the one thing
// it must never do is replace it.
func TestACallerFilterCannotWidenTheScope(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := scopedUsers.Bind(rec).GetAll(context.Background(),
		crud.Where(crud.Eq("TenantID", int64(2)))); err != nil {
		t.Fatal(err)
	}

	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "tenant_id" = $2)`)
	if args := rec.Last().Args; len(args) != 2 || args[0] != int64(1) || args[1] != int64(2) {
		t.Fatalf("bound %#v, want the scope's tenant first and the caller's second", args)
	}
}

// Permanent narrowings are independently safe declarations. Repeating Scope
// must retain both predicates — last-wins would turn adding a visibility guard
// into removing the tenant guard it followed.
func TestRepeatedScopesComposeByAND(t *testing.T) {
	repository := sqlrepo.Define[User, int64, UserUpdate]("users",
		sqlrepo.Scope(crud.Eq("TenantID", int64(1))),
		sqlrepo.Scope(crud.Eq("Age", 30)),
	).Bind(crudtest.Postgres().Push(crudtest.Rows()))

	if _, err := repository.GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Bind a fresh recorder to inspect the exact declaration-derived SQL: the
	// first repository above proves the declaration remains normally callable.
	rec := crudtest.Postgres().Push(crudtest.Rows())
	repository = sqlrepo.Define[User, int64, UserUpdate]("users",
		sqlrepo.Scope(crud.Eq("TenantID", int64(1))),
		sqlrepo.Scope(crud.Eq("Age", 30)),
	).Bind(rec)
	if _, err := repository.GetAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "age" = $2)`)
}

// An UPDATE has a WHERE clause of its own, but it is the primary key alone: the
// scope does its work on the load that precedes it, so a row outside the scope
// is never diffed and never written.
func TestUpdateLoadsThroughTheScopeSoAnOutsideRowIsNotFound(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows()) // the scoped load finds nothing

	_, err := scopedUsers.Bind(rec).Update(context.Background(), 5, UserUpdate{Name: ptr("x")})

	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for a row outside the scope", err)
	}
	wantSQL(t, mustSQL(t, rec, 0).SQL,
		`SELECT "id", "email", "name", "age", "tenant_id", "created_at" FROM "users" `+
			`WHERE ("tenant_id" = $1 AND "id" = $2) LIMIT 1`)
	if n := len(rec.Statements()); n != 1 {
		t.Fatalf("%d statements ran, want only the load: %v", n, rec.SQL())
	}
}

// Save is an upsert: there is no WHERE clause for a scope to narrow, which the
// Scope documentation says out loud. Pinned here so that the day it changes, it
// changes deliberately — a service method or a security policy is what guards
// this hole today.
func TestScopeCannotReachSave(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(userRow(9, "n@x", "New", 18, 3)))
	u := User{Email: "n@x", Name: "New", TenantID: 3} // tenant 3, while the scope pins 1

	if _, err := scopedUsers.Bind(rec).Save(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	st := rec.Last()
	wantSQL(t, st.SQL,
		`INSERT INTO "users" ("email", "name", "age", "tenant_id") VALUES ($1, $2, $3, $4) `+
			`RETURNING "id", "email", "name", "age", "tenant_id", "created_at"`)
	if got := st.Args[3]; got != int64(3) {
		t.Fatalf("the insert wrote tenant %#v; if the scope now reaches Save, say so out loud", got)
	}
}
