package receipt_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/receipt"
)

// The same rows behind a transaction resolved through another handle, which is
// the composition where only the ledger's unit is bound to the context.
type ledgerOn struct {
	receipt.Ledger
	store event.Store
}

func (this ledgerOn) Transaction(ctx context.Context) (event.Authority, error) {
	return this.store.Transaction(ctx)
}

func TestAResolveInsideTheWritingTransactionIsRefused(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-mike-two")

	elsewhere := newStand(t)
	ledgersOwn, err := elsewhere.store.Begin(ctx)
	if err != nil {
		t.Fatalf("opening the ledger's own transaction answered %v", err)
	}
	defer func() { _ = ledgersOwn.Rollback(ctx) }()
	shadow := ledgerOn{Ledger: stand.ledger, store: elsewhere.store}

	resolving := func(t *testing.T, what string, inner context.Context, ledger receipt.Ledger) (receipt.Resolution, error) {
		t.Helper()
		held, err := receipt.Resolve(inner, receipt.ResolveSpec{Ledger: ledger, Store: stand.store, Key: key})
		if err == nil && !held.Standing.Valid() {
			t.Fatalf("%s answered standing %v beside no error", what, held.Standing)
		}
		return held, err
	}

	var open, crossed error
	var fresh receipt.Resolution
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 31, Reason: "deposit"})
		taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		if err := taken.Complete(inner, written); err != nil {
			return err
		}
		_, open = resolving(t, "a resolve on the claiming transaction's own context", inner, stand.ledger)
		_, crossed = resolving(t, "a resolve on a context carrying the ledger's transaction alone",
			eventmemory.WithTransaction(ctx, ledgersOwn), shadow)
		held, err := resolving(t, "a resolve on a fresh context", ctx, stand.ledger)
		fresh = held
		return err
	})

	if !errors.Is(open, receipt.ErrSpec) {
		t.Fatalf("a resolve inside the unit that took the claim answered %v, and it would read its own uncommitted row and report an operation that can still roll back", open)
	}
	if !errors.Is(crossed, receipt.ErrSpec) {
		t.Fatalf("a resolve on a context carrying the ledger's transaction answered %v, and the row it would read is that transaction's own", crossed)
	}
	if fresh.Standing != receipt.Unresolved {
		t.Fatalf("a resolve on a fresh context answered %v while the writer had not committed, and an absent row proves no rollback", fresh.Standing)
	}

	t.Run("the control: the same three after the commit are ErrSpec, ErrSpec and Found", func(t *testing.T) {
		_, stillOpen := resolving(t, "a resolve on the committed transaction's context", ctx, stand.ledger)
		_, stillCrossed := resolving(t, "a resolve on a context carrying the ledger's transaction alone",
			eventmemory.WithTransaction(ctx, ledgersOwn), shadow)
		found, err := resolving(t, "a resolve on a fresh context", ctx, stand.ledger)
		if stillOpen != nil {
			t.Fatalf("a resolve on a bare context answered %v, so the first arm refused the placement of something other than the transaction", stillOpen)
		}
		if !errors.Is(stillCrossed, receipt.ErrSpec) {
			t.Fatalf("a resolve on the ledger's still-open transaction answered %v after the writer committed, so the refusal is about the row rather than about the placement", stillCrossed)
		}
		if err != nil || found.Standing != receipt.Found {
			t.Fatalf("the committed row resolved to %v with %v, so the Unresolved above was about the row rather than about the open transaction", found.Standing, err)
		}
	})
}

func TestAnAbsentRowIsUnresolvedAndAHorizonDecidesExpired(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	horizon := time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC)
	stand.ledger.setHorizon(horizon)

	complete := keyed(t, "req-november-two")
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 14, Reason: "deposit"})
		taken, err := receipt.Claim(inner, stand.claimSpec(t, complete, at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		return taken.Complete(inner, written)
	})

	abandoned := keyed(t, "req-oscar-two")
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "B-42", credited{Minor: 3, Reason: "fee"})
		_, err := receipt.Claim(inner, stand.claimSpec(t, abandoned, at, print))
		return err
	})

	swept := keyed(t, "req-papa-two")

	for _, one := range []struct {
		what   string
		key    receipt.Key
		issued time.Time
		want   receipt.Standing
	}{
		{"a row that is present and complete", complete, horizon.Add(time.Hour), receipt.Found},
		{"a row that is present with no range", abandoned, horizon.Add(time.Hour), receipt.Incomplete},
		{"an absent row the caller dates after the horizon", swept, horizon.Add(10 * time.Minute), receipt.Unresolved},
		{"an absent row the caller dates before the horizon", swept, horizon.Add(-time.Hour), receipt.Expired},
		{"an absent row the caller cannot date at all", swept, time.Time{}, receipt.Unresolved},
	} {
		held, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store, Key: one.key, Issued: one.issued})
		if err != nil {
			t.Fatalf("%s answered %v, and a zero Issued in particular is a caller that holds a retried key and not the instant it was minted", one.what, err)
		}
		if held.Standing != one.want {
			t.Fatalf("%s answered %v where the arithmetic says %v", one.what, held.Standing, one.want)
		}
		if !held.Horizon.Equal(horizon) {
			t.Fatalf("%s answered horizon %v where the ledger publishes %v, and every standing carries it so a caller that could not date its key can make the comparison itself", one.what, held.Horizon, horizon)
		}
	}

	t.Run("the control: the horizon is what decides, and it decides between two non-conclusions", func(t *testing.T) {
		before, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store, Key: swept, Issued: horizon.Add(-time.Nanosecond)})
		if err != nil || before.Standing != receipt.Expired {
			t.Fatalf("an instant one nanosecond before the horizon answered %v with %v", before.Standing, err)
		}
		after, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store, Key: swept, Issued: horizon})
		if err != nil || after.Standing != receipt.Unresolved {
			t.Fatalf("an instant at the horizon answered %v with %v, and the boundary is at or after it rather than past it", after.Standing, err)
		}
		for _, one := range []receipt.Standing{before.Standing, after.Standing} {
			if one == receipt.Found {
				t.Fatal("a reading of the two clocks answered Found for a row nobody found, which is the one answer skew must not be able to produce")
			}
		}
		stand.ledger.setHorizon(time.Time{})
		everything, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store, Key: swept, Issued: horizon.Add(-time.Hour)})
		if err != nil || everything.Standing != receipt.Unresolved {
			t.Fatalf("a ledger claiming to hold everything for ever answered %v with %v, and nothing it holds may be reported expired", everything.Standing, err)
		}
	})
}

func TestAResolveReadsNoEventAndOffersNothingToAppendWith(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-quebec-two")

	var commit event.Commit
	var print receipt.Fingerprint
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, digested := stand.decide(t, inner, "A-17", credited{Minor: 88, Reason: "deposit"}, credited{Minor: 12, Reason: "interest"})
		taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, digested))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		commit, print = written, digested
		return taken.Complete(inner, written)
	})

	counted := counting(stand.store)
	held, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: stand.ledger, Store: counted, Key: key})
	if err != nil {
		t.Fatalf("resolving a committed receipt answered %v", err)
	}
	if held.Standing != receipt.Found {
		t.Fatalf("a present and complete row resolved to %v", held.Standing)
	}
	if held.Receipt.Key != key || !held.Receipt.Fingerprint.Equal(print) {
		t.Fatalf("the resolution carries another operation's key or fingerprint")
	}
	if held.Receipt.Stream != commit.Stream() || held.Receipt.First != commit.First() || held.Receipt.Last != commit.Last() {
		t.Fatalf("the resolution carries %v %d..%d where the operation wrote %v %d..%d",
			held.Receipt.Stream, held.Receipt.First, held.Receipt.Last, commit.Stream(), commit.First(), commit.Last())
	}

	t.Run("the control: the event store is asked one question and no other", func(t *testing.T) {
		asked := counted.asks()
		if !slices.Equal(asked, []string{"Transaction"}) {
			t.Fatalf("the event store was asked %v, and the one question a resolve has for it is whether a transaction of its is bound — it reads no stream and folds nothing", asked)
		}
	})
}
