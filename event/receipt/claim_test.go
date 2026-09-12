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

// A domain refusal raised after everything else in the unit has been done, which
// is how a case rolls one back without the rollback being about the receipt.
var errDomainRefused = errors.New("the domain refused this command")

func TestAClaimAnAppendAndACompletionAreOneTransaction(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-alpha")

	var held receipt.Held
	var commit event.Commit
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 250, Reason: "deposit"})
		taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
		if err != nil {
			return err
		}
		if taken.Verdict() != receipt.Recorded {
			t.Fatalf("a fresh key answered %v where no row existed for it", taken.Verdict())
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		if err := taken.Complete(inner, written); err != nil {
			return err
		}
		held, commit = taken, written
		return nil
	})

	found, taken := stand.ledger.committed(key)
	if !taken {
		t.Fatal("the unit committed and the ledger holds no row for the key it claimed")
	}
	if stand.ledger.count() != 1 {
		t.Fatalf("the ledger holds %d rows where a claim and a completion write one", stand.ledger.count())
	}
	if found.first != commit.First() || found.last != commit.Last() || !found.complete {
		t.Fatalf("the row carries %d..%d complete=%v where the append wrote %d..%d", found.first, found.last, found.complete, commit.First(), commit.Last())
	}
	if found.fingerprint != held.Receipt().Fingerprint.String() || found.streamKey != "A-17" {
		t.Fatalf("the row carries fingerprint %s of stream %s, which is not the operation that was claimed", found.fingerprint, found.streamKey)
	}
	if held.Receipt().First != commit.First() || held.Receipt().Last != commit.Last() {
		t.Fatalf("the Held answers %d..%d after its completion where the append wrote %d..%d", held.Receipt().First, held.Receipt().Last, commit.First(), commit.Last())
	}

	t.Run("the control: the same unit rolled back leaves no row at all", func(t *testing.T) {
		rolled := keyed(t, "req-bravo")
		err := stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "B-42", credited{Minor: 40, Reason: "refund"})
			taken, err := receipt.Claim(inner, stand.claimSpec(t, rolled, at, print))
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
			return errDomainRefused
		})
		if !errors.Is(err, errDomainRefused) {
			t.Fatalf("the unit answered %v where the domain refused it", err)
		}
		if _, taken := stand.ledger.committed(rolled); taken {
			t.Fatal("the unit rolled back and its row is in the ledger, so the row's existence is evidence about the claim rather than about the transaction")
		}
		if stand.ledger.count() != 1 {
			t.Fatalf("the ledger holds %d rows after a rolled-back claim, where the one committed operation wrote one", stand.ledger.count())
		}
		if version := stand.versionOf(t, ctx, "B-42"); version != 0 {
			t.Fatalf("the rolled-back stream is at version %d, so the events did not roll back with the row and this control compares two different things", version)
		}
	})
}

func TestAClaimIsRefusedUnlessTheLedgerAndTheStoreAreOneTransaction(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()

	// A second handle on what a deployment would call one database: another pool,
	// another transaction, and no sentence about atomicity that means anything.
	elsewhere := newStand(t)
	crossed := newLedger(elsewhere.store)

	noTransactions := counting(stand.store)
	noTransactions.transactions = event.Unsupported

	autocommitting := newLedger(stand.store)
	autocommitting.transaction = func(context.Context) (event.Authority, error) { return event.Authority{}, nil }

	refused := func(t *testing.T, what string, ledger *ledger, run func(inner context.Context) error) {
		t.Helper()
		before := ledger.claims.Load()
		err := stand.unit(ctx, run)
		if !errors.Is(err, receipt.ErrSpec) {
			t.Fatalf("%s answered %v where a claim that is not one transaction with its append is ErrSpec", what, err)
		}
		if after := ledger.claims.Load(); after != before {
			t.Fatalf("%s reached the ledger %d times, and the refusal is at the door before anything is written", what, after-before)
		}
	}

	refused(t, "a ledger on a second pool", crossed, func(inner context.Context) error {
		other, err := elsewhere.store.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = other.Rollback(ctx) }()
		both := eventmemory.WithTransaction(inner, other)
		at, _, print := stand.decide(t, inner, "A-17", credited{Minor: 5, Reason: "fee"})
		spec := stand.claimSpec(t, keyed(t, "req-two-pools"), at, print)
		spec.Ledger = crossed
		_, err = receipt.Claim(both, spec)
		return err
	})

	refused(t, "a claim outside any transaction", stand.ledger, func(context.Context) error {
		at, _, print := stand.decide(t, ctx, "A-17", credited{Minor: 5, Reason: "fee"})
		_, err := receipt.Claim(ctx, stand.claimSpec(t, keyed(t, "req-no-unit"), at, print))
		return err
	})

	refused(t, "a claim against a store that states no transactions", stand.ledger, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-17", credited{Minor: 5, Reason: "fee"})
		spec := stand.claimSpec(t, keyed(t, "req-no-binding"), at, print)
		spec.Store = noTransactions
		_, err := receipt.Claim(inner, spec)
		return err
	})

	refused(t, "a ledger whose transaction is nobody's", autocommitting, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-17", credited{Minor: 5, Reason: "fee"})
		spec := stand.claimSpec(t, keyed(t, "req-autocommit"), at, print)
		spec.Ledger = autocommitting
		_, err := receipt.Claim(inner, spec)
		return err
	})

	t.Run("the control: one handle for both is admitted", func(t *testing.T) {
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-17", credited{Minor: 5, Reason: "fee"})
			taken, err := receipt.Claim(inner, stand.claimSpec(t, keyed(t, "req-one-pool"), at, print))
			if err != nil {
				return err
			}
			if taken.Verdict() != receipt.Recorded {
				t.Fatalf("the admitted claim answered %v where it took the key", taken.Verdict())
			}
			return nil
		})
		if stand.ledger.count() != 1 {
			t.Fatalf("the ledger holds %d rows where four refusals wrote none and one acceptance wrote one", stand.ledger.count())
		}
	})
}

func TestACompletionThatIsNotTheClaimsIsRefusedFiveWays(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-charlie")

	var first event.Commit
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 60, Reason: "deposit"})
		taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		first = written

		outside := taken.Complete(ctx, written)

		second, err := stand.store.Begin(ctx)
		if err != nil {
			return err
		}
		elsewhere := taken.Complete(eventmemory.WithTransaction(ctx, second), written)
		if err := second.Rollback(ctx); err != nil {
			return err
		}

		otherAt, otherChanges, _ := stand.decide(t, inner, "B-42", credited{Minor: 9, Reason: "refund"})
		_, otherCommit, err := stand.repo.Append(inner, otherAt, otherChanges...)
		if err != nil {
			return err
		}
		crossStream := taken.Complete(inner, otherCommit)

		if admitted := taken.Complete(inner, written); admitted != nil {
			t.Fatalf("the claim's own transaction, its own commit, once, on a Recorded verdict answered %v — so the four refusals beside it are a blanket one", admitted)
		}
		twice := taken.Complete(inner, written)

		for _, one := range []struct {
			what    string
			refused error
		}{
			{"a completion on a context carrying no transaction", outside},
			{"a completion on a second transaction of the same store", elsewhere},
			{"a completion with another aggregate's commit", crossStream},
			{"a second completion of one claim", twice},
		} {
			if !errors.Is(one.refused, receipt.ErrSpec) {
				t.Fatalf("%s answered %v where a completion that would undo what the claim proved is ErrSpec", one.what, one.refused)
			}
		}
		return nil
	})

	held, taken := stand.ledger.committed(key)
	if !taken || held.first != first.First() || held.last != first.Last() || !held.complete {
		t.Fatalf("the row carries %d..%d complete=%v where the one admitted completion wrote %d..%d", held.first, held.last, held.complete, first.First(), first.Last())
	}
	if wrote := stand.ledger.completes.Load(); wrote != 1 {
		t.Fatalf("the ledger was asked to complete %d times where one completion was admitted and four were refused before it", wrote)
	}

	t.Run("a completion on a verdict that is not Recorded", func(t *testing.T) {
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-17", credited{Minor: 60, Reason: "deposit"})
			repeat, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
			if err != nil {
				return err
			}
			if repeat.Verdict() != receipt.Repeated {
				t.Fatalf("the second presentation of a complete row answered %v", repeat.Verdict())
			}
			if refused := repeat.Complete(inner, first); !errors.Is(refused, receipt.ErrSpec) {
				t.Fatalf("completing a repeat answered %v, and it would overwrite the range the first attempt wrote with this one's", refused)
			}
			return nil
		})
		if wrote := stand.ledger.completes.Load(); wrote != 1 {
			t.Fatalf("the ledger was asked to complete %d times where the repeat's completion was to be refused before it", wrote)
		}
	})
}

func TestARepeatIsAnsweredOnlyFromACompleteRow(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-delta")
	fact := credited{Minor: 125, Reason: "deposit"}

	var first event.Commit
	var firstAt event.Version
	var firstPrint receipt.Fingerprint
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", fact)
		taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		first, firstAt, firstPrint = written, at.Version(), print
		return taken.Complete(inner, written)
	})

	// The only Given a retry in a new process can have: a fresh load, which
	// answers the version the first attempt left, and a fresh decision of the same
	// facts.
	moved := stand.versionOf(t, ctx, "A-17")
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, _, print := stand.decide(t, inner, "A-17", fact)
		if at.Version() == firstAt {
			t.Fatalf("the retry loaded version %d, which is the one the first attempt was decided at — the case measures nothing", at.Version())
		}
		if !print.Equal(firstPrint) {
			t.Fatal("two attempts at one operation digested differently across a moved version, which is the identity the retry cannot reproduce")
		}
		repeat, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
		if err != nil {
			return err
		}
		if repeat.Verdict() != receipt.Repeated {
			t.Fatalf("the retry answered %v where a complete row compares equal", repeat.Verdict())
		}
		if repeat.Receipt().First != first.First() || repeat.Receipt().Last != first.Last() {
			t.Fatalf("the retry was answered %d..%d where the first attempt wrote %d..%d", repeat.Receipt().First, repeat.Receipt().Last, first.First(), first.Last())
		}
		return nil
	})
	if after := stand.versionOf(t, ctx, "A-17"); after != moved {
		t.Fatalf("the stream moved from %d to %d across a repeat, and a repeat appends nothing", moved, after)
	}

	t.Run("a row that exists and is not complete answers no verdict at all", func(t *testing.T) {
		abandoned := keyed(t, "req-echo")
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "C-99", credited{Minor: 1, Reason: "fee"})
			_, err := receipt.Claim(inner, stand.claimSpec(t, abandoned, at, print))
			return err
		})
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "C-99", credited{Minor: 1, Reason: "fee"})
			retry, err := receipt.Claim(inner, stand.claimSpec(t, abandoned, at, print))
			if !errors.Is(err, receipt.ErrIncomplete) {
				t.Fatalf("a retry of a claim nobody resolved answered %v, and whether its events reached the log is not a question its row answers", err)
			}
			if retry.Verdict().Valid() {
				t.Fatalf("the refused retry carries verdict %v, and a state nothing may be concluded from is a refusal and not an answer", retry.Verdict())
			}
			if errors.Is(err, receipt.ErrCollision) {
				t.Fatal("an unresolved row was reported as a collision, which tells a caller its key was spent when nothing may be concluded at all")
			}
			return nil
		})
	})

	t.Run("the control: the same replay with the row deleted answers Recorded and appends", func(t *testing.T) {
		stand.ledger.forget(key)
		before := stand.versionOf(t, ctx, "A-17")
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "A-17", fact)
			taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
			if err != nil {
				return err
			}
			if taken.Verdict() != receipt.Recorded {
				t.Fatalf("the replay answered %v with no row in the ledger, so Repeated above was a guess rather than the row's answer", taken.Verdict())
			}
			_, written, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			return taken.Complete(inner, written)
		})
		if after := stand.versionOf(t, ctx, "A-17"); after != before+1 {
			t.Fatalf("the stream moved from %d to %d where a Recorded claim appends its decision", before, after)
		}
	})
}

func TestACollisionTravelsAsAnErrorAndNotOnlyAsAVerdict(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-foxtrot")
	decided := []credited{{Minor: 10, Reason: "first"}, {Minor: 20, Reason: "second"}}

	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", decided...)
		taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
		if err != nil {
			return err
		}
		_, written, err := stand.repo.Append(inner, at, changes...)
		if err != nil {
			return err
		}
		return taken.Complete(inner, written)
	})
	settled := stand.versionOf(t, ctx, "A-17")

	// The worst code that compiles: a caller that checks the error channel and
	// nothing else. It must refuse rather than spend a stranger's key.
	spend := func(t *testing.T, id string, facts ...credited) error {
		t.Helper()
		return stand.unit(ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, id, facts...)
			held, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
			if err != nil {
				if held.Verdict() == receipt.Collided && !errors.Is(err, receipt.ErrCollision) {
					t.Fatalf("a collided claim answered %v, which is not the sentinel a caller branches on", err)
				}
				return err
			}
			_, _, err = stand.repo.Append(inner, at, changes...)
			return err
		})
	}

	for _, one := range []struct {
		what  string
		id    string
		facts []credited
	}{
		{"a batch differing in one byte of one payload", "A-17", []credited{{Minor: 10, Reason: "first"}, {Minor: 21, Reason: "second"}}},
		{"the identical records decided for another stream", "B-42", decided},
		{"the identical records in another order", "A-17", []credited{decided[1], decided[0]}},
	} {
		if err := spend(t, one.id, one.facts...); !errors.Is(err, receipt.ErrCollision) {
			t.Fatalf("%s answered %v where the key was spent on another operation", one.what, err)
		}
		if version := stand.versionOf(t, ctx, one.id); one.id == "A-17" && version != settled {
			t.Fatalf("%s moved A-17 from %d to %d, so the refusal did not reach the caller before its append", one.what, settled, version)
		}
		if version := stand.versionOf(t, ctx, "B-42"); version != 0 {
			t.Fatalf("%s appended to B-42, which is the second copy the refusal exists to prevent", one.what)
		}
	}

	t.Run("the control: the identical records at another expected version do NOT collide", func(t *testing.T) {
		stand.inUnit(t, ctx, func(inner context.Context) error {
			_, at, err := stand.repo.Load(inner, "A-17")
			if err != nil {
				return err
			}
			if at.Version() == settled-event.Version(len(decided)) {
				t.Fatal("the retry is at the version the first attempt was decided at, so the expected version is not being varied")
			}
			at, _, print := stand.decide(t, inner, "A-17", decided...)
			repeat, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
			if err != nil {
				t.Fatalf("the identical records at a moved version answered %v, and the expected version is deliberately not in the digest", err)
			}
			if repeat.Verdict() != receipt.Repeated {
				t.Fatalf("the identical records at a moved version answered %v where they are one operation retried", repeat.Verdict())
			}
			return nil
		})
	})

	t.Run("the control: the same shape on a fresh key appends", func(t *testing.T) {
		fresh := keyed(t, "req-golf")
		before := stand.versionOf(t, ctx, "A-17")
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 33, Reason: "another"})
			held, err := receipt.Claim(inner, stand.claimSpec(t, fresh, at, print))
			if err != nil {
				return err
			}
			_, written, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			return held.Complete(inner, written)
		})
		if after := stand.versionOf(t, ctx, "A-17"); after != before+1 {
			t.Fatalf("the same caller shape on a fresh key moved A-17 from %d to %d, so the refusals above are not about the key", before, after)
		}
	})
}

func TestOnceRunsTheWorkOnlyOnRecordedAndCompletesIt(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()
	key := keyed(t, "req-hotel")
	fact := credited{Minor: 75, Reason: "deposit"}

	ran := 0
	run := func(t *testing.T, key receipt.Key, id string, facts ...credited) receipt.Held {
		t.Helper()
		var held receipt.Held
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, id, facts...)
			taken, err := receipt.Once(inner, stand.claimSpec(t, key, at, print), func(inner context.Context) (event.Commit, error) {
				ran++
				_, written, err := stand.repo.Append(inner, at, changes...)
				return written, err
			})
			held = taken
			return err
		})
		return held
	}

	first := run(t, key, "A-17", fact)
	if ran != 1 || first.Verdict() != receipt.Recorded {
		t.Fatalf("the first operation ran the work %d times and answered %v", ran, first.Verdict())
	}
	if first.Receipt().First == 0 || !first.Receipt().Complete {
		t.Fatalf("Once answered %d..%d complete=%v, and the completion is not optional on any path", first.Receipt().First, first.Receipt().Last, first.Receipt().Complete)
	}

	settled := stand.versionOf(t, ctx, "A-17")
	repeat := run(t, key, "A-17", fact)
	if ran != 1 {
		t.Fatalf("the retry ran the work, so a Repeated key reached the decision it must not")
	}
	if repeat.Verdict() != receipt.Repeated || repeat.Receipt().First != first.Receipt().First {
		t.Fatalf("the retry answered %v carrying %d..%d where the first attempt wrote %d..%d", repeat.Verdict(), repeat.Receipt().First, repeat.Receipt().Last, first.Receipt().First, first.Receipt().Last)
	}
	if after := stand.versionOf(t, ctx, "A-17"); after != settled {
		t.Fatalf("the retry moved the stream from %d to %d", settled, after)
	}

	t.Run("a decision that yields no changes completes with a zero range", func(t *testing.T) {
		nothing := keyed(t, "req-india")
		held := run(t, nothing, "A-17")
		if held.Verdict() != receipt.Recorded {
			t.Fatalf("a no-op under a fresh key answered %v", held.Verdict())
		}
		if held.Receipt().First != 0 || held.Receipt().Last != 0 || !held.Receipt().Complete {
			t.Fatalf("the no-op recorded %d..%d complete=%v, where a decision that wrote nothing is a finished operation with an empty range", held.Receipt().First, held.Receipt().Last, held.Receipt().Complete)
		}
		retry := run(t, nothing, "A-17")
		if retry.Verdict() != receipt.Repeated || retry.Receipt().First != 0 || retry.Receipt().Last != 0 {
			t.Fatalf("the retry of a no-op answered %v carrying %d..%d, where the true answer is done and nothing changed", retry.Verdict(), retry.Receipt().First, retry.Receipt().Last)
		}
	})

	t.Run("the control: the same caller without the completion leaves the defect row", func(t *testing.T) {
		abandoned := keyed(t, "req-juliet")
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-17")
			_, err := receipt.Claim(inner, stand.claimSpec(t, abandoned, at, print))
			return err
		})
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, _, print := stand.decide(t, inner, "A-17")
			_, err := receipt.Once(inner, stand.claimSpec(t, abandoned, at, print), func(context.Context) (event.Commit, error) {
				t.Fatal("the work ran under a key whose row nobody resolved")
				return event.Commit{}, nil
			})
			if !errors.Is(err, receipt.ErrIncomplete) {
				t.Fatalf("the retry of an abandoned claim answered %v, so completing an empty commit is not what distinguishes a finished no-op from it", err)
			}
			return nil
		})
	})
}

func TestEveryRefusalClaimHasIsReachableThroughOnce(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()

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

	spent := keyed(t, "req-kilo")
	abandoned := keyed(t, "req-lima")
	stand.inUnit(t, ctx, func(inner context.Context) error {
		at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 11, Reason: "first"})
		taken, err := receipt.Claim(inner, stand.claimSpec(t, spent, at, print))
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
		unresolved, _, other := stand.decide(t, inner, "B-42", credited{Minor: 12, Reason: "second"})
		_, err = receipt.Claim(inner, stand.claimSpec(t, abandoned, unresolved, other))
		return err
	})

	// Every row is built inside the unit it is refused in, because what four of
	// them are about is the transaction the context carries.
	for _, one := range []struct {
		what string
		want error
		spec func(t *testing.T, inner context.Context) receipt.ClaimSpec
	}{
		{"a spec naming no ledger", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-mike", "A-17")
			spec.Ledger = nil
			return spec
		}},
		{"a spec naming no store", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-november", "A-17")
			spec.Store = nil
			return spec
		}},
		{"a spec carrying the zero Key", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-oscar", "A-17")
			spec.Key = receipt.Key{}
			return spec
		}},
		{"a spec carrying the zero Fingerprint", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-papa", "A-17")
			spec.Fingerprint = receipt.Fingerprint{}
			return spec
		}},
		{"a spec carrying the zero Stream", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-quebec", "A-17")
			spec.Stream = event.Stream{}
			return spec
		}},
		{"a store that states no transactions", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-romeo", "A-17")
			spec.Store = noTransactions
			return spec
		}},
		{"a ledger on a second pool", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-sierra", "A-17")
			spec.Ledger = crossed
			return spec
		}},
		{"a ledger whose transaction is nobody's", receipt.ErrSpec, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-tango", "A-17")
			spec.Ledger = autocommitting
			return spec
		}},
		{"a ledger that answers no row at all", receipt.ErrLedger, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-uniform", "A-17")
			spec.Ledger = silent
			return spec
		}},
		{"a key whose claim nobody resolved", receipt.ErrIncomplete, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-victor", "B-42")
			spec.Key = abandoned
			return spec
		}},
		{"a key spent on another operation", receipt.ErrCollision, func(t *testing.T, inner context.Context) receipt.ClaimSpec {
			spec := claiming(t, stand, inner, "req-whiskey", "A-17", credited{Minor: 99, Reason: "another operation entirely"})
			spec.Key = spent
			return spec
		}},
	} {
		var open, combined error
		_ = stand.unit(ctx, func(inner context.Context) error {
			_, open = receipt.Claim(inner, one.spec(t, inner))
			_, combined = receipt.Once(inner, one.spec(t, inner), func(context.Context) (event.Commit, error) {
				t.Fatalf("%s ran the work, and Once runs it only where the claim took the key", one.what)
				return event.Commit{}, nil
			})
			return errDomainRefused
		})
		if !errors.Is(open, one.want) {
			t.Fatalf("%s answered %v through Claim where the rule it broke is %v", one.what, open, one.want)
		}
		if !sameRefusal(open, combined) {
			t.Fatalf("%s answered %v through Claim and %v through Once, and every refusal the open-coded door has is reachable through the one the module page gives", one.what, open, combined)
		}
	}

	t.Run("the control: a refusal reachable through one door only fails the comparison", func(t *testing.T) {
		var open, combined error
		_ = stand.unit(ctx, func(inner context.Context) error {
			spec := claiming(t, stand, inner, "req-xray", "A-17")
			_, open = receipt.Claim(inner, spec)
			_, combined = receipt.Once(inner, spec, nil)
			return errDomainRefused
		})
		if open != nil {
			t.Fatalf("the control's claim answered %v where it was to be admitted", open)
		}
		if !errors.Is(combined, receipt.ErrSpec) {
			t.Fatalf("Once with no work answered %v, and a claim with nothing to complete it is the row Resolve reports as a defect", combined)
		}
		if sameRefusal(open, combined) {
			t.Fatal("the comparison the table above rests on reports a one-door refusal as the same answer, so every row of it proves nothing")
		}
	})
}

func claiming(t *testing.T, stand *stand, ctx context.Context, key, id string, facts ...credited) receipt.ClaimSpec {
	t.Helper()
	if len(facts) == 0 {
		facts = []credited{{Minor: 11, Reason: "first"}}
	}
	at, _, print := stand.decide(t, ctx, id, facts...)
	return stand.claimSpec(t, keyed(t, key), at, print)
}

func sameRefusal(open, combined error) bool {
	if open == nil || combined == nil {
		return open == nil && combined == nil
	}
	return open.Error() == combined.Error()
}

func TestALedgerThatAnswersSomethingNoLedgerAnswersIsRefused(t *testing.T) {
	stand := newStand(t)
	ctx := context.Background()

	stranger := newLedger(stand.store)
	stranger.onClaim = func(_ context.Context, held receipt.Receipt) (receipt.Receipt, bool, error) {
		held.Key = keyed(t, "req-somebody-else")
		held.RecordedAt = stranger.clock.now()
		return held, true, nil
	}

	silent := newLedger(stand.store)
	silent.onClaim = func(context.Context, receipt.Receipt) (receipt.Receipt, bool, error) {
		return receipt.Receipt{}, false, nil
	}

	// A winner reads back its own insert, so a row that is not the one it handed in
	// is a row some other operation wrote — which is the lookup a ledger gets wrong
	// by keying its select on something other than the key it inserted.
	misread := newLedger(stand.store)
	misread.onClaim = func(_ context.Context, held receipt.Receipt) (receipt.Receipt, bool, error) {
		held.Fingerprint = printOf(t, 19)
		held.RecordedAt = misread.clock.now()
		return held, true, nil
	}

	// One ledger per arm of the fresh-row check, because a row that sets all three
	// is refused under an && as readily as under an ||. The shape that escapes a
	// narrowed check is completion beside no range at all, and it is the row this
	// package writes for an empty commit: a ledger whose won comes from a rowcount
	// while its RETURNING picked up the row already there certifies clean whenever
	// the operation that spent the key appended nothing, and a second caller is
	// handed Recorded over a key that is spent.
	completed := wonWith(stand, receipt.Receipt{Complete: true})
	opened := wonWith(stand, receipt.Receipt{First: 1})
	closed := wonWith(stand, receipt.Receipt{Last: 2})

	optimistic := newLedger(stand.store)
	optimistic.onFind = func(context.Context, receipt.Key) (receipt.Receipt, bool, error) {
		return receipt.Receipt{
			Key:         keyed(t, "req-yankee"),
			Fingerprint: printOf(t, 7),
			Stream:      event.Stream{Family: "receipts.account", Key: "A-17"},
			First:       1, Last: 1, Complete: true,
			RecordedAt: time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC),
		}, true, nil
	}
	optimistic.onHorizon = func(context.Context) (time.Time, error) {
		return time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC), nil
	}

	said := map[string]string{}
	for _, one := range []struct {
		what   string
		ledger *ledger
		names  string
	}{
		{"a ledger that reports a win beside another key's row", stranger, "another key"},
		{"a ledger that reports a repeat beside no row at all", silent, "no row at all"},
		{"a ledger that reports a win beside a fingerprint it was not handed", misread, "fingerprint or stream"},
		{"a ledger that reports a win beside a row an empty commit already completed", completed, "already complete"},
		{"a ledger that reports a win beside a row whose range carries a first version", opened, "a first version"},
		{"a ledger that reports a win beside a row whose range carries a last version", closed, "a last version"},
	} {
		err := stand.unit(ctx, func(inner context.Context) error {
			spec := claiming(t, stand, inner, "req-zulu", "A-17")
			spec.Ledger = one.ledger
			_, err := receipt.Claim(inner, spec)
			return err
		})
		if !errors.Is(err, receipt.ErrLedger) {
			t.Fatalf("%s answered %v where an answer no ledger gives is refused before it is compared", one.what, err)
		}
		// Every arm is a separate sentence because every arm is a separate defect:
		// a row for another key is a lookup written wrong, no row at all beside a
		// repeat is the single-statement claim whose one snapshot answers the loser
		// zero rows, and the last three are three ways for a row to be somebody
		// else's. A ledger author reads which one it was.
		if !strings.Contains(err.Error(), one.names) {
			t.Fatalf("%s answered %q, which does not say which of the answers it gave", one.what, err)
		}
		if earlier, told := said[err.Error()]; told {
			t.Fatalf("%s and %s were refused with the same sentence, so one arm answers for the other and either could be removed unnoticed", one.what, earlier)
		}
		said[err.Error()] = one.what
	}

	held, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: optimistic, Store: stand.store, Key: keyed(t, "req-yankee")})
	if !errors.Is(err, receipt.ErrLedger) {
		t.Fatalf("a ledger publishing a horizon later than a row it still answers for was answered %v with standing %v", err, held.Standing)
	}

	t.Run("the control: a conformant ledger passes every check", func(t *testing.T) {
		key := keyed(t, "req-alfa-two")
		stand.inUnit(t, ctx, func(inner context.Context) error {
			at, changes, print := stand.decide(t, inner, "A-17", credited{Minor: 4, Reason: "fee"})
			taken, err := receipt.Claim(inner, stand.claimSpec(t, key, at, print))
			if err != nil {
				return err
			}
			_, written, err := stand.repo.Append(inner, at, changes...)
			if err != nil {
				return err
			}
			return taken.Complete(inner, written)
		})
		found, err := receipt.Resolve(ctx, receipt.ResolveSpec{Ledger: stand.ledger, Store: stand.store, Key: key})
		if err != nil {
			t.Fatalf("the conformant ledger was refused with %v, so the three arms above refuse everything", err)
		}
		if found.Standing != receipt.Found {
			t.Fatalf("the conformant ledger's completed row resolved to %v", found.Standing)
		}
	})
}

func wonWith(stand *stand, row receipt.Receipt) *ledger {
	held := newLedger(stand.store)
	held.onClaim = func(_ context.Context, taken receipt.Receipt) (receipt.Receipt, bool, error) {
		taken.First, taken.Last, taken.Complete = row.First, row.Last, row.Complete
		taken.RecordedAt = held.clock.now()
		return taken, true, nil
	}
	return held
}

func printOf(t *testing.T, seed byte) receipt.Fingerprint {
	t.Helper()
	var digest [32]byte
	for index := range digest {
		digest[index] = seed + byte(index)
	}
	print, err := receipt.NewFingerprint(digest)
	if err != nil {
		t.Fatalf("a fingerprint over thirty-two bytes was refused: %v", err)
	}
	return print
}
