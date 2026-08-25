package query_test

import (
	"strings"
	"testing"

	"github.com/shardit-io/vv/crud/query"
)

// A preload compiles against the related model but keeps the *root's*
// allow-lists, so every entry it matches has to be spelled the way the root
// spells it. Without the prefix the two routes to one column cost different
// permissions, in both directions: a grant on the root's own Body authorised
// Comments.Body through a preload, and a grant on Comments.Body — the spelling
// the documentation shows — authorised the root path and refused the preload.
func TestAPreloadSubFilterCostsTheSamePermissionAsTheFilterPath(t *testing.T) {
	const (
		byFilter  = `{"filter":{"comments.body":"x"},"unpaged":true}`
		byPreload = `{"preload":[{"path":"comments","filter":{"body":"x"}}],"unpaged":true}`
	)

	t.Run("a grant on the root's own column does not leak sideways", func(t *testing.T) {
		// Article.Body and Comment.Body are different columns on different
		// tables that happen to share a name.
		cfg := &query.Config{Filterable: []string{"Body"}, Preloadable: []string{"Comments"}}

		if _, _, err := tryDoc(t, byFilter, cfg); err == nil {
			t.Fatal("Comments.Body was filterable on a grant that named only the root's Body")
		}
		if _, _, err := tryDoc(t, byPreload, cfg); err == nil {
			t.Fatal("the same column, reached through a preload, was filterable on the same grant")
		}
	})

	t.Run("the subtree spelling authorises the route it looks like it authorises", func(t *testing.T) {
		cfg := &query.Config{Filterable: []string{"Comments.Body"}, Preloadable: []string{"Comments"}}

		if _, _, err := tryDoc(t, byFilter, cfg); err != nil {
			t.Fatalf("Comments.Body was refused on a grant that names it: %v", err)
		}
		if _, _, err := tryDoc(t, byPreload, cfg); err != nil {
			t.Fatalf("the preload route was refused on a grant that names the column: %v", err)
		}
	})

	t.Run("a subtree wildcard covers it too", func(t *testing.T) {
		cfg := &query.Config{Filterable: []string{"Comments.*"}, Preloadable: []string{"Comments"}}
		if _, _, err := tryDoc(t, byPreload, cfg); err != nil {
			t.Fatalf("Comments.* did not cover Comments.Body: %v", err)
		}
	})
}

// Sorting is the same axis the root sort guards, and a preload's sort used to
// skip the guard entirely: a column the config never named was sortable as long
// as the request went through a relation.
func TestAPreloadSortObeysTheSortableList(t *testing.T) {
	cfg := &query.Config{
		Sortable:    []string{"Title"},
		Preloadable: []string{"Comments"},
	}

	// The root refuses it, and so must the preload.
	if _, _, err := tryDoc(t, `{"sort":["-body"]}`, cfg); err == nil {
		t.Fatal("the root sort accepted a column the list does not name")
	}
	for _, doc := range []string{
		`{"preload":[{"path":"comments","sort":["-body"]}],"unpaged":true}`,
		`{"preload":[{"path":"comments","sort":["-approved"]}],"unpaged":true}`,
	} {
		_, _, err := tryDoc(t, doc, cfg)
		if err == nil {
			t.Fatalf("%s sorted a preload by a column no list named", doc)
		}
		if !strings.Contains(err.Error(), "not sortable") {
			t.Fatalf("error = %q, want it to say the column is not sortable", err)
		}
	}

	// And the grant that names the preloaded column lets it through.
	ok := &query.Config{Sortable: []string{"Comments.Body"}, Preloadable: []string{"Comments"}}
	if _, _, err := tryDoc(t, `{"preload":[{"path":"comments","sort":["-body"]}],"unpaged":true}`, ok); err != nil {
		t.Fatalf("a preload sort named by the list was refused: %v", err)
	}
}

// A `nulls` the root rejects with a 400 was accepted inside a preload and then
// thrown away — the client asked for an ordering, got a 200, and got a
// different ordering.
func TestAPreloadSortValidatesItsNullsPlacementLikeTheRootDoes(t *testing.T) {
	const rootDoc = `{"sort":[{"field":"title","nulls":"nope"}]}`
	const preloadDoc = `{"preload":[{"path":"comments","sort":[{"field":"body","nulls":"nope"}]}],"unpaged":true}`

	if _, _, err := tryDoc(t, rootDoc, nil); err == nil {
		t.Fatal("the root accepted a nulls placement that is neither first nor last")
	}
	_, _, err := tryDoc(t, preloadDoc, nil)
	if err == nil {
		t.Fatal("a preload accepted a nulls placement the root rejects")
	}
	if !strings.Contains(err.Error(), "nulls must be first or last") {
		t.Fatalf("error = %q, want the same refusal the root gives", err)
	}

	// The two placements the root does accept keep working inside a preload.
	for _, nulls := range []string{"first", "last"} {
		doc := `{"preload":[{"path":"comments","sort":[{"field":"body","nulls":"` + nulls + `"}]}],"unpaged":true}`
		if _, _, err := tryDoc(t, doc, nil); err != nil {
			t.Fatalf("nulls=%s was refused inside a preload: %v", nulls, err)
		}
	}
}
