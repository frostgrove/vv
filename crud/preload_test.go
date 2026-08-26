package crud_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

func runPreloads(t *testing.T, rec *crudtest.Recorder, m *crud.Meta, items any, specs ...crud.PreloadSpec) {
	t.Helper()
	if err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), m, items, specs, 0, nil); err != nil {
		t.Fatalf("preload: %v", err)
	}
}

func specs(paths ...string) []crud.PreloadSpec {
	o := crud.Build(crud.Preload(paths...))
	return o.Preloads
}

func TestPreloadOptionCollectsPaths(t *testing.T) {
	o := crud.Build(crud.Preload("Author", "  ", "", " Comments.Author "))
	if len(o.Preloads) != 2 {
		t.Fatalf("preloads = %+v, want the blanks dropped", o.Preloads)
	}
	if o.Preloads[0].Path != "Author" || o.Preloads[1].Path != "Comments.Author" {
		t.Fatalf("preloads = %+v, want the paths trimmed", o.Preloads)
	}

	o = crud.Build(crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))))
	if len(o.Preloads) != 1 || o.Preloads[0].Path != "Comments" || len(o.Preloads[0].Opts) != 1 {
		t.Fatalf("preloads = %+v, want one narrowed spec", o.Preloads)
	}
}

// One statement for the whole page, with the parent keys deduplicated — the
// entire reason preloading exists instead of a lazy getter.
func TestPreloadBelongsToIsOneBatchedQuery(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(7), "ann", "berlin"},
		[]any{int64(8), "bo", nil},
	))
	articles := []Article{{ID: 1, AuthorID: 7}, {ID: 2, AuthorID: 7}, {ID: 3, AuthorID: 8}}

	runPreloads(t, rec, articleMeta(t), articles, specs("Author")...)

	if len(rec.Statements()) != 1 {
		t.Fatalf("three articles produced %d statements, want exactly one:\n%v", len(rec.Statements()), rec.SQL())
	}
	st := rec.Last()
	if got := crudtest.Normalize(st.SQL); got != `SELECT "id", "name", "city" FROM "authors" WHERE "id" IN ($1, $2)` {
		t.Fatalf("sql = %s", got)
	}
	if len(st.Args) != 2 || st.Args[0] != int64(7) || st.Args[1] != int64(8) {
		t.Fatalf("keys = %#v, want the two distinct author ids", st.Args)
	}

	for i, want := range []string{"ann", "ann", "bo"} {
		if articles[i].Author == nil {
			t.Fatalf("article %d was left without an author", articles[i].ID)
		}
		if got := articles[i].Author.Name; got != want {
			t.Errorf("article %d got author %q, want %q", articles[i].ID, got, want)
		}
	}
	if city, ok := articles[2].Author.City.Get(); ok {
		t.Errorf("city = %v, want the NULL column to arrive as a null Opt", city)
	}
}

// A has_many hands each parent its own children, and a parent with none gets an
// empty slice rather than nil — which is what makes the JSON honest.
func TestPreloadHasManyDistributesChildren(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), int64(1), int64(7), "first", true},
		[]any{int64(11), int64(1), int64(7), "second", false},
		[]any{int64(12), int64(2), int64(8), "third", true},
	))
	articles := []Article{{ID: 1}, {ID: 2}, {ID: 3}}

	runPreloads(t, rec, articleMeta(t), articles, specs("Comments")...)

	want := `SELECT "id", "article_id", "author_id", "body", "approved" FROM "comments" WHERE "article_id" IN ($1, $2, $3)`
	if got := crudtest.Normalize(rec.Last().SQL); got != want {
		t.Fatalf("sql  = %s\nwant = %s", got, want)
	}
	if len(articles[0].Comments) != 2 || articles[0].Comments[0].Body != "first" || articles[0].Comments[1].Body != "second" {
		t.Fatalf("article 1 got %+v, want its two comments in row order", articles[0].Comments)
	}
	if len(articles[1].Comments) != 1 || articles[1].Comments[0].Body != "third" {
		t.Fatalf("article 2 got %+v, want just the third comment", articles[1].Comments)
	}
	if articles[2].Comments == nil || len(articles[2].Comments) != 0 {
		t.Fatalf("article 3 got %#v, want an empty slice — it has no comments", articles[2].Comments)
	}
}

// A many_to_many reads the owner key out of the join table, so one statement
// still serves every parent.
func TestPreloadManyToManyCarriesTheOwnerKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(1), int64(100), "go"},
		[]any{int64(1), int64(101), "sql"},
		[]any{int64(2), int64(100), "go"},
	))
	articles := []Article{{ID: 1}, {ID: 2}}

	runPreloads(t, rec, articleMeta(t), articles, specs("Tags")...)

	want := `SELECT rxj."article_id", rxt."id", rxt."slug" FROM "tags" AS rxt ` +
		`JOIN "article_tags" AS rxj ON rxj."tag_id" = rxt."id" WHERE rxj."article_id" IN ($1, $2)`
	if got := crudtest.Normalize(rec.Last().SQL); got != want {
		t.Fatalf("sql  = %s\nwant = %s", got, want)
	}
	if len(articles[0].Tags) != 2 || articles[0].Tags[0].Slug != "go" || articles[0].Tags[1].Slug != "sql" {
		t.Fatalf("article 1 tags = %+v, want go and sql", articles[0].Tags)
	}
	if len(articles[1].Tags) != 1 || articles[1].Tags[0].Slug != "go" {
		t.Fatalf("article 2 tags = %+v, want just go", articles[1].Tags)
	}
}

// Shop and Ware are both perfectly ordinary models whose keys happen to be of
// different widths — the join table's owner column is a shop id, not a ware id.
type Shop struct {
	ID    int32  `db:"id,pk,auto"`
	Name  string `db:"name"`
	Wares []Ware `rel:"many_to_many,join=shop_wares"`
}

type Ware struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

// Store keeps the same wares under a text key, which is what a uuid or a slug
// primary key looks like.
type Store struct {
	ID    string `db:"id,pk"`
	Wares []Ware `rel:"many_to_many,join=store_wares"`
}

// The owner column of a join table holds the parent's key, so it has to be read
// as the parent's type. Guessing the target's type instead fails in two
// different ways, and neither of them says so out loud.
func TestPreloadManyToManyReadsTheOwnerKeyAsTheOwnersType(t *testing.T) {
	// Silently, when the two types are convertible: the children are indexed
	// under a key no parent can match and the relation comes back empty.
	t.Run("an owner key of another width", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{int32(1), int64(100), "hammer"}))
		shops := []Shop{{ID: 1}}

		runPreloads(t, rec, metaOf[Shop](t, "shops"), shops, specs("Wares")...)

		if len(shops[0].Wares) != 1 || shops[0].Wares[0].Name != "hammer" {
			t.Fatalf("wares = %+v, want the hammer attached to shop 1", shops[0].Wares)
		}
	})

	// Loudly but far from the cause, when they are not: a text owner key does
	// not scan into the integer destination the target's key would have asked
	// for, and the caller is handed a scan error from a column it never named.
	t.Run("an owner key that is not a number at all", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{"acme", int64(100), "hammer"}))
		stores := []Store{{ID: "acme"}}

		runPreloads(t, rec, metaOf[Store](t, "stores"), stores, specs("Wares")...)

		if len(stores[0].Wares) != 1 || stores[0].Wares[0].Name != "hammer" {
			t.Fatalf("wares = %+v, want the hammer attached to store acme", stores[0].Wares)
		}
	})
}

// Yard and Pallet are the same mismatch one hop over, without a join table in
// sight: an INT foreign key pointing at a BIGINT primary key is an ordinary
// schema, and it is what an ORM generates the moment two models were written by
// two people.
type Yard struct {
	ID      int64    `db:"id,pk,auto"`
	Name    string   `db:"name"`
	Pallets []Pallet `rel:"has_many,fk=YardID"`
}

type Pallet struct {
	ID     int64  `db:"id,pk,auto"`
	YardID int32  `db:"yard_id"`
	Label  string `db:"label"`
	Yard   *Yard  `rel:"belongs_to,fk=YardID"`
}

// The keys either side of a relation are two different struct fields, so they
// may be two different integer widths. Both directions have to match on the
// value, because the failure is silent: a key that misses files the children
// under something no parent ever looks up, and the query itself succeeded.
func TestPreloadMatchesKeysOfDifferentWidths(t *testing.T) {
	t.Run("has_many, narrow fk to a wide key", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows(
			[]any{int64(50), int32(1), "hammers"},
			[]any{int64(51), int32(1), "nails"},
		))
		yards := []Yard{{ID: 1}}

		runPreloads(t, rec, metaOf[Yard](t, "yards"), yards, specs("Pallets")...)

		if len(yards[0].Pallets) != 2 {
			t.Fatalf("yard 1 got %d pallets, want the 2 the query returned: an int32 fk and an int64 key are the same row",
				len(yards[0].Pallets))
		}
		if yards[0].Pallets[0].Label != "hammers" || yards[0].Pallets[1].Label != "nails" {
			t.Fatalf("pallets = %+v, want them in row order", yards[0].Pallets)
		}
	})

	t.Run("belongs_to, narrow fk to a wide key", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(1), "north"}))
		pallets := []Pallet{{ID: 50, YardID: 1}}

		runPreloads(t, rec, metaOf[Pallet](t, "pallets"), pallets, specs("Yard")...)

		if pallets[0].Yard == nil {
			t.Fatal("pallet 50 came back without its yard, though the query returned it")
		}
		if pallets[0].Yard.Name != "north" {
			t.Fatalf("yard = %+v, want north", pallets[0].Yard)
		}
	})
}

// A nested preload has to fill the children where they now live — inside the
// parent's slice — not the temporaries they were scanned into.
func TestNestedPreloadReachesIntoTheStoredChildren(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(
			[]any{int64(10), int64(1), int64(7), "first", true},
			[]any{int64(11), int64(2), int64(8), "second", true},
		),
		crudtest.Rows(
			[]any{int64(7), "ann", "berlin"},
			[]any{int64(8), "bo", nil},
		),
	)
	articles := []Article{{ID: 1}, {ID: 2}}

	runPreloads(t, rec, articleMeta(t), articles, specs("Comments.Author")...)

	if got := len(rec.Statements()); got != 2 {
		t.Fatalf("%d statements, want one per relation per level:\n%v", got, rec.SQL())
	}
	if !strings.Contains(rec.SQL()[0], `"comments"`) || !strings.Contains(rec.SQL()[1], `"authors"`) {
		t.Fatalf("statements ran out of order:\n%v", rec.SQL())
	}
	for i, want := range []string{"ann", "bo"} {
		if len(articles[i].Comments) != 1 {
			t.Fatalf("article %d got %d comments, want 1", articles[i].ID, len(articles[i].Comments))
		}
		author := articles[i].Comments[0].Author
		if author == nil {
			t.Fatalf("article %d's comment kept no author: the nested preload filled a temporary copy", articles[i].ID)
		}
		if author.Name != want {
			t.Errorf("article %d's comment has author %q, want %q", articles[i].ID, author.Name, want)
		}
	}
}

// Preloading a shared parent set of pointers works the same way as a value
// slice; both are what a repository hands over.
func TestPreloadAcceptsAPointerSlice(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(7), "ann", "berlin"}))
	articles := []*Article{{ID: 1, AuthorID: 7}, nil}

	runPreloads(t, rec, articleMeta(t), articles, specs("Author")...)

	if articles[0].Author == nil || articles[0].Author.Name != "ann" {
		t.Fatalf("author = %+v, want it filled in", articles[0].Author)
	}
}

// Keys are chunked so a large page cannot build an IN list the database will
// refuse — and the chunking is the only reason there is more than one
// statement.
func TestPreloadChunksLargeKeySets(t *testing.T) {
	const n = 901
	rec := crudtest.Postgres().Push(crudtest.Rows(), crudtest.Rows())
	articles := make([]Article, n)
	for i := range articles {
		articles[i] = Article{ID: int64(i + 1), AuthorID: int64(i + 1)}
	}

	runPreloads(t, rec, articleMeta(t), articles, specs("Author")...)

	st := rec.Statements()
	if len(st) != 2 {
		t.Fatalf("%d statements for %d keys, want two chunks", len(st), n)
	}
	if len(st[0].Args) != 900 || len(st[1].Args) != 1 {
		t.Fatalf("chunk sizes = %d and %d, want 900 and 1", len(st[0].Args), len(st[1].Args))
	}
}

// A preload may be narrowed and sorted; the extra clauses land inside the one
// batched statement.
func TestPreloadWhereNarrowsTheChildQuery(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(10), int64(1), int64(7), "first", true}))
	articles := []Article{{ID: 1}}

	o := crud.Build(crud.PreloadWhere("Comments",
		crud.Where(crud.Eq("Approved", true)),
		crud.OrderBy(crud.Asc("ID"))))
	runPreloads(t, rec, articleMeta(t), articles, o.Preloads...)

	want := `SELECT "id", "article_id", "author_id", "body", "approved" FROM "comments" ` +
		`WHERE "article_id" IN ($1) AND "approved" = $2 ORDER BY "id" ASC`
	if got := crudtest.Normalize(rec.Last().SQL); got != want {
		t.Fatalf("sql  = %s\nwant = %s", got, want)
	}
	if args := rec.Last().Args; len(args) != 2 || args[1] != true {
		t.Fatalf("args = %#v, want the parent key then the narrowing value", args)
	}
}

// A LIMIT on a batched preload would truncate some parents' children and not
// others, so it is refused instead of quietly lying.
func TestPreloadCannotBePaginated(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  crud.Option
	}{
		{"limit", crud.Limit(1)},
		{"page", crud.Page(2)},
		{"offset", crud.Offset(5)},
		{"unpaged", crud.Unpaged()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			o := crud.Build(crud.PreloadWhere("Comments", tc.opt))
			err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), articleMeta(t),
				[]Article{{ID: 1}}, o.Preloads, 0, nil)
			if err == nil {
				t.Fatal("pagination on a preload was accepted")
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("the query ran anyway: %v", rec.SQL())
			}
		})
	}
}

// Preload paths usually arrive from an HTTP client, so their depth is capped.
func TestPreloadDepthIsCapped(t *testing.T) {
	rec := crudtest.Postgres()
	deep := strings.Repeat("Manager.", 5) + "Manager" // six hops
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), metaOf[Person](t, "persons"),
		[]Person{{ID: 1}}, specs(deep), 0, nil)
	if err == nil {
		t.Fatal("a six-level preload path was accepted under the default cap of five")
	}
	if !strings.Contains(err.Error(), "deeper than") {
		t.Fatalf("err = %v, want it to name the depth limit", err)
	}

	// The cap is per call, so a repository may tighten it.
	err = crud.RunPreloads(context.Background(), rec, rec.Dialect(), articleMeta(t),
		[]Article{{ID: 1}}, specs("Comments.Author"), 1, nil)
	if err == nil {
		t.Fatal("a two-level path was accepted with the cap set to one")
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a rejected preload still queried: %v", rec.SQL())
	}
}

func TestPreloadOfNothingQueriesNothing(t *testing.T) {
	rec := crudtest.Postgres()
	articles := []Article{{ID: 1}}

	runPreloads(t, rec, articleMeta(t), articles)
	runPreloads(t, rec, articleMeta(t), []Article{}, specs("Author")...)

	if len(rec.Statements()) != 0 {
		t.Fatalf("statements = %v, want none", rec.SQL())
	}
}

func TestPreloadOfAnUnknownRelationIsReported(t *testing.T) {
	rec := crudtest.Postgres()
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), articleMeta(t),
		[]Article{{ID: 1}}, specs("Nope"), 0, nil)
	if err == nil {
		t.Fatal("preloading a relation that does not exist was accepted")
	}
}
