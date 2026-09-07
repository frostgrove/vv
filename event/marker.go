package event

import "context"

// What Within leaves in the context, and the only thing that makes a later
// mismatch visible: without one, a context that has since acquired a different
// transaction cannot be told from a context that always carried this one.
//
// A marker chains on any marker already there rather than replacing it, and an
// operation reads the innermost marker whose backing is its own store's.
// Resolved innermost-first instead, an operation that calls Within over store A
// and then over store B — two backings, one context, an ordinary program —
// leaves B's marker innermost, and A's next append compares A's authority
// against a transaction that was never A's and is refused with a message that
// names neither of them. Chaining is what lets two stores' transaction contexts
// compose; keying by backing is what makes the chain resolvable.
//
// It is not forgeable: the type, the key and the constructor are unexported, so
// the only party that mints one is Within, from an authority a store answered.
type marker struct {
	authority Authority
	outer     *marker
}

type markerKey struct{}

func withMarker(ctx context.Context, authority Authority) context.Context {
	outer, _ := ctx.Value(markerKey{}).(*marker)
	return context.WithValue(ctx, markerKey{}, &marker{authority: authority, outer: outer})
}

func markerFor(ctx context.Context, backing Backing) (Authority, bool) {
	held, _ := ctx.Value(markerKey{}).(*marker)
	for ; held != nil; held = held.outer {
		if held.authority.backing.Equal(backing) {
			return held.authority, true
		}
	}
	return Authority{}, false
}
