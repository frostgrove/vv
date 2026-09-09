//go:build integration

package eventpg

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/projection"
)

const (
	ordersFamily   = "eventpg.router.orders"
	paymentsFamily = "eventpg.router.payments"
	shippingFamily = "eventpg.router.shipping"
)

// The three aggregates a live log carries in UC-130, and the facts each
// declares. orders.placed retains two revisions, so a payload written at the
// older one reaches its applier through a declared upcaster rather than through
// a cast — which is what makes the applier's argument the declaration's own
// value and not the bytes.
type routed struct {
	placed     *event.Fact[held, string, held]
	paid       *event.Fact[held, string, held]
	authorised *event.Fact[held, string, held]
	captured   *event.Fact[held, string, held]
}

func declareRouted(t *testing.T) routed {
	t.Helper()
	orders, _ := declareHolding(t, ordersFamily)
	payments, _ := declareHolding(t, paymentsFamily)
	upcasting := event.Then(event.From(event.Codec[held](heldCodec{})), event.Codec[held](heldCodec{}),
		func(carried held) (held, error) { return held{Bytes: append([]byte("v1:"), carried.Bytes...)}, nil })
	placed, err := event.TryDeclare(orders, ordersFamily+".placed", upcasting, func(_ held, carried held) held { return carried })
	if err != nil {
		t.Fatalf("the two-revision fact this case reads an older payload through was refused: %v", err)
	}
	if _, err := event.TryDeclare(orders, ordersFamily+".cancelled", event.From(event.Codec[held](heldCodec{})), func(_ held, carried held) held { return carried }); err != nil {
		t.Fatalf("the fifth fact, the one no route claims, was refused: %v", err)
	}
	return routed{
		placed:     placed,
		paid:       oneRevision(t, orders, ordersFamily+".paid"),
		authorised: oneRevision(t, payments, paymentsFamily+".authorised"),
		captured:   oneRevision(t, payments, paymentsFamily+".captured"),
	}
}

func oneRevision(t *testing.T, aggregate *event.Aggregate[held, string], name string) *event.Fact[held, string, held] {
	t.Helper()
	fact, err := event.TryDeclare(aggregate, name, event.From(event.Codec[held](heldCodec{})),
		func(_ held, carried held) held { return carried })
	if err != nil {
		t.Fatalf("the fact %q was refused: %v", name, err)
	}
	return fact
}

func (this *destination) applies(ctx context.Context, value held, _ event.Envelope) error {
	_, err := this.on(ctx).ExecContext(ctx, "INSERT INTO "+this.table+" (payload) VALUES ($1)", string(value.Bytes))
	return err
}

// The log this case routes: two aggregates the router covers, a fifth type it
// ignores by name, and a third aggregate it was never told about.
func writeRoutedLog(t *testing.T, held *projectionCase) {
	t.Helper()
	held.writeAt(t, aStream(ordersFamily, "o-1"), ordersFamily+".placed", 1, "o1-placed")
	held.write(t, aStream(ordersFamily, "o-1"), ordersFamily+".paid", "o1-paid")
	held.writeAt(t, aStream(ordersFamily, "o-2"), ordersFamily+".placed", 2, "o2-placed")
	held.write(t, aStream(ordersFamily, "o-2"), ordersFamily+".cancelled", "o2-cancelled")
	held.write(t, aStream(paymentsFamily, "p-1"), paymentsFamily+".authorised", "p1-auth")
	held.write(t, aStream(paymentsFamily, "p-1"), paymentsFamily+".captured", "p1-capt")
	held.write(t, aStream(shippingFamily, "s-1"), shippingFamily+".dispatched", "s1-disp")
}

// UC-130 over a live log, and the pair is what makes INV-081 discriminating
// rather than either universal refusal or universal silence: the SAME missing
// registration halts inside a family the router covers and is skipped outside
// every one of them. A router that refused both, or skipped both, fails one half
// each.
func TestARouterRoutesDeclaresAndRefusesTheUnclaimed(t *testing.T) {
	facts := declareRouted(t)

	t.Run("the control: the complete router applies the four, ignores the fifth and counts the foreign", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.destination(t, "read_model")
		writeRoutedLog(t, held)

		router := projection.NewRouter(projection.SkipForeign)
		projection.On(router, facts.placed, rows.applies)
		projection.On(router, facts.paid, rows.applies)
		projection.On(router, facts.authorised, rows.applies)
		projection.On(router, facts.captured, rows.applies)
		projection.Ignore(router, ordersFamily, ordersFamily+".cancelled")

		held.run(t, held.spec(t, "orders", router)).following(t, "the complete router drained the log")

		if got := rows.rows(t); !slices.Equal(got, []string{"v1:o1-placed", "o1-paid", "o2-placed", "p1-auth", "p1-capt"}) {
			t.Fatalf("the read model holds %v where the four appliers write five rows and the older revision reaches its applier through the declared upcaster", got)
		}
		if router.Skipped() != 1 {
			t.Fatalf("the router skipped %d envelopes where one aggregate of the log is of a family it routes nothing of", router.Skipped())
		}
		row, found := held.row(t, "orders")
		if !found || row.quarantined != 0 {
			t.Fatalf("the checkpoint row is %+v (found %v) where a complete router quarantines nothing", row, found)
		}
	})

	t.Run("one On removed inside a covered family halts at the first such event", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.destination(t, "read_model")
		writeRoutedLog(t, held)

		router := projection.NewRouter(projection.SkipForeign)
		projection.On(router, facts.placed, rows.applies)
		projection.On(router, facts.authorised, rows.applies)
		projection.On(router, facts.captured, rows.applies)
		projection.Ignore(router, ordersFamily, ordersFamily+".cancelled")

		stopped := held.run(t, held.spec(t, "orders", router))
		cause := stopped.halted(t, "the projection halted on the type nothing routes")

		if !errors.Is(cause, projection.ErrUnrouted) {
			t.Fatalf("the projection halted with %v where a type of a covered family that no route claims is ErrUnrouted", cause)
		}
		if !strings.Contains(cause.Error(), ordersFamily+".paid") {
			t.Fatalf("the halt reports %q and does not name the registration that is missing", cause)
		}
		if got := rows.rows(t); !slices.Equal(got, []string{"v1:o1-placed"}) {
			t.Fatalf("the read model holds %v where the projection halts on the second envelope of the log rather than draining past it", got)
		}
		if row, found := held.row(t, "orders"); found {
			t.Fatalf("the checkpoint moved to advance %d over a page the router refused, so the events of that type are skipped for good", row.advance)
		}
	})

	t.Run("the same registration missing outside every covered family is skipped, counted and invisible", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.destination(t, "read_model")
		writeRoutedLog(t, held)

		router := projection.NewRouter(projection.SkipForeign)
		projection.On(router, facts.placed, rows.applies)
		projection.On(router, facts.paid, rows.applies)
		projection.Ignore(router, ordersFamily, ordersFamily+".cancelled")

		held.run(t, held.spec(t, "orders", router)).following(t, "the router with no payments route drained the log")

		if got := rows.rows(t); !slices.Equal(got, []string{"v1:o1-placed", "o1-paid", "o2-placed"}) {
			t.Fatalf("the read model holds %v where the two families this router routes nothing of are skipped rather than refused", got)
		}
		if router.Skipped() != 3 {
			t.Fatalf("the router skipped %d envelopes where the payments aggregate holds two and the shipping one holds one", router.Skipped())
		}
		if row, found := held.row(t, "orders"); !found || row.highest == 0 {
			t.Fatalf("the checkpoint row is %+v (found %v) where a router that skipped what it was never told about advances over it", row, found)
		}
	})

	t.Run("RefuseForeign halts on the third aggregate's first event with the same sentinel", func(t *testing.T) {
		held := newProjectionCase(t, 8, 0)
		rows := held.destination(t, "read_model")
		writeRoutedLog(t, held)

		router := projection.NewRouter(projection.RefuseForeign)
		projection.On(router, facts.placed, rows.applies)
		projection.On(router, facts.paid, rows.applies)
		projection.On(router, facts.authorised, rows.applies)
		projection.On(router, facts.captured, rows.applies)
		projection.Ignore(router, ordersFamily, ordersFamily+".cancelled")

		cause := held.run(t, held.spec(t, "orders", router)).halted(t, "the projection halted on the foreign aggregate")
		if !errors.Is(cause, projection.ErrUnrouted) {
			t.Fatalf("a refused foreign type halted with %v where a consumer reads one refusal rather than two", cause)
		}
		if !strings.Contains(cause.Error(), shippingFamily+".dispatched") {
			t.Fatalf("the halt reports %q and does not name the type it refused", cause)
		}
		if router.Skipped() != 0 {
			t.Fatalf("a router built to refuse foreign types skipped %d of them", router.Skipped())
		}
	})
}
