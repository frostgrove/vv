package app

import (
	"cmp"
	"slices"
)

type Ordered[H any] struct {
	Name string

	Order int

	Handler H
}

func Sorted[H any](contributions []Ordered[H]) []Ordered[H] {
	out := slices.Clone(contributions)
	slices.SortStableFunc(out, func(a, b Ordered[H]) int {
		if c := cmp.Compare(a.Order, b.Order); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}
