package crud_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func commentScope(t *testing.T, rs *crud.RelationScopes, path string) string {
	t.Helper()
	p := rs.At(path, metaOf[Comment](t, "comments"))
	if p == nil {
		return ""
	}
	b := crud.NewSQL(crud.Postgres{}, metaOf[Comment](t, "comments"))
	b.Predicate(p)
	sql, _, err := b.Done()
	if err != nil {
		t.Fatalf("the merged scope does not render: %v", err)
	}
	return sql
}

func TestMergingTwoNarrowingsOfOneRelationKeepsBoth(t *testing.T) {
	permanent := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("Approved", true))
	perRequest := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("AuthorID", int64(7)))

	got := commentScope(t, crud.MergeRelationScopes(permanent, perRequest), "Comments")
	if want := `("approved" = $1 AND "author_id" = $2)`; got != want {
		t.Fatalf("the merged narrowing is %s\nwant %s — one of the two declarations was dropped", got, want)
	}

	ct := reflect.TypeOf(Comment{})
	byModel := crud.MergeRelationScopes(
		(*crud.RelationScopes)(nil).ForModel(ct, crud.Eq("Approved", true)),
		(*crud.RelationScopes)(nil).ForModel(ct, crud.Eq("AuthorID", int64(7))),
	)
	if got := commentScope(t, byModel, "Comments"); got != `("approved" = $1 AND "author_id" = $2)` {
		t.Fatalf("merging two model declarations gave %s, want both of them", got)
	}
}

func TestMergingDoesNotLeaveTheRequestsNarrowingOnTheRepositorysOwn(t *testing.T) {
	permanent := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("Approved", true))
	perRequest := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("AuthorID", int64(7)))

	merged := crud.MergeRelationScopes(permanent, perRequest)

	if got := commentScope(t, permanent, "Comments"); got != `"approved" = $1` {
		t.Fatalf("the repository's own narrowing became %s after one request merged into it", got)
	}
	if got := commentScope(t, perRequest, "Comments"); got != `"author_id" = $1` {
		t.Fatalf("the request's narrowing became %s after being merged", got)
	}

	if got := commentScope(t, merged, "Comments"); got == `"approved" = $1` {
		t.Fatal("the merge returned the left side unchanged, so nothing above is being tested")
	}
}

func TestMergingWithNothingDeclaredHandsBackTheOtherSide(t *testing.T) {
	declared := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("Approved", true))

	if got := crud.MergeRelationScopes(nil, declared); got != declared {
		t.Fatal("merging into nothing copied the declared scopes instead of handing them back")
	}
	if got := crud.MergeRelationScopes(declared, nil); got != declared {
		t.Fatal("merging nothing in copied the declared scopes instead of handing them back")
	}
	if got := crud.MergeRelationScopes(nil, nil); !got.Empty() {
		t.Fatal("merging two empty scopes produced something that claims to narrow a relation")
	}
}

func TestTheRelationScopeBuildersDoNotMutateWhatTheyWereCalledOn(t *testing.T) {
	m := articleMeta(t)
	base := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("Approved", true))

	one := base.AtPath("Author", crud.Eq("AuthorID", 1))
	two := base.AtPath("Author", crud.Eq("AuthorID", 2))

	if base.At("Author", m) != nil {
		t.Fatal("extending a stored narrowing wrote into it, so the next request inherits the last request's narrowing")
	}
	if one.At("Author", m) == nil || two.At("Author", m) == nil {
		t.Fatal("the extension did not take effect at all")
	}
	if same(t, one.At("Author", m), two.At("Author", m)) {
		t.Fatal("two requests extending the same base got each other's narrowing")
	}

	for _, rs := range []*crud.RelationScopes{one, two} {
		if rs.At("Comments", m) == nil {
			t.Fatal("the copy dropped the narrowing the base already had")
		}
	}
}

func TestRepeatedRelationScopeDeclarationsComposeByAND(t *testing.T) {
	rs := (*crud.RelationScopes)(nil).
		AtPath("Comments", crud.Eq("AuthorID", int64(7))).
		AtPath("Comments", crud.Eq("Approved", true))

	if got := commentScope(t, rs, "Comments"); got != `("author_id" = $1 AND "approved" = $2)` {
		t.Fatalf("same-path declarations rendered %s, want both narrowings", got)
	}

	ct := reflect.TypeOf(Comment{})
	byModel := (*crud.RelationScopes)(nil).
		ForModel(ct, crud.Eq("AuthorID", int64(7))).
		ForModel(ct, crud.Eq("Approved", true))
	if got := commentScope(t, byModel, "Comments"); got != `("author_id" = $1 AND "approved" = $2)` {
		t.Fatalf("same-model declarations rendered %s, want both narrowings", got)
	}
}

func TestPathAndModelRelationScopesComposeByAND(t *testing.T) {
	ct := reflect.TypeOf(Comment{})
	rs := (*crud.RelationScopes)(nil).
		ForModel(ct, crud.Eq("Approved", true)).
		AtPath("Comments", crud.Eq("AuthorID", int64(7)))

	if got := commentScope(t, rs, "Comments"); got != `("author_id" = $1 AND "approved" = $2)` {
		t.Fatalf("path scope replaced model scope: %s", got)
	}
}

func same(t *testing.T, a, b crud.Predicate) bool {
	t.Helper()
	m := articleMeta(t)
	sa, aa, _ := crud.NewSQL(crud.Postgres{}, m).Predicate(a).Done()
	sb, ab, _ := crud.NewSQL(crud.Postgres{}, m).Predicate(b).Done()
	return sa == sb && fmt.Sprint(aa) == fmt.Sprint(ab)
}
