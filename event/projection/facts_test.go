package projection_test

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/event"
)

// Three aggregates, six facts and one upcaster, which is what a router over a
// global log meets: two families it routes, one it does not, and a type inside a
// routed family that was deliberately not declared.

type order struct {
	Items    int
	Paid     int64
	Archived bool
}

type placedV1 struct{ Item string }

type placed struct {
	Item     string
	Quantity int
}

type paid struct{ Amount int64 }

type archived struct{ Reason string }

type payment struct{ Captured int64 }

type authorised struct{ Amount int64 }

type captured struct{ Amount int64 }

type shipment struct{ Dispatched bool }

type dispatched struct{ Carrier string }

// The v2 reading of orders.placed: revision 1 is what a v1 deployment wrote and
// the upcaster is what makes those bytes the current shape.
type orders struct {
	aggregate *event.Aggregate[order, string]
	placed    *event.Fact[order, string, placed]
	paid      *event.Fact[order, string, paid]
	archived  *event.Fact[order, string, archived]
}

func declareOrders(t *testing.T) orders {
	t.Helper()
	aggregate := event.Define[order]("orders", func(id string) event.Key { return event.Key(id) })
	return orders{
		aggregate: aggregate,
		placed: event.Declare(aggregate, "orders.placed",
			event.Then(event.From(event.JSON[placedV1]()), event.JSON[placed](), func(was placedV1) (placed, error) {
				return placed{Item: was.Item, Quantity: 1}, nil
			}),
			func(state order, carried placed) order {
				state.Items += carried.Quantity
				return state
			}),
		paid: event.Declare(aggregate, "orders.paid", event.From(event.JSON[paid]()),
			func(state order, carried paid) order {
				state.Paid += carried.Amount
				return state
			}),
		archived: event.Declare(aggregate, "orders.archived", event.From(event.JSON[archived]()),
			func(state order, _ archived) order {
				state.Archived = true
				return state
			}),
	}
}

// The v1 reading of the same family, on a binding of its own: what it writes is
// revision 1 for ever, which is what makes the upcaster above run over a real
// stored payload rather than over a value a test invented.
type ordersV1 struct {
	aggregate *event.Aggregate[order, string]
	placed    *event.Fact[order, string, placedV1]
}

func declareOrdersV1(t *testing.T) ordersV1 {
	t.Helper()
	aggregate := event.Define[order]("orders", func(id string) event.Key { return event.Key(id) })
	return ordersV1{
		aggregate: aggregate,
		placed: event.Declare(aggregate, "orders.placed", event.From(event.JSON[placedV1]()),
			func(state order, _ placedV1) order {
				state.Items++
				return state
			}),
	}
}

type payments struct {
	aggregate  *event.Aggregate[payment, string]
	authorised *event.Fact[payment, string, authorised]
	captured   *event.Fact[payment, string, captured]
}

func declarePayments(t *testing.T) payments {
	t.Helper()
	aggregate := event.Define[payment]("payments", func(id string) event.Key { return event.Key(id) })
	return payments{
		aggregate: aggregate,
		authorised: event.Declare(aggregate, "payments.authorised", event.From(event.JSON[authorised]()),
			func(state payment, _ authorised) payment { return state }),
		captured: event.Declare(aggregate, "payments.captured", event.From(event.JSON[captured]()),
			func(state payment, carried captured) payment {
				state.Captured += carried.Amount
				return state
			}),
	}
}

type shipping struct {
	aggregate  *event.Aggregate[shipment, string]
	dispatched *event.Fact[shipment, string, dispatched]
}

func declareShipping(t *testing.T) shipping {
	t.Helper()
	aggregate := event.Define[shipment]("shipping", func(id string) event.Key { return event.Key(id) })
	return shipping{
		aggregate: aggregate,
		dispatched: event.Declare(aggregate, "shipping.dispatched", event.From(event.JSON[dispatched]()),
			func(state shipment, _ dispatched) shipment {
				state.Dispatched = true
				return state
			}),
	}
}

func writes[S, ID any](t *testing.T, store event.Store, aggregate *event.Aggregate[S, ID]) *event.Repo[S, ID] {
	t.Helper()
	repo, err := event.Bind(event.Open(store), aggregate)
	if err != nil {
		t.Fatalf("binding %T answered %v", aggregate, err)
	}
	return repo
}

func wrote[S, ID any](t *testing.T, repo *event.Repo[S, ID], id ID, changes ...event.Change[S]) {
	t.Helper()
	ctx := context.Background()
	_, at, err := repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("loading %v answered %v", id, err)
	}
	if _, _, err := repo.Append(ctx, at, changes...); err != nil {
		t.Fatalf("appending to %v answered %v", id, err)
	}
}
