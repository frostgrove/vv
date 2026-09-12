package receipt_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/receipt"
)

// A refusal names the rule that was broken and never the data that broke it. The
// three needles are the three this package is handed: the operation key, which is
// a request identity; the stream key, which is a customer's identifier; and the
// payload text, which is the fingerprint's preimage and the fact itself.
func TestNoRefusalOfAReceiptNamesAKeyOrAFingerprintsPreimage(t *testing.T) {
	const (
		carried = "req-yankee-needle-77"
		reason  = "zulu-needle-payload"
		id      = "A-needle-17"
		other   = "B-needle-42"
	)
	needles := []string{carried, reason, id, other}

	stand := newStand(t)
	ctx := context.Background()
	spent := keyed(t, carried)
	abandoned := keyed(t, "req-xray-abandoned")
	fact := credited{Minor: 41, Reason: reason}

	elsewhere := newStand(t)
	crossed := newLedger(elsewhere.store)
	noTransactions := counting(stand.store)
	noTransactions.transactions = event.Unsupported
	autocommitting := newLedger(stand.store)
	autocommitting.transaction = func(context.Context) (event.Authority, error) { return event.Authority{}, nil }
	silent := newLedger(stand.store)
	silent.onClaim = func(context.Context, receipt.Receipt) (receipt.Receipt, bool, error) {
		return receipt.Receipt{}, false, nil
	}
	stranger := newLedger(stand.store)
	stranger.onClaim = func(_ context.Context, held receipt.Receipt) (receipt.Receipt, bool, error) {
		held.Key = keyed(t, "req-somebody-else")
		return held, true, nil
	}
	optimistic := newLedger(stand.store)
	optimistic.onFind = func(_ context.Context, key receipt.Key) (receipt.Receipt, bool, error) {
		return receipt.Receipt{
			Key: key, Fingerprint: printOf(t, 3),
			Stream:     event.Stream{Family: "receipts.account", Key: event.Key(id)},
			First:      1,
			Last:       1,
			Complete:   true,
			RecordedAt: time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC),
		}, true, nil
	}
	optimistic.onHorizon = func(context.Context) (time.Time, error) {
		return time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC), nil
	}

	var spentCommit event.Commit
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, id, fact)
		taken, err := receipt.Claim(inner, stand.claimSpec(t, spent, at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		spentCommit = written
		return taken.Complete(inner, written)
	})
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, other, fact)
		_, err := receipt.Claim(inner, stand.claimSpec(t, abandoned, at, print))
		return err
	})

	type refusal struct {
		what    string
		refused error
		want    error
	}
	var refusals []refusal
	add := func(what string, refused, want error) {
		refusals = append(refusals, refusal{what: what, refused: refused, want: want})
	}

	for _, one := range []struct{ what, raw string }{
		{"a key that is empty", ""},
		{"a key over the bound", strings.Repeat(carried, 64)},
		{"a key that is not valid UTF-8", carried + "\xff\xfe"},
		{"a key carrying a control character", carried + "\x00"},
		{"a key carrying a bracket", "[" + carried + "]"},
	} {
		_, err := receipt.NewKey(one.raw)
		add(one.what, err, receipt.ErrSpec)
	}
	_, zeroed := receipt.NewFingerprint([32]byte{})
	add("a fingerprint of thirty-two zero bytes", zeroed, receipt.ErrSpec)
	for _, one := range []struct{ what, raw string }{
		{"a fingerprint naming no algorithm", carried},
		{"a fingerprint that is not hexadecimal", "sha256:" + reason},
		{"a fingerprint of the wrong length", "sha256:abcd"},
	} {
		_, err := receipt.ParseFingerprint(one.raw)
		add(one.what, err, receipt.ErrSpec)
	}

	changing := func(what string, want error, change func(spec *receipt.ClaimSpec)) func(inner context.Context) {
		return func(inner context.Context) {
			spec := claiming(t, stand, inner, "req-"+strings.ReplaceAll(what, " ", "-"), id, fact)
			change(&spec)
			_, err := receipt.Claim(inner, spec)
			add(what, err, want)
		}
	}

	_ = stand.unit(ctx, func(inner context.Context) error {
		for _, gather := range []func(context.Context){
			changing("a claim naming no ledger", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Ledger = nil }),
			changing("a claim naming no store", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Store = nil }),
			changing("a claim carrying the zero key", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Key = receipt.Key{} }),
			changing("a claim carrying the zero fingerprint", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Fingerprint = receipt.Fingerprint{} }),
			changing("a claim carrying the zero stream", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Stream = event.Stream{} }),
			changing("a claim against a store with no transactions", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Store = noTransactions }),
			changing("a claim whose ledger is on a second pool", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Ledger = crossed }),
			changing("a claim whose ledger names nobody's transaction", receipt.ErrSpec, func(spec *receipt.ClaimSpec) { spec.Ledger = autocommitting }),
			changing("a claim whose ledger answers no row", receipt.ErrLedger, func(spec *receipt.ClaimSpec) { spec.Ledger = silent }),
			changing("a claim whose ledger answers another key's row", receipt.ErrLedger, func(spec *receipt.ClaimSpec) { spec.Ledger = stranger }),
			changing("a claim on a key nobody resolved", receipt.ErrIncomplete, func(spec *receipt.ClaimSpec) { spec.Key = abandoned }),
			changing("a claim on a key spent elsewhere", receipt.ErrCollision, func(spec *receipt.ClaimSpec) {
				spec.Key = spent
				_, _, print := stand.decide(t, inner, id, credited{Minor: 1, Reason: reason + "-different"})
				spec.Fingerprint = print
			}),
		} {
			gather(inner)
		}

		add("a claim outside any transaction", claimingOutside(t, stand, ctx, id, fact), receipt.ErrSpec)

		at, changes, print := stand.decide(t, inner, id, fact)
		taken, err := receipt.Claim(inner, stand.claimSpec(t, keyed(t, "req-completions"), at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		second, err := stand.store.Begin(ctx)
		if err != nil {
			return err
		}
		add("a completion on the zero Held", receipt.Held{}.Complete(inner, written), receipt.ErrSpec)
		add("a completion outside any transaction", taken.Complete(ctx, written), receipt.ErrSpec)
		add("a completion on a second transaction", taken.Complete(eventmemory.WithTransaction(ctx, second), written), receipt.ErrSpec)
		if err := second.Rollback(ctx); err != nil {
			return err
		}
		add("a completion with a commit of another transaction", taken.Complete(inner, spentCommit), receipt.ErrSpec)

		otherAt, otherChanges, _ := stand.decide(t, inner, other, fact)
		_, otherCommit, err := stand.repo.Append(inner, otherAt, otherChanges...)
		if err != nil {
			return err
		}
		add("a completion with another aggregate's commit", taken.Complete(inner, otherCommit), receipt.ErrSpec)
		if admitted := taken.Complete(inner, written); admitted != nil {
			t.Fatalf("the claim's own completion answered %v, so the rows beside it are a blanket refusal", admitted)
		}
		add("a second completion of one claim", taken.Complete(inner, written), receipt.ErrSpec)

		repeat, err := receipt.Claim(inner, stand.claimSpec(t, keyed(t, "req-completions"), at, print))
		if err != nil {
			return err
		}
		add("a completion of a repeat", repeat.Complete(inner, written), receipt.ErrSpec)

		_, refused := receipt.Once(inner, stand.claimSpec(t, keyed(t, "req-once"), at, print), nil)
		add("a Once with no work", refused, receipt.ErrSpec)

		_, inside := receipt.Resolve(inner, receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store, Key: spent})
		add("a resolve inside the writing transaction", inside, receipt.ErrSpec)
		return errDomainRefused
	})

	for _, one := range []struct {
		what string
		spec receipt.ResolveSpec
	}{
		{"a resolve naming no ledger", receipt.ResolveSpec{Store: stand.store, Key: spent}},
		{"a resolve naming no store", receipt.ResolveSpec{Ledger: stand.ledger, Key: spent}},
		{"a resolve carrying the zero key", receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store}},
	} {
		_, err := receipt.Resolve(ctx, one.spec)
		add(one.what, err, receipt.ErrSpec)
	}
	_, optimistically := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: optimistic, Store: stand.store, Key: spent})
	add("a resolve over a ledger whose horizon is after a row it holds", optimistically, receipt.ErrLedger)

	if len(refusals) < 30 {
		t.Fatalf("%d refusals were driven, and this package publishes more than that — a table that shrank is one that stopped asking", len(refusals))
	}
	for _, one := range refusals {
		if !errors.Is(one.refused, one.want) {
			t.Fatalf("%s answered %v where the rule it broke is %v", one.what, one.refused, one.want)
		}
		matched := 0
		for _, sentinel := range []error{receipt.ErrSpec, receipt.ErrCollision, receipt.ErrIncomplete, receipt.ErrLedger} {
			if errors.Is(one.refused, sentinel) {
				matched++
			}
		}
		if matched != 1 {
			t.Fatalf("%s answers %d of the published sentinels, and a refusal belongs to exactly one class", one.what, matched)
		}
		for _, named := range needles {
			if strings.Contains(one.refused.Error(), named) {
				t.Fatalf("%s rendered %q into %q, and a refusal names the rule that was broken and never the data that broke it", one.what, named, one.refused)
			}
		}
	}
}

func claimingOutside(t *testing.T, stand *stand, ctx context.Context, id string, fact credited) error {
	t.Helper()
	at, _, print := stand.decide(t, ctx, id, fact)
	_, err := receipt.Claim(ctx, stand.claimSpec(t, keyed(t, "req-no-unit-at-all"), at, print))
	return err
}
