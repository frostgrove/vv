package crud_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/utils"
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

type Shop struct {
	ID    int32  `db:"id,pk,auto"`
	Name  string `db:"name"`
	Wares []Ware `rel:"many_to_many,join=shop_wares"`
}

type Ware struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

type Store struct {
	ID    string `db:"id,pk"`
	Wares []Ware `rel:"many_to_many,join=store_wares"`
}

func TestPreloadManyToManyReadsTheOwnerKeyAsTheOwnersType(t *testing.T) {
	t.Run("an owner key of another width", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{int32(1), int64(100), "hammer"}))
		shops := []Shop{{ID: 1}}

		runPreloads(t, rec, metaOf[Shop](t, "shops"), shops, specs("Wares")...)

		if len(shops[0].Wares) != 1 || shops[0].Wares[0].Name != "hammer" {
			t.Fatalf("wares = %+v, want the hammer attached to shop 1", shops[0].Wares)
		}
	})

	t.Run("an owner key that is not a number at all", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{"acme", int64(100), "hammer"}))
		stores := []Store{{ID: "acme"}}

		runPreloads(t, rec, metaOf[Store](t, "stores"), stores, specs("Wares")...)

		if len(stores[0].Wares) != 1 || stores[0].Wares[0].Name != "hammer" {
			t.Fatalf("wares = %+v, want the hammer attached to store acme", stores[0].Wares)
		}
	})
}

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

func TestPreloadAcceptsAPointerSlice(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(7), "ann", "berlin"}))
	articles := []*Article{{ID: 1, AuthorID: 7}, nil}

	runPreloads(t, rec, articleMeta(t), articles, specs("Author")...)

	if articles[0].Author == nil || articles[0].Author.Name != "ann" {
		t.Fatalf("author = %+v, want it filled in", articles[0].Author)
	}
}

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

func TestPreloadRefusesEveryUnsupportedGenericOptionBeforeRowsOrSQL(t *testing.T) {
	relationScopes := (*crud.RelationScopes)(nil).
		AtPath("Author", crud.Eq("ID", int64(7)))
	tests := []struct {
		name string
		opt  crud.Option
		want string
	}{
		{"projection", crud.Select("Body"), "projection"},
		{"nested preload", crud.Preload("Author"), "dotted top-level"},
		{"relation narrowing", crud.NarrowRelations(relationScopes), "containing query"},
		{"aggregate", crud.Aggregate(crud.CountAll("total")), "aggregate"},
		{"cursor after", crud.After("cursor"), "cursor"},
		{"cursor before", crud.Before("cursor"), "cursor"},
		{"primary", crud.PrimaryOnly(), "containing read"},
		{"unsorted", crud.Unsorted(), "deterministic"},
		{"skip total", crud.SkipTotal(), "total query"},
		{"row lock", crud.ForUpdate(), "lock"},
		{"distinct", crud.Distinct(), "DISTINCT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := crudtest.Postgres()
			o := crud.Build(crud.PreloadWhere("Comments", test.opt))
			err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
				articleMeta(t), []Article{}, o.Preloads, 0, nil)
			var schemaErr *crud.SchemaError
			if !errors.As(err, &schemaErr) || !strings.Contains(schemaErr.Reason, test.want) {
				t.Fatalf("RunPreloads error = %T %v, want SchemaError containing %q", err, err, test.want)
			}
			if schemaErr.Field != "Comments" {
				t.Fatalf("SchemaError field = %q, want preload path Comments", schemaErr.Field)
			}
			if len(rec.Statements()) != 0 {
				t.Fatalf("unsupported preload option reached SQL: %v", rec.SQL())
			}
		})
	}
}

func TestPreloadOptionsAreResolvedExactlyOnce(t *testing.T) {
	calls := 0
	option := crud.Option(func(o *crud.Options) {
		calls++
		o.Filter = append(o.Filter, crud.Eq("Approved", true))
	})
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(crud.PreloadWhere("Comments", option))

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	if calls != 1 {
		t.Fatalf("preload option calls = %d, want exactly one resolution", calls)
	}
	if got := rec.Last().Args; len(got) != 2 || got[1] != true {
		t.Fatalf("resolved preload args = %#v, want parent key and filter", got)
	}
}

func TestPreloadSortReplacementSurvivesInsideOneSpec(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(
		crud.PreloadWhere("Comments",
			crud.OrderBy(crud.Asc("ID")),
			crud.SortBy(crud.Desc("Body"))),
	)

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	sql := crudtest.Normalize(rec.Last().SQL)
	if !strings.Contains(sql, `ORDER BY "body" DESC`) || strings.Contains(sql, `"id" ASC`) {
		t.Fatalf("folded preload sort = %s, want the later SortBy replacement", sql)
	}
}

func TestPreloadSortsFromSeparateRequestsComposeInOrder(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(
		crud.PreloadWhere("Comments", crud.OrderBy(crud.Asc("ID"))),
		crud.PreloadWhere("Comments", crud.SortBy(crud.Desc("Body"))),
	)

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	if sql := crudtest.Normalize(rec.Last().SQL); !strings.Contains(sql, `ORDER BY "id" ASC, "body" DESC`) {
		t.Fatalf("sorts from separate preload requests = %s, want both in request order", sql)
	}
}

func TestPreloadCapsFromSeparateRequestsUseTheStrictestBudget(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(
		crud.PreloadWhere("Comments", crud.PreloadRows(1)),
		crud.PreloadWhere("Comments", crud.PreloadRows(5)),
	)

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	if sql := crudtest.Normalize(rec.Last().SQL); !strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("folded preload cap SQL = %s, want strictest cap plus one", sql)
	}
}

func TestPreloadRowsOptionIsConsumedAsAnExactRefusalCap(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), int64(1), int64(7), "first", true},
		[]any{int64(11), int64(1), int64(7), "second", true},
	))
	o := crud.Build(crud.PreloadWhere("Comments", crud.PreloadRows(1)))
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), articleMeta(t),
		[]Article{{ID: 1}}, o.Preloads, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "preload exceeds") {
		t.Fatalf("RunPreloads error = %v, want exact row-cap refusal", err)
	}
	if sql := rec.Last().SQL; !strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("preload SQL = %s, want cap plus one", sql)
	}
}

func TestNestedPreloadRowsCapsTheIntermediateHop(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), int64(1), int64(7), "first", true},
		[]any{int64(11), int64(1), int64(7), "second", true},
	))
	o := crud.Build(crud.PreloadWhere("Comments.Author", crud.PreloadRows(1)))
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), articleMeta(t),
		[]Article{{ID: 1}}, o.Preloads, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "preload exceeds") {
		t.Fatalf("RunPreloads error = %v, want intermediate-hop row-cap refusal", err)
	}
	if len(rec.Statements()) != 1 || !strings.Contains(rec.Last().SQL, "LIMIT 2") {
		t.Fatalf("nested preload statements = %v, want capped Comments query only", rec.SQL())
	}
}

func TestNegativePreloadCapsAreRefusedBeforeRowsOrSQL(t *testing.T) {
	tests := []crud.Option{
		crud.PreloadWhere("Comments", crud.PreloadRows(-1)),
		crud.PreloadCap("Comments", -1),
	}
	for i, option := range tests {
		rec := crudtest.Postgres()
		o := crud.Build(option)
		err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
			articleMeta(t), []Article{}, o.Preloads, 0, nil)
		if err == nil || !strings.Contains(err.Error(), "cannot be negative") {
			t.Fatalf("case %d error = %v, want negative-cap refusal", i, err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatalf("case %d reached SQL: %v", i, rec.SQL())
		}
	}
}

func TestMaximumPreloadCapDoesNotOverflowItsDetectionLimit(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(crud.PreloadWhere("Comments", crud.PreloadRows(math.MaxInt)))

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	want := fmt.Sprintf("LIMIT %d", math.MaxInt)
	if sql := rec.Last().SQL; !strings.Contains(sql, want) {
		t.Fatalf("maximum preload cap SQL = %s, want saturated %s", sql, want)
	}
}

func TestPreloadDepthIsCapped(t *testing.T) {
	rec := crudtest.Postgres()
	deep := strings.Repeat("Manager.", 5) + "Manager"
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(), metaOf[Person](t, "persons"),
		[]Person{{ID: 1}}, specs(deep), 0, nil)
	if err == nil {
		t.Fatal("a six-level preload path was accepted under the default cap of five")
	}
	if !strings.Contains(err.Error(), "deeper than") {
		t.Fatalf("err = %v, want it to name the depth limit", err)
	}

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

type namedBinaryKey []byte

type binaryKeyParent struct {
	ID       namedBinaryKey   `db:"id,pk"`
	Children []binaryKeyChild `rel:"has_many,fk=ParentID"`
}

type binaryKeyChild struct {
	ID       int64          `db:"id,pk"`
	ParentID namedBinaryKey `db:"parent_id"`
	Label    string         `db:"label"`
}

func TestPreloadKeepsDistinctNamedByteSliceKeysApart(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), namedBinaryKey{1}, "first"},
		[]any{int64(20), namedBinaryKey{2}, "second"},
	))
	parents := []binaryKeyParent{{ID: namedBinaryKey{1}}, {ID: namedBinaryKey{2}}}

	runPreloads(t, rec, metaOf[binaryKeyParent](t, "binary_key_parents"), parents, specs("Children")...)

	if len(parents[0].Children) != 1 || parents[0].Children[0].Label != "first" {
		t.Fatalf("first parent children = %+v", parents[0].Children)
	}
	if len(parents[1].Children) != 1 || parents[1].Children[0].Label != "second" {
		t.Fatalf("second parent children = %+v", parents[1].Children)
	}
	if got := rec.Last().Args; len(got) != 2 {
		t.Fatalf("preload deduplicated two distinct named []byte keys: %#v", got)
	} else if first, ok := got[0].([]byte); !ok || len(first) != 1 || first[0] != 1 {
		t.Fatalf("named []byte bind = %T %#v, want a driver-safe []byte", got[0], got[0])
	}
}

func TestPreloadKeepsANonNilEmptyByteKeyDistinctFromNull(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), namedBinaryKey{}, "empty"},
	))
	parents := []binaryKeyParent{{ID: nil}, {ID: namedBinaryKey{}}}

	runPreloads(t, rec, metaOf[binaryKeyParent](t, "empty_binary_key_parents"), parents, specs("Children")...)

	if len(parents[0].Children) != 0 {
		t.Fatalf("NULL byte-key parent children = %+v", parents[0].Children)
	}
	if len(parents[1].Children) != 1 || parents[1].Children[0].Label != "empty" {
		t.Fatalf("non-nil empty byte-key parent children = %+v", parents[1].Children)
	}
	args := rec.Last().Args
	if len(args) != 1 {
		t.Fatalf("empty byte-key binds = %#v, want one non-NULL key", args)
	}
	bound, ok := args[0].([]byte)
	if !ok || bound == nil || len(bound) != 0 {
		t.Fatalf("empty byte-key bind = %T %#v, want a non-nil empty []byte", args[0], args[0])
	}
}

type valuerBinaryKey struct{ raw []byte }

func (this valuerBinaryKey) Value() (driver.Value, error) {
	if this.raw == nil {
		return nil, nil
	}
	return append([]byte(nil), this.raw...), nil
}

func (this *valuerBinaryKey) Scan(src any) error {
	switch value := src.(type) {
	case []byte:
		this.raw = append(this.raw[:0], value...)
		return nil
	case string:
		this.raw = append(this.raw[:0], value...)
		return nil
	case valuerBinaryKey:
		this.raw = append(this.raw[:0], value.raw...)
		return nil
	default:
		return fmt.Errorf("scan valuerBinaryKey from %T", src)
	}
}

type valuerKeyParent struct {
	ID       valuerBinaryKey  `db:"id,pk"`
	Children []valuerKeyChild `rel:"has_many,fk=ParentID"`
}

type valuerKeyChild struct {
	ID       int64           `db:"id,pk"`
	ParentID valuerBinaryKey `db:"parent_id"`
	Label    string          `db:"label"`
}

func TestPreloadCanonicalisesNonComparableDriverValuerKeys(t *testing.T) {
	one := valuerBinaryKey{raw: []byte("one")}
	two := valuerBinaryKey{raw: []byte("two")}
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), one, "first"},
		[]any{int64(20), two, "second"},
	))
	parents := []valuerKeyParent{{ID: one}, {ID: two}}

	runPreloads(t, rec, metaOf[valuerKeyParent](t, "valuer_key_parents"), parents, specs("Children")...)

	if len(parents[0].Children) != 1 || parents[0].Children[0].Label != "first" {
		t.Fatalf("first Valuer parent children = %+v", parents[0].Children)
	}
	if len(parents[1].Children) != 1 || parents[1].Children[0].Label != "second" {
		t.Fatalf("second Valuer parent children = %+v", parents[1].Children)
	}
	if got := rec.Last().Args; len(got) != 2 {
		t.Fatalf("Valuer preload binds = %#v", got)
	} else if first, ok := got[0].([]byte); !ok || string(first) != "one" {
		t.Fatalf("Valuer bind = %T %#v, want resolved driver bytes", got[0], got[0])
	}
}

type oneShotRelationKey struct {
	raw   []byte
	calls *int
}

func (this *oneShotRelationKey) Value() (driver.Value, error) {
	*this.calls++
	if *this.calls != 1 {
		return []byte("changed"), nil
	}
	return this.raw, nil
}

type oneShotKeyParent struct {
	ID       oneShotRelationKey `db:"id,pk"`
	Children []oneShotKeyChild  `rel:"has_many,fk=ParentID"`
}

type oneShotKeyChild struct {
	ID       int64              `db:"id,pk"`
	ParentID oneShotRelationKey `db:"parent_id"`
}

func TestPreloadSnapshotsPointerOnlyDriverValuerExactlyOnce(t *testing.T) {
	calls := 0
	raw := []byte("stable")
	rec := crudtest.Postgres().Push(crudtest.Rows())
	parents := []oneShotKeyParent{{ID: oneShotRelationKey{raw: raw, calls: &calls}}}

	runPreloads(t, rec, metaOf[oneShotKeyParent](t, "one_shot_key_parents"), parents, specs("Children")...)

	if calls != 1 {
		t.Fatalf("pointer-only Valuer calls = %d, want exactly one", calls)
	}
	args := rec.Last().Args
	if len(args) != 1 {
		t.Fatalf("pointer-only Valuer binds = %#v, want one", args)
	}
	bound, ok := args[0].([]byte)
	if !ok || string(bound) != "stable" {
		t.Fatalf("pointer-only Valuer bind = %T %#v, want stable bytes", args[0], args[0])
	}
	raw[0] = 'X'
	if string(bound) != "stable" {
		t.Fatalf("driver bind aliases the Valuer buffer: %q", bound)
	}
}

type mutableDecimalCell struct{ digit byte }

func (this *mutableDecimalCell) Decompose(buf []byte) (byte, bool, []byte, int32) {
	return 0, false, append(buf[:0], this.digit), 0
}

type mutatingDecimalKey struct {
	cell  *mutableDecimalCell
	digit byte
}

func (this mutatingDecimalKey) Value() (driver.Value, error) {
	this.cell.digit = this.digit
	return this.cell, nil
}

type decimalKeyParent struct {
	ID       mutatingDecimalKey `db:"id,pk"`
	Children []decimalKeyChild  `rel:"has_many,fk=ParentID"`
}

type decimalKeyChild struct {
	ID       int64              `db:"id,pk"`
	ParentID mutatingDecimalKey `db:"parent_id"`
}

func TestPreloadRefusesMutableDecimalDriverValuesBeforeSQL(t *testing.T) {
	shared := &mutableDecimalCell{}
	rec := crudtest.Postgres()
	parents := []decimalKeyParent{
		{ID: mutatingDecimalKey{cell: shared, digit: 1}},
		{ID: mutatingDecimalKey{cell: shared, digit: 2}},
	}

	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		metaOf[decimalKeyParent](t, "decimal_key_parents"), parents, specs("Children"), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported relation key type") {
		t.Fatalf("RunPreloads error = %T %v, want mutable decimal refusal", err, err)
	}
	var schemaErr *crud.SchemaError
	if errors.As(err, &schemaErr) {
		t.Fatalf("a runtime Valuer result was exposed as a declaration/client error: %v", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a mutable decimal relation key reached SQL: %v", rec.SQL())
	}
}

var errRelationKeyValue = errors.New("relation key value failed")

type failedRelationKey string

func (failedRelationKey) Value() (driver.Value, error) { return nil, errRelationKeyValue }

type failedKeyParent struct {
	ID       failedRelationKey `db:"id,pk"`
	Children []failedKeyChild  `rel:"has_many,fk=ParentID"`
}

type failedKeyChild struct {
	ID       int64             `db:"id,pk"`
	ParentID failedRelationKey `db:"parent_id"`
}

func TestPreloadPropagatesDriverValuerFailuresWithoutQuerying(t *testing.T) {
	rec := crudtest.Postgres()
	parents := []failedKeyParent{{ID: failedRelationKey("broken")}}
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		metaOf[failedKeyParent](t, "failed_key_parents"), parents, specs("Children"), 0, nil)
	if !errors.Is(err, errRelationKeyValue) {
		t.Fatalf("RunPreloads error = %v, want the Valuer cause", err)
	}
	var schemaErr *crud.SchemaError
	if errors.As(err, &schemaErr) {
		t.Fatalf("runtime Valuer failure was exposed as a declaration error: %v", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a failed Valuer reached SQL: %v", rec.SQL())
	}
}

type nilJSONRelationKey []string

func (nilJSONRelationKey) Value() (driver.Value, error) { return []byte("null-json"), nil }

type nilValuerKeyParent struct {
	ID       nilJSONRelationKey  `db:"id,pk"`
	Children []nilValuerKeyChild `rel:"has_many,fk=ParentID"`
}

type nilValuerKeyChild struct {
	ID       int64              `db:"id,pk"`
	ParentID nilJSONRelationKey `db:"parent_id"`
}

func TestPreloadLetsANilNonPointerValuerDefineANonNullKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	parents := []nilValuerKeyParent{{ID: nil}}

	runPreloads(t, rec, metaOf[nilValuerKeyParent](t, "nil_valuer_key_parents"), parents, specs("Children")...)

	args := rec.Last().Args
	if len(args) != 1 {
		t.Fatalf("nil non-pointer Valuer binds = %#v, want its non-NULL driver value", args)
	}
	bound, ok := args[0].([]byte)
	if !ok || string(bound) != "null-json" {
		t.Fatalf("nil non-pointer Valuer bind = %T %#v, want resolved bytes", args[0], args[0])
	}
}

type nilPointerJSONRelationKey []string

func (*nilPointerJSONRelationKey) Value() (driver.Value, error) {
	return []byte("pointer-null-json"), nil
}

type nilPointerValuerKeyParent struct {
	ID       nilPointerJSONRelationKey  `db:"id,pk"`
	Children []nilPointerValuerKeyChild `rel:"has_many,fk=ParentID"`
}

type nilPointerValuerKeyChild struct {
	ID       int64                     `db:"id,pk"`
	ParentID nilPointerJSONRelationKey `db:"parent_id"`
}

func TestPreloadLetsANilPointerOnlyValuerDefineANonNullKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	parents := []nilPointerValuerKeyParent{{ID: nil}}

	runPreloads(t, rec, metaOf[nilPointerValuerKeyParent](t, "nil_pointer_valuer_key_parents"), parents, specs("Children")...)

	args := rec.Last().Args
	if len(args) != 1 {
		t.Fatalf("nil pointer-only Valuer binds = %#v, want its non-NULL driver value", args)
	}
	bound, ok := args[0].([]byte)
	if !ok || string(bound) != "pointer-null-json" {
		t.Fatalf("nil pointer-only Valuer bind = %T %#v, want resolved bytes", args[0], args[0])
	}
}

type nilPointerRelationKey struct{}

func (*nilPointerRelationKey) Value() (driver.Value, error) {
	return []byte("typed-nil-pointer"), nil
}

type typedNilPointerKeyParent struct {
	ID       *nilPointerRelationKey    `db:"id,pk"`
	Children []typedNilPointerKeyChild `rel:"has_many,fk=ParentID"`
}

type typedNilPointerKeyChild struct {
	ID       int64                  `db:"id,pk"`
	ParentID *nilPointerRelationKey `db:"parent_id"`
}

func TestPreloadLetsATypedNilPointerOnlyValuerDefineANonNullKey(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	parents := []typedNilPointerKeyParent{{ID: nil}}

	runPreloads(t, rec, metaOf[typedNilPointerKeyParent](t, "typed_nil_pointer_key_parents"), parents, specs("Children")...)

	args := rec.Last().Args
	if len(args) != 1 {
		t.Fatalf("typed nil pointer-only Valuer binds = %#v, want its non-NULL driver value", args)
	}
	bound, ok := args[0].([]byte)
	if !ok || string(bound) != "typed-nil-pointer" {
		t.Fatalf("typed nil pointer-only Valuer bind = %T %#v, want resolved bytes", args[0], args[0])
	}
}

type optionalValuerKeyParent struct {
	ID       utils.Opt[oneShotRelationKey] `db:"id,pk"`
	Children []optionalValuerKeyChild      `rel:"has_many,fk=ParentID"`
}

type optionalValuerKeyChild struct {
	ID       int64                         `db:"id,pk"`
	ParentID utils.Opt[oneShotRelationKey] `db:"parent_id"`
}

func TestPreloadUnwrapsOptBeforeDiscoveringAPointerOnlyValuer(t *testing.T) {
	calls := 0
	rec := crudtest.Postgres().Push(crudtest.Rows())
	parents := []optionalValuerKeyParent{{ID: utils.Set(oneShotRelationKey{
		raw: []byte("optional"), calls: &calls,
	})}}

	runPreloads(t, rec, metaOf[optionalValuerKeyParent](t, "optional_valuer_key_parents"), parents, specs("Children")...)

	if calls != 1 {
		t.Fatalf("Opt pointer-only Valuer calls = %d, want exactly one", calls)
	}
	args := rec.Last().Args
	if len(args) != 1 {
		t.Fatalf("Opt pointer-only Valuer binds = %#v, want one", args)
	}
	bound, ok := args[0].([]byte)
	if !ok || string(bound) != "optional" {
		t.Fatalf("Opt pointer-only Valuer bind = %T %#v, want resolved bytes", args[0], args[0])
	}
}

type nilOptPointerKeyParent struct {
	ID       *utils.Opt[int64]       `db:"id,pk"`
	Children []nilOptPointerKeyChild `rel:"has_many,fk=ParentID"`
}

type nilOptPointerKeyChild struct {
	ID       int64             `db:"id,pk"`
	ParentID *utils.Opt[int64] `db:"parent_id"`
}

func TestPreloadTreatsANilOptPointerAsNullWithoutPanicking(t *testing.T) {
	rec := crudtest.Postgres()
	parents := []nilOptPointerKeyParent{{ID: nil}}

	runPreloads(t, rec, metaOf[nilOptPointerKeyParent](t, "nil_opt_pointer_key_parents"), parents, specs("Children")...)

	if len(rec.Statements()) != 0 {
		t.Fatalf("nil *Opt relation key reached SQL: %v", rec.SQL())
	}
}

type pointerKeyParent struct {
	ID       **int64           `db:"id,pk"`
	Children []pointerKeyChild `rel:"has_many,fk=ParentID"`
}

type pointerKeyChild struct {
	ID       int64   `db:"id,pk"`
	ParentID **int64 `db:"parent_id"`
	Label    string  `db:"label"`
}

func nestedInt64(value int64) **int64 {
	one := &value
	return &one
}

func TestPreloadCanonicalisesNestedPointersByValueNotAddress(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), int64(1), "first"},
		[]any{int64(20), int64(2), "second"},
	))
	parents := []pointerKeyParent{{ID: nestedInt64(1)}, {ID: nestedInt64(2)}}

	runPreloads(t, rec, metaOf[pointerKeyParent](t, "pointer_key_parents"), parents, specs("Children")...)

	if len(parents[0].Children) != 1 || parents[0].Children[0].Label != "first" {
		t.Fatalf("first nested-pointer parent children = %+v", parents[0].Children)
	}
	if len(parents[1].Children) != 1 || parents[1].Children[0].Label != "second" {
		t.Fatalf("second nested-pointer parent children = %+v", parents[1].Children)
	}
	if got := rec.Last().Args; len(got) != 2 || got[0] != int64(1) || got[1] != int64(2) {
		t.Fatalf("nested-pointer binds = %#v, want scalar values", got)
	}
}

type relationFlag bool

type scalarKeyParent struct {
	ID       relationFlag     `db:"id,pk"`
	Children []scalarKeyChild `rel:"has_many,fk=ParentID"`
}

type scalarKeyChild struct {
	ID       int64 `db:"id,pk"`
	ParentID bool  `db:"parent_id"`
}

type floatKeyParent struct {
	ID       float32         `db:"id,pk"`
	Children []floatKeyChild `rel:"has_many,fk=ParentID"`
}

type floatKeyChild struct {
	ID       int64   `db:"id,pk"`
	ParentID float64 `db:"parent_id"`
}

func TestPreloadCanonicalisesDriverScalarKindsAcrossRelationEnds(t *testing.T) {
	t.Run("named bool and bool", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(10), true}))
		parents := []scalarKeyParent{{ID: relationFlag(true)}}
		runPreloads(t, rec, metaOf[scalarKeyParent](t, "scalar_key_parents"), parents, specs("Children")...)
		if len(parents[0].Children) != 1 {
			t.Fatalf("named bool parent children = %+v", parents[0].Children)
		}
	})

	t.Run("float32 and float64", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(10), float64(1.5)}))
		parents := []floatKeyParent{{ID: float32(1.5)}}
		runPreloads(t, rec, metaOf[floatKeyParent](t, "float_key_parents"), parents, specs("Children")...)
		if len(parents[0].Children) != 1 {
			t.Fatalf("float32 parent children = %+v", parents[0].Children)
		}
	})
}

func TestPreloadRefusesNaNRelationKeysBeforeSQL(t *testing.T) {
	rec := crudtest.Postgres()
	parents := []floatKeyParent{{ID: float32(math.NaN())}}
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		metaOf[floatKeyParent](t, "nan_key_parents"), parents, specs("Children"), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "NaN relation keys") {
		t.Fatalf("RunPreloads error = %T %v, want portable NaN refusal", err, err)
	}
	var schemaErr *crud.SchemaError
	if errors.As(err, &schemaErr) {
		t.Fatalf("a runtime NaN value was exposed as a declaration/client error: %v", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a NaN relation key reached SQL: %v", rec.SQL())
	}
}

type unsupportedRelationKey []string

type unsupportedKeyParent struct {
	ID       unsupportedRelationKey `db:"id,pk"`
	Children []unsupportedKeyChild  `rel:"has_many,fk=ParentID"`
}

type unsupportedKeyChild struct {
	ID       int64                  `db:"id,pk"`
	ParentID unsupportedRelationKey `db:"parent_id"`
}

type dynamicRelationKey [1]any

type dynamicKeyParent struct {
	ID       dynamicRelationKey `db:"id,pk"`
	Children []dynamicKeyChild  `rel:"has_many,fk=ParentID"`
}

type dynamicKeyChild struct {
	ID       int64              `db:"id,pk"`
	ParentID dynamicRelationKey `db:"parent_id"`
}

func TestPreloadRefusesADeclaredNonComparableRelationKeyBeforeSQL(t *testing.T) {
	rec := crudtest.Postgres()
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		metaOf[unsupportedKeyParent](t, "unsupported_key_parents"),
		[]unsupportedKeyParent{{ID: unsupportedRelationKey{"a"}}}, specs("Children"), 0, nil)
	var schemaErr *crud.SchemaError
	if !errors.As(err, &schemaErr) || !strings.Contains(schemaErr.Reason, "not comparable") {
		t.Fatalf("RunPreloads error = %T %v, want an actionable SchemaError", err, err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("an invalid relation key reached SQL: %v", rec.SQL())
	}
}

func TestPreloadRefusesADynamicallyNonComparableKeyWithoutPanicking(t *testing.T) {
	rec := crudtest.Postgres()
	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		metaOf[dynamicKeyParent](t, "dynamic_key_parents"),
		[]dynamicKeyParent{{ID: dynamicRelationKey{[]byte("not-comparable")}}}, specs("Children"), 0, nil)
	if err == nil || !strings.Contains(err.Error(), "not comparable") {
		t.Fatalf("RunPreloads error = %T %v, want an actionable runtime mapping error", err, err)
	}
	var schemaErr *crud.SchemaError
	if errors.As(err, &schemaErr) {
		t.Fatalf("a dynamic server value was exposed as a declaration/client error: %v", err)
	}
	if len(rec.Statements()) != 0 {
		t.Fatalf("a dynamically invalid relation key reached SQL: %v", rec.SQL())
	}
}
