package crud_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/shardit-io/vv/crud"
)

// MergeRelationScopes is where a repository's permanent narrowing meets the one
// a single request carries — the per-tenant filter an access-control decorator
// adds, which cannot be baked into the blueprint because it depends on who is
// asking. Two things about that meeting are load-bearing and neither is visible
// from a call site: the two narrowings compose, and the request's copy does not
// stay behind.

// commentScope renders the narrowing that applies to a hop landing on Comments.
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

// Where both sides narrow the same relation the two are ANDed. Replacing one
// with the other would hand the request whichever narrowing was declared last —
// and one of the two is the repository's own, which nobody asked to have lifted.
func TestMergingTwoNarrowingsOfOneRelationKeepsBoth(t *testing.T) {
	permanent := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("Approved", true))
	perRequest := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("AuthorID", int64(7)))

	got := commentScope(t, crud.MergeRelationScopes(permanent, perRequest), "Comments")
	if want := `("approved" = $1 AND "author_id" = $2)`; got != want {
		t.Fatalf("the merged narrowing is %s\nwant %s — one of the two declarations was dropped", got, want)
	}

	// The same for a declaration made by model rather than by path, which is
	// the form that survives a self-relation at any depth.
	ct := reflect.TypeOf(Comment{})
	byModel := crud.MergeRelationScopes(
		(*crud.RelationScopes)(nil).ForModel(ct, crud.Eq("Approved", true)),
		(*crud.RelationScopes)(nil).ForModel(ct, crud.Eq("AuthorID", int64(7))),
	)
	if got := commentScope(t, byModel, "Comments"); got != `("approved" = $1 AND "author_id" = $2)` {
		t.Fatalf("merging two model declarations gave %s, want both of them", got)
	}
}

// The merge builds its own maps. A merge that wrote into the repository's
// blueprint would leave one caller's narrowing on it, and every later request —
// another tenant's — would then be answered through a filter nobody declared for
// it. Nothing at the call site would show it, because the first request was
// answered correctly.
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

	// The control: the merge did compose the two, so the two assertions above
	// are about isolation and not about a merge that quietly did nothing.
	if got := commentScope(t, merged, "Comments"); got == `"approved" = $1` {
		t.Fatal("the merge returned the left side unchanged, so nothing above is being tested")
	}
}

// Nothing declared on one side hands back the other untouched, which is what
// makes the merge free on every request that carries no scope of its own.
// The first two legs pin the shared-value optimisation rather than an answer a
// caller can observe: merging with nothing hands back the same pointer instead
// of a fresh equivalent value. A failure there is a cost change, not a bug — the
// third leg is the one about behaviour.
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

// The chained builders do not write into the value they were called on.
//
// `(*RelationScopes)(nil).AtPath(…).AtPath(…)` reads as a builder, and it used to
// mutate its receiver. On this path that is not a style question: a policy's
// RelationScopes function runs per request, so a consumer who kept the result of
// one call and extended it on the next would be writing into a value another
// in-flight request is reading — and what they would be corrupting is a
// narrowing, which is the thing that decides whose rows come back.
func TestTheRelationScopeBuildersDoNotMutateWhatTheyWereCalledOn(t *testing.T) {
	// At answers nil for a nil target, so the lookups below need a real one. Any
	// meta will do: a path narrowing is found by path, not by what it lands on.
	m := articleMeta(t)
	base := (*crud.RelationScopes)(nil).AtPath("Comments", crud.Eq("Approved", true))

	// Two requests extend the same stored base differently.
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

	// The control: what the base already carried survives into both.
	for _, rs := range []*crud.RelationScopes{one, two} {
		if rs.At("Comments", m) == nil {
			t.Fatal("the copy dropped the narrowing the base already had")
		}
	}
}

// same reports whether two predicates are the same narrowing, which is the only
// comparison a closed AST allows ([[D-003]]).
//
// SQL *and* args. A value is a bind parameter, so `Eq("TenantID", 1)` and
// `Eq("TenantID", 2)` render byte-identically — comparing the text alone would
// call two different tenants the same narrowing, which is exactly the confusion
// this test exists to catch.
func same(t *testing.T, a, b crud.Predicate) bool {
	t.Helper()
	m := articleMeta(t)
	sa, aa, _ := crud.NewSQL(crud.Postgres{}, m).Predicate(a).Done()
	sb, ab, _ := crud.NewSQL(crud.Postgres{}, m).Predicate(b).Done()
	return sa == sb && fmt.Sprint(aa) == fmt.Sprint(ab)
}
