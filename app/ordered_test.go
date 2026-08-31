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

func TestContributionsRunInTheOrderTheyDeclared(t *testing.T) {
	got := app.Sorted([]app.Ordered[int]{
		{Name: "handler", Order: 200},
		{Name: "guard", Order: 100},
	})
	if want := []string{"guard", "handler"}; !slices.Equal(names(got), want) {
		t.Fatalf("ran %v, want %v", names(got), want)
	}
}

func TestContributionsAtTheSameOrderRunByName(t *testing.T) {
	got := app.Sorted([]app.Ordered[int]{
		{Name: "zulu", Order: 100},
		{Name: "alpha", Order: 100},
	})
	if want := []string{"alpha", "zulu"}; !slices.Equal(names(got), want) {
		t.Fatalf("ran %v, want %v", names(got), want)
	}
}

func TestSortingDoesNotReorderTheCallersOwnSlice(t *testing.T) {
	mine := []app.Ordered[int]{{Name: "handler", Order: 200}, {Name: "guard", Order: 100}}

	app.Sorted(mine)

	if want := []string{"handler", "guard"}; !slices.Equal(names(mine), want) {
		t.Fatalf("the caller's slice came back as %v, want %v", names(mine), want)
	}
}
