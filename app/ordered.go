package app

import (
	"cmp"
	"slices"
)

// An Ordered is one contribution and where it goes in the sequence.
//
// It is generic over what is being ordered because the thing itself is always
// the transport's — a fiber.Handler, a gin.HandlerFunc, a func(http.Handler)
// http.Handler — and the ordering problem is not. Three copies of this struct,
// one per binding, is three places a stable sort can be written unstably.
type Ordered[H any] struct {
	// Name is what a log line and a duplicate report name it by.
	Name string
	// Order decides what runs first. A number rather than a list, so a
	// contributor can slot in between two others without editing either.
	//
	// This matters most where it is least visible. A set collected from
	// independent modules arrives in whatever order the collector walked them,
	// and "the guard runs before the handler" decided that way is a security
	// property decided by luck — one that every test mounting a single module
	// still passes.
	Order int
	// Handler is the contribution.
	Handler H
}

// Sorted answers the contributions in the order they were declared to run.
//
// Sorted by order and then by name, so two runs of the same build sequence the
// same way and a log from one is comparable with a log from the other. Ties
// broken by anything less deterministic — registration order, a map walk — turn
// a reordering somewhere else into a behaviour change here.
//
// The input is cloned. It usually comes from a collection the caller keeps, and
// sorting it in place would reorder theirs.
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
