package query_test

import (
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud/query"
)

func TestAPreloadSubFilterCostsTheSamePermissionAsTheFilterPath(t *testing.T) {
	const (
		byFilter  = `{"filter":{"comments.body":"x"}}`
		byPreload = `{"preload":[{"path":"comments","filter":{"body":"x"}}]}`
	)

	t.Run("a grant on the root's own column does not leak sideways", func(t *testing.T) {
		config := &query.Config{Filterable: []string{"Body"}, Preloadable: []string{"Comments"}}

		if _, _, err := tryDoc(t, byFilter, config); err == nil {
			t.Fatal("Comments.Body was filterable on a grant that named only the root's Body")
		}
		if _, _, err := tryDoc(t, byPreload, config); err == nil {
			t.Fatal("the same column, reached through a preload, was filterable on the same grant")
		}
	})

	t.Run("the subtree spelling authorises the route it looks like it authorises", func(t *testing.T) {
		config := &query.Config{Filterable: []string{"Comments.Body"}, Preloadable: []string{"Comments"}}

		if _, _, err := tryDoc(t, byFilter, config); err != nil {
			t.Fatalf("Comments.Body was refused on a grant that names it: %v", err)
		}
		if _, _, err := tryDoc(t, byPreload, config); err != nil {
			t.Fatalf("the preload route was refused on a grant that names the column: %v", err)
		}
	})

	t.Run("a subtree wildcard covers it too", func(t *testing.T) {
		config := &query.Config{Filterable: []string{"Comments.*"}, Preloadable: []string{"Comments"}}
		if _, _, err := tryDoc(t, byPreload, config); err != nil {
			t.Fatalf("Comments.* did not cover Comments.Body: %v", err)
		}
	})
}

func TestAPreloadSortObeysTheSortableList(t *testing.T) {
	config := &query.Config{
		Sortable:    []string{"Title"},
		Preloadable: []string{"Comments"},
	}

	if _, _, err := tryDoc(t, `{"sort":["-body"]}`, config); err == nil {
		t.Fatal("the root sort accepted a column the list does not name")
	}
	for _, doc := range []string{
		`{"preload":[{"path":"comments","sort":["-body"]}]}`,
		`{"preload":[{"path":"comments","sort":["-approved"]}]}`,
	} {
		_, _, err := tryDoc(t, doc, config)
		if err == nil {
			t.Fatalf("%s sorted a preload by a column no list named", doc)
		}
		if !strings.Contains(err.Error(), "not sortable") {
			t.Fatalf("error = %q, want it to say the column is not sortable", err)
		}
	}

	ok := &query.Config{Sortable: []string{"Comments.Body"}, Preloadable: []string{"Comments"}}
	if _, _, err := tryDoc(t, `{"preload":[{"path":"comments","sort":["-body"]}]}`, ok); err != nil {
		t.Fatalf("a preload sort named by the list was refused: %v", err)
	}
}

func TestAPreloadSortValidatesItsNullsPlacementLikeTheRootDoes(t *testing.T) {
	const rootDoc = `{"sort":[{"field":"title","nulls":"nope"}]}`
	const preloadDoc = `{"preload":[{"path":"comments","sort":[{"field":"body","nulls":"nope"}]}]}`

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

	for _, nulls := range []string{"first", "last"} {
		doc := `{"preload":[{"path":"comments","sort":[{"field":"body","nulls":"` + nulls + `"}]}]}`
		if _, _, err := tryDoc(t, doc, nil); err != nil {
			t.Fatalf("nulls=%s was refused inside a preload: %v", nulls, err)
		}
	}
}
