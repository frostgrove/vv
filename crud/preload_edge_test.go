package crud_test

import (
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
)

func TestAPreloadedToOneIsNotSharedBetweenParents(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows([]any{int64(7), "ann", "berlin"}))
	articles := []Article{{ID: 1, AuthorID: 7}, {ID: 2, AuthorID: 7}}

	runPreloads(t, rec, articleMeta(t), articles, specs("Author")...)

	if articles[0].Author == nil || articles[1].Author == nil {
		t.Fatalf("the author did not load: %+v", articles)
	}
	if articles[0].Author == articles[1].Author {
		t.Fatal("both articles point at one Author; editing one row's child edits its siblings'")
	}
	articles[0].Author.Name = "redacted"
	if articles[1].Author.Name != "ann" {
		t.Fatalf("the second article's author became %q when the first one's was rewritten",
			articles[1].Author.Name)
	}
}

type plOwner struct {
	ID   int64   `db:"id,pk,auto"`
	Name string  `db:"name"`
	Pets []plPet `rel:"has_many,fk=OwnerID"`
}

type plPet struct {
	ID      int64  `db:"id,pk,auto"`
	OwnerID int64  `db:"owner_id"`
	Name    string `db:"name"`
}

type plTicket struct {
	ID      int64    `db:"id,pk,auto"`
	OwnerID int64    `db:"owner_id"`
	Owner   *plOwner `rel:"belongs_to,fk=OwnerID"`
}

func TestANestedPreloadFillsEveryParentsOwnCopy(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(7), "ann"}),
		crudtest.Rows([]any{int64(70), int64(7), "rex"}),
	)
	tickets := []plTicket{{ID: 1, OwnerID: 7}, {ID: 2, OwnerID: 7}}

	runPreloads(t, rec, metaOf[plTicket](t, "pl_tickets"), tickets, specs("Owner.Pets")...)

	for i, tk := range tickets {
		if tk.Owner == nil || len(tk.Owner.Pets) != 1 || tk.Owner.Pets[0].Name != "rex" {
			t.Fatalf("ticket %d ended up with %+v; the second hop has to reach every copy", i, tk.Owner)
		}
	}
	if tickets[0].Owner == tickets[1].Owner {
		t.Fatal("the two tickets share one owner")
	}
}

func TestABarePreloadWinsOverANarrowedOneForTheSamePath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []crud.Option
	}{
		{"the bare request first", []crud.Option{
			crud.Preload("Comments"),
			crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))),
		}},
		{"and the other way round", []crud.Option{
			crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))),
			crud.Preload("Comments"),
		}},
		{"across equivalent path spellings", []crud.Option{
			crud.Preload("comments"),
			crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			o := crud.Build(tc.options...)

			if err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
				articleMeta(t), []Article{{ID: 1}}, o.Preloads, 0, nil); err != nil {
				t.Fatal(err)
			}
			if n := len(rec.Statements()); n != 1 {
				t.Fatalf("%d statements, want the two requests folded into one: %v", n, rec.SQL())
			}
			_, where, _ := strings.Cut(rec.Last().SQL, " WHERE ")
			if strings.Contains(where, " AND ") {
				t.Fatalf("the preload ran as %s; the wider request asked for every comment "+
					"and would have received a subset", rec.Last().SQL)
			}
		})
	}
}

func TestABarePreloadCannotHideAnUnsupportedOptionForTheSamePath(t *testing.T) {
	for _, options := range [][]crud.Option{
		{
			crud.Preload("Comments"),
			crud.PreloadWhere("Comments", crud.Select("Body")),
		},
		{
			crud.PreloadWhere("Comments", crud.Select("Body")),
			crud.Preload("Comments"),
		},
	} {
		rec := crudtest.Postgres()
		o := crud.Build(options...)
		err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
			articleMeta(t), []Article{}, o.Preloads, 0, nil)
		if err == nil || !strings.Contains(err.Error(), "projection") {
			t.Fatalf("RunPreloads error = %v, want unsupported projection refusal", err)
		}
		if len(rec.Statements()) != 0 {
			t.Fatalf("unsupported option reached SQL: %v", rec.SQL())
		}
	}
}

func TestABarePreloadKeepsOrthogonalSortAndRowCap(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows(
		[]any{int64(10), int64(1), int64(7), "first", true},
		[]any{int64(11), int64(1), int64(7), "second", true},
	))
	o := crud.Build(
		crud.PreloadWhere("Comments",
			crud.Where(crud.Eq("Approved", false)),
			crud.SortBy(crud.Desc("Body")),
			crud.PreloadRows(1)),
		crud.Preload("Comments"),
	)

	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		articleMeta(t), []Article{{ID: 1}}, o.Preloads, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "preload exceeds") {
		t.Fatalf("RunPreloads error = %v, want retained row-cap refusal", err)
	}
	sql := crudtest.Normalize(rec.Last().SQL)
	if strings.Contains(sql, `"approved" =`) {
		t.Fatalf("bare preload retained the narrower filter: %s", sql)
	}
	if !strings.Contains(sql, `ORDER BY "body" DESC`) || !strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("bare preload dropped orthogonal sort/cap: %s", sql)
	}
}

func TestANonFilteringPreloadRequestWinsOverAFilteredOne(t *testing.T) {
	for _, test := range []struct {
		name  string
		whole crud.Option
		want  string
	}{
		{"sort only", crud.PreloadWhere("Comments", crud.SortBy(crud.Desc("Body"))), `ORDER BY "body" DESC`},
		{"cap only", crud.PreloadWhere("Comments", crud.PreloadRows(2)), "LIMIT 3"},
		{"nil filter", crud.PreloadWhere("Comments", crud.Where(nil)), ""},
		{"true filter", crud.PreloadWhere("Comments", crud.Where(crud.True())), ""},
		{"empty not-in filter", crud.PreloadWhere("Comments", crud.Where(crud.NotIn("Approved"))), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			o := crud.Build(
				test.whole,
				crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", false))),
			)
			runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)
			sql := crudtest.Normalize(rec.Last().SQL)
			if strings.Contains(sql, `"approved" =`) {
				t.Fatalf("non-filtering request retained a narrower filter: %s", sql)
			}
			if test.want != "" && !strings.Contains(sql, test.want) {
				t.Fatalf("non-filtering request dropped %q: %s", test.want, sql)
			}
		})
	}
}

func TestAFilterReplacementInsideOneSpecRemainsANarrowing(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	replaceFilter := crud.Option(func(o *crud.Options) {
		o.Filter = []crud.Predicate{crud.Eq("AuthorID", int64(7))}
	})
	o := crud.Build(
		crud.PreloadWhere("Comments",
			crud.Where(crud.Eq("Approved", false)),
			replaceFilter),
	)

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	sql := crudtest.Normalize(rec.Last().SQL)
	if !strings.Contains(sql, `"author_id" =`) || strings.Contains(sql, `"approved" =`) {
		t.Fatalf("replacement filter was widened or composed unexpectedly: %s", sql)
	}
}

func TestFiltersFromSeparatePreloadRequestsAreIntersected(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	replaceFilter := crud.Option(func(o *crud.Options) {
		o.Filter = []crud.Predicate{crud.Eq("AuthorID", int64(7))}
	})
	o := crud.Build(
		crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", false))),
		crud.PreloadWhere("Comments", replaceFilter),
	)

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	sql := crudtest.Normalize(rec.Last().SQL)
	if !strings.Contains(sql, `"author_id" =`) || !strings.Contains(sql, `"approved" =`) {
		t.Fatalf("separate preload narrowings were not intersected: %s", sql)
	}
}

func TestAnUnnarrowedNestedPathMakesItsPrefixWhole(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(
		crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", false))),
		crud.Preload("Comments.Author"),
	)

	runPreloads(t, rec, articleMeta(t), []Article{{ID: 1}}, o.Preloads...)

	if sql := crudtest.Normalize(rec.Last().SQL); strings.Contains(sql, `"approved" =`) {
		t.Fatalf("unnarrowed nested path retained a filter on its prefix: %s", sql)
	}
}

func TestAUnsupportedOptionCannotBeCanceledByALaterOption(t *testing.T) {
	rec := crudtest.Postgres()
	clearProjection := crud.Option(func(o *crud.Options) { o.Fields = nil })
	o := crud.Build(
		crud.PreloadWhere("Comments", crud.Select("Body")),
		crud.PreloadWhere("Comments", clearProjection),
	)

	err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		articleMeta(t), []Article{}, o.Preloads, 0, nil)
	if err == nil || !strings.Contains(err.Error(), "projection") {
		t.Fatalf("RunPreloads error = %v, want the unsupported option at its own boundary", err)
	}
}

func TestTwoNarrowedPreloadsOfOnePathStillBothApply(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())
	o := crud.Build(
		crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))),
		crud.PreloadWhere("Comments", crud.Where(crud.Eq("Body", "hi"))),
	)

	if err := crud.RunPreloads(context.Background(), rec, rec.Dialect(),
		articleMeta(t), []Article{{ID: 1}}, o.Preloads, 0, nil); err != nil {
		t.Fatal(err)
	}
	_, where, _ := strings.Cut(rec.Last().SQL, " WHERE ")
	if !strings.Contains(where, `"approved" = `) || !strings.Contains(where, `"body" = `) {
		t.Fatalf("the preload ran as %s, want both narrowings", rec.Last().SQL)
	}
}
