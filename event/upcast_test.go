package event

import (
	"errors"
	"testing"
)

var errUnreadableShape = errors.New("this revision names a shape the application retired")

func TestAnUpcasterMayRefuseAndAFoldMayNot(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}

	declare := func(t *testing.T, family string, up func(creditedV1) (creditedV2, error)) *Fact[account, accountID, creditedV2] {
		t.Helper()
		aggregate := Define[account](family, accountKey)
		return Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), up), creditAccount)
	}

	t.Run("an upcaster that refuses hands the caller its own error", func(t *testing.T) {
		credited := declare(t, "accounts.refusing", func(creditedV1) (creditedV2, error) {
			return creditedV2{}, errUnreadableShape
		})
		_, err := credited.RoundTrip(creditedV1{Minor: 250}, creditedV2{Minor: 7, Reason: "now"})
		if !errors.Is(err, ErrUpcast) {
			t.Fatalf("an upcaster's refusal answered %v", err)
		}
		if !errors.Is(err, errUnreadableShape) {
			t.Fatalf("the application's own error is not reachable from %v, and the sentinel exists to carry it", err)
		}
	})

	t.Run("an upcaster that panics is the same refusal, wrapping nothing", func(t *testing.T) {
		credited := declare(t, "accounts.panicking", func(creditedV1) (creditedV2, error) {
			panic(errUnreadableShape)
		})
		_, err := credited.RoundTrip(creditedV1{Minor: 250}, creditedV2{Minor: 7, Reason: "now"})
		if !errors.Is(err, ErrUpcast) {
			t.Fatalf("an upcaster's panic answered %v rather than the refusal its error would have been", err)
		}
		if errors.Is(err, errUnreadableShape) || CauseOf(err) != nil {
			t.Fatalf("a recovered panic value travelled as an application error in %v", err)
		}
	})

	t.Run("the upcaster that answers is the control", func(t *testing.T) {
		credited := declare(t, "accounts.carrying", toCreditedV2)
		carried, err := credited.RoundTrip(creditedV1{Minor: 250}, creditedV2{Minor: 7, Reason: "now"})
		if err != nil || carried[0].Minor != 250 {
			t.Fatalf("the control chain carried %+v (%v), so the two cases above pass by refusing every upcast", carried, err)
		}
	})

	t.Run("a fold that panics is not recovered into any sentinel", func(t *testing.T) {
		ledgers := Define[ledger]("ledgers.unguarded", accountKey)
		credited := Declare(ledgers, "ledgers.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2),
			func(this ledger, event creditedV2) ledger {
				this[event.Reason] += event.Minor
				return this
			})
		change := credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _ = ledgers.Fold(acme, nil, change)
		}()
		if recovered == nil {
			t.Fatal("a fold that writes into a nil map was recovered, so an application's own bug arrives as a framework refusal a retry loop will hammer")
		}
		if failed, isError := recovered.(error); isError && errors.Is(failed, ErrUpcast) {
			t.Fatalf("a fold's panic was reported as an upcaster's refusal: %v", failed)
		}

		if _, err := ledgers.Fold(acme, ledger{}, change); err != nil {
			t.Fatalf("the same fold over a state it can write was refused (%v), so the case above passes by breaking every fold", err)
		}
	})
}
