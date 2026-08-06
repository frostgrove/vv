package crud_test

import (
	"context"
	"strings"
	"testing"

	"rx-crud/crud"
	"rx-crud/crud/crudtest"
)

// Two articles by the same author must not end up holding the same *Author.
// They did, and which spelling of the relation field was used decided it: the
// value form has always copied. Anything that walks the page and rewrites a
// child — a redaction pass, a presenter, a service normalising a name —
// rewrote it for every sibling that happened to share the row.
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

// Copying per parent only helps if the second level then fills every copy —
// otherwise the nested preload writes into an object nobody holds.
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

// Folding two requests for one relation into a single query is what lets
// "Comments" and "Comments.Author" share a statement. Folding their narrowings
// together is a different thing: a request that asks for all of them and for a
// subset would receive only the subset, with a 200 and no way to notice.
func TestABarePreloadWinsOverANarrowedOneForTheSamePath(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []crud.Option
	}{
		{"the bare request first", []crud.Option{
			crud.Preload("Comments"),
			crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))),
		}},
		{"and the other way round", []crud.Option{
			crud.PreloadWhere("Comments", crud.Where(crud.Eq("Approved", true))),
			crud.Preload("Comments"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := crudtest.Postgres().Push(crudtest.Rows())
			o := crud.Build(tc.opts...)

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

// Narrowing the same path twice is still an intersection — only an unnarrowed
// request widens it back out.
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
