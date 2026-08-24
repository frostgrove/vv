package basic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shardit-io/go-rx-crud/crud"
	"github.com/shardit-io/go-rx-crud/crud/crudtest"
	"github.com/shardit-io/go-rx-crud/repo/basic"
)

// A tree of soft-deletable rows, all in one table. The scope is the whole point
// of the model: a tombstone must not come back through any door.
type Node struct {
	ID       int64  `db:"id,pk,auto"`
	ParentID int64  `db:"parent_id"`
	Deleted  bool   `db:"deleted"`
	Name     string `db:"name"`

	Children []Node `rel:"has_many,fk=ParentID"`
}

const nodeCols = `"id", "parent_id", "deleted", "name"`

var liveNodes = basic.Define[Node, int64, struct{}]("nodes", basic.Scope(crud.Eq("Deleted", false)))

func nodeRow(id, parent int64, deleted bool, name string) []any {
	return []any{id, parent, deleted, name}
}

// The scope exists so a tombstone is unreachable. A preload is a second
// statement against the same table, so unless the narrowing travels with it,
// ?preload=children hands back exactly the rows the scope was declared to hide.
func TestAPreloadOfTheRepositorysOwnModelCarriesItsScope(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(nodeRow(1, 0, false, "root")),
		crudtest.Rows(), // no live children
	)

	if _, err := liveNodes.Bind(rec).GetAll(context.Background(), crud.Preload("Children")); err != nil {
		t.Fatal(err)
	}

	wantSQL(t, mustSQL(t, rec, 1).SQL,
		`SELECT `+nodeCols+` FROM "nodes" WHERE "parent_id" IN ($1) AND "deleted" = $2`)
	if args := mustSQL(t, rec, 1).Args; len(args) != 2 || args[1] != false {
		t.Fatalf("the preload bound %#v; the scope has to reach the children too, or a "+
			"tombstone comes back as a child of a live row", args)
	}
}

// A tombstone reached through two hops is still a tombstone. The narrowing is
// declared for the model, not for one path, so depth cannot shake it off.
func TestANestedPreloadOfTheSameModelCarriesTheScopeAtEveryLevel(t *testing.T) {
	rec := crudtest.Postgres().Push(
		crudtest.Rows(nodeRow(1, 0, false, "root")),
		crudtest.Rows(nodeRow(2, 1, false, "child")),
		crudtest.Rows(),
	)

	if _, err := liveNodes.Bind(rec).GetAll(context.Background(),
		crud.Preload("Children.Children")); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{1, 2} {
		if got := mustSQL(t, rec, i).SQL; !strings.Contains(got, `"deleted" = `) {
			t.Fatalf("hop %d ran unscoped: %s", i, got)
		}
	}
}

// A filter that hops a relation opens a correlated EXISTS with its own FROM, so
// it inherits nothing from the outer WHERE. Left alone it is an oracle: the
// client cannot read a deleted row, but it can ask whether one exists and what
// it says, and read the answer off the parent page.
func TestARelationFilterCarriesTheScopeIntoItsSubquery(t *testing.T) {
	rec := crudtest.Postgres().Push(crudtest.Rows())

	if _, err := liveNodes.Bind(rec).GetAll(context.Background(),
		crud.Where(crud.Eq("Children.Name", "TOMBSTONE"))); err != nil {
		t.Fatal(err)
	}

	wantSQL(t, rec.Last().SQL,
		`SELECT `+nodeCols+` FROM "nodes" WHERE ("deleted" = $1 AND EXISTS (`+
			`SELECT 1 FROM "nodes" AS rx1 WHERE rx1."parent_id" = "nodes"."id" `+
			`AND rx1."deleted" = $2 AND rx1."name" = $3))`)
}

// The scalar subquery a nested sort opens has the same hole as the EXISTS: it
// picks a value out of the related table with no narrowing, so a row could be
// ordered by a deleted relative's column.
func TestANestedSortCarriesTheScopeIntoItsSubquery(t *testing.T) {
	type Employee struct {
		ID        int64  `db:"id,pk,auto"`
		ManagerID int64  `db:"manager_id"`
		Retired   bool   `db:"retired"`
		Name      string `db:"name"`

		Manager *Employee `rel:"belongs_to,fk=ManagerID"`
	}
	current := basic.Define[Employee, int64, struct{}]("employees", basic.Scope(crud.Eq("Retired", false)))

	rec := crudtest.Postgres().Push(crudtest.Rows())
	if _, err := current.Bind(rec).GetAll(context.Background(),
		crud.OrderBy(crud.Asc("Manager.Name")), crud.Limit(10)); err != nil {
		t.Fatal(err)
	}

	wantSQL(t, rec.Last().SQL,
		`SELECT "id", "manager_id", "retired", "name" FROM "employees" WHERE "retired" = $1 `+
			`ORDER BY (SELECT rx1."name" FROM "employees" AS rx1 WHERE rx1."id" = "employees"."manager_id" `+
			`AND rx1."retired" = $2 LIMIT 1) ASC, "id" ASC LIMIT 10`)
}

// ---------------------------------------------------------------------------
// a relation to another model

type Post struct {
	ID      int64     `db:"id,pk,auto"`
	Title   string    `db:"title"`
	Hidden  bool      `db:"hidden"`
	Remarks []Remark  `rel:"has_many"`
	Author  *Reviewer `rel:"belongs_to,fk=AuthorID"`

	AuthorID int64 `db:"author_id"`
}

type Remark struct {
	ID     int64  `db:"id,pk,auto"`
	PostID int64  `db:"post_id"`
	Spam   bool   `db:"spam"`
	Body   string `db:"body"`
}

type Reviewer struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

// Another model's rows are another repository's business, so the narrowing has
// to be declared — but once it is, it holds on both routes into that table.
func TestRelationScopeNarrowsBothThePreloadAndTheFilterHop(t *testing.T) {
	posts := basic.Define[Post, int64, struct{}]("posts",
		basic.Scope(crud.Eq("Hidden", false)),
		basic.RelationScope("Remarks", crud.Eq("Spam", false)))

	t.Run("preload", func(t *testing.T) {
		rec := crudtest.Postgres().Push(
			crudtest.Rows([]any{int64(1), "t", false, int64(0)}),
			crudtest.Rows(),
		)
		if _, err := posts.Bind(rec).GetAll(context.Background(), crud.Preload("Remarks")); err != nil {
			t.Fatal(err)
		}
		wantSQL(t, mustSQL(t, rec, 1).SQL,
			`SELECT "id", "post_id", "spam", "body" FROM "remarks" WHERE "post_id" IN ($1) AND "spam" = $2`)
	})

	t.Run("filter hop", func(t *testing.T) {
		rec := crudtest.Postgres().Push(crudtest.Rows())
		if _, err := posts.Bind(rec).GetAll(context.Background(),
			crud.Where(crud.Eq("Remarks.Body", "buy pills"))); err != nil {
			t.Fatal(err)
		}
		wantSQL(t, rec.Last().SQL,
			`SELECT "id", "title", "hidden", "author_id" FROM "posts" WHERE ("hidden" = $1 AND EXISTS (`+
				`SELECT 1 FROM "remarks" AS rx1 WHERE rx1."post_id" = "posts"."id" `+
				`AND rx1."spam" = $2 AND rx1."body" = $3))`)
	})

	t.Run("a relation nobody narrowed is still read raw", func(t *testing.T) {
		// The honest half of the contract: RelationScope is per relation, and a
		// relation without one is not narrowed. If this ever starts failing
		// because some other model's scope leaked in, that is the bug.
		rec := crudtest.Postgres().Push(
			crudtest.Rows([]any{int64(1), "t", false, int64(9)}),
			crudtest.Rows(),
		)
		if _, err := posts.Bind(rec).GetAll(context.Background(), crud.Preload("Author")); err != nil {
			t.Fatal(err)
		}
		wantSQL(t, mustSQL(t, rec, 1).SQL,
			`SELECT "id", "name" FROM "reviewers" WHERE "id" IN ($1)`)
	})
}

// A caller's own PreloadWhere narrows further; it can never take the
// repository's narrowing back off.
func TestACallerCannotWidenARelationScope(t *testing.T) {
	posts := basic.Define[Post, int64, struct{}]("posts",
		basic.RelationScope("Remarks", crud.Eq("Spam", false)))

	rec := crudtest.Postgres().Push(
		crudtest.Rows([]any{int64(1), "t", false, int64(0)}),
		crudtest.Rows(),
	)
	if _, err := posts.Bind(rec).GetAll(context.Background(),
		crud.PreloadWhere("Remarks", crud.Where(crud.Eq("Spam", true)))); err != nil {
		t.Fatal(err)
	}

	st := mustSQL(t, rec, 1)
	wantSQL(t, st.SQL,
		`SELECT "id", "post_id", "spam", "body" FROM "remarks" WHERE "post_id" IN ($1) `+
			`AND ("spam" = $2 AND "spam" = $3)`)
	if args := st.Args; len(args) != 3 || args[1] != false || args[2] != true {
		t.Fatalf("bound %#v, want the repository's narrowing first and the caller's second", args)
	}
}

// A path that names no relation is a typo, and a typo that silently narrows
// nothing is the worst kind — the declaration reads as protection and is not.
func TestRelationScopeRefusesAPathTheModelDoesNotHave(t *testing.T) {
	_, err := basic.TryDefine[Post, int64, struct{}]("posts",
		basic.RelationScope("Remrks", crud.Eq("Spam", false)))
	if err == nil {
		t.Fatal("a RelationScope on a relation the model does not declare was accepted")
	}
	if !strings.Contains(err.Error(), "Remrks") {
		t.Fatalf("err = %v, want a message naming the path that does not resolve", err)
	}
}
