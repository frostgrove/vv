package app_test

import (
	"slices"
	"testing"

	"github.com/frostgrove/vv/app"
)

func names[H any](contributions []app.Ordered[H]) []string {
	out := make([]string, 0, len(contributions))
	for _, contribution := range contributions {
		out = append(out, contribution.Name)
	}
	return out
}

// The order a set arrives in is not the order it runs in, and the gap is where
// an authentication middleware ends up behind the handler it was meant to guard.
func TestContributionsRunInTheOrderTheyDeclared(t *testing.T) {
	got := app.Sorted([]app.Ordered[int]{
		{Name: "handler", Order: 200},
		{Name: "guard", Order: 100},
	})
	if want := []string{"guard", "handler"}; !slices.Equal(names(got), want) {
		t.Fatalf("ran %v, want %v", names(got), want)
	}
}

// Two contributions at the same order run in name order, so two runs of one
// build sequence the same way. A tie broken by arrival order turns a reordering
// somewhere else into a behaviour change here.
func TestContributionsAtTheSameOrderRunByName(t *testing.T) {
	got := app.Sorted([]app.Ordered[int]{
		{Name: "zulu", Order: 100},
		{Name: "alpha", Order: 100},
	})
	if want := []string{"alpha", "zulu"}; !slices.Equal(names(got), want) {
		t.Fatalf("ran %v, want %v", names(got), want)
	}
}

// The caller's slice usually is a collection they keep. Sorting it in place
// would reorder theirs, which is the kind of change that is invisible until the
// second reader of the same set gets a different answer.
func TestSortingDoesNotReorderTheCallersOwnSlice(t *testing.T) {
	mine := []app.Ordered[int]{{Name: "handler", Order: 200}, {Name: "guard", Order: 100}}

	app.Sorted(mine)

	if want := []string{"handler", "guard"}; !slices.Equal(names(mine), want) {
		t.Fatalf("the caller's slice came back as %v, want %v", names(mine), want)
	}
}
