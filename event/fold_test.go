package event

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

type note struct{ Body []byte }

type carrier struct{ Body []byte }

type reading struct{ Rate float64 }

type ledger map[string]int64

type balance int64

// A codec that decodes by aliasing the payload it was handed, which is what a
// compact binary format is and is an ordinary codec rather than a broken one.
// It hands its own input back on encode too, so nothing but the kernel's freeze
// stands between a caller's payload and the recorded fact.
type aliasCodec struct{}

func (aliasCodec) Encode(value note) ([]byte, error)   { return value.Body, nil }
func (aliasCodec) Decode(payload []byte) (note, error) { return note{Body: payload}, nil }
func (aliasCodec) CanEncode() error                    { return nil }

type refusingDecode struct{ Codec[creditedV2] }

func (this refusingDecode) Decode(payload []byte) (creditedV2, error) {
	value, err := this.Codec.Decode(payload)
	if err == nil && value.Minor < 0 {
		return creditedV2{}, errors.New("a negative amount was never recorded")
	}
	return value, err
}

func carryNote(this carrier, event note) carrier {
	this.Body = event.Body
	return this
}

func TestFoldRefusesAnotherInstance(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	other := accountID{tenant: "acme", number: "B-42"}

	t.Run("the list folded with the identity it was minted for succeeds", func(t *testing.T) {
		declared := declareAccounts(t)
		state, err := declared.aggregate.Fold(acme, account{},
			declared.opened.New(acme, opened{Owner: "acme"}),
			declared.credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"}))
		if err != nil {
			t.Fatalf("a correct fold was refused, so every refusal below passes by refusing everything: %v", err)
		}
		if state.Balance != 250 || !state.Applied["deposit"] {
			t.Fatalf("the fold produced %+v rather than the state its two facts describe", state)
		}
	})

	t.Run("a change of another instance is refused, per change", func(t *testing.T) {
		declared := declareAccounts(t)
		mine := declared.credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		theirs := declared.credited.New(other, creditedV2{Minor: 400, Reason: "deposit"})
		for _, list := range [][]Change[account]{
			{theirs},
			{mine, theirs},
			{theirs, mine},
		} {
			state, err := declared.aggregate.Fold(acme, account{Balance: 7}, list...)
			if !errors.Is(err, ErrWrongStream) {
				t.Fatalf("a list carrying another instance's change folded with %v, so one account's decision advances another's state", err)
			}
			if state.Balance != 7 {
				t.Fatalf("a refused fold advanced the state to %d, and the first three causes run before any fold", state.Balance)
			}
		}
	})

	t.Run("the zero identity is a key refusal and not a stream crossing", func(t *testing.T) {
		aggregate := Define[account]("accounts.numbered", func(id accountID) Key { return Compose(id.number) })
		credited := Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), creditAccount)
		correct := credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		if _, err := aggregate.Fold(acme, account{}, correct); err != nil {
			t.Fatalf("the control fold was refused: %v", err)
		}
		_, err := aggregate.Fold(accountID{}, account{}, correct)
		if !errors.Is(err, ErrKey) || errors.Is(err, ErrWrongStream) {
			t.Fatalf("a blank identity answered %v, and reporting it as a stream crossing puts a request-class fault in the wiring class", err)
		}
	})

	t.Run("a change carrying its own refusal surfaces it rather than folding nothing", func(t *testing.T) {
		aggregate := Define[float64]("sensors.sensor", func(id accountID) Key { return Compose(id.tenant, id.number) })
		read := Declare(aggregate, "sensors.read", From(JSON[reading]()),
			func(this float64, event reading) float64 { return this + event.Rate })
		unencodable := read.New(acme, reading{Rate: math.Inf(1)})
		if !errors.Is(unencodable.Err(), ErrEncode) {
			t.Fatalf("a payload its codec cannot encode was minted without a carried refusal: %v", unencodable.Err())
		}
		state, err := aggregate.Fold(acme, 1.5, read.New(acme, reading{Rate: 2}), unencodable)
		if !errors.Is(err, ErrEncode) {
			t.Fatalf("a fold of a change that never encoded answered %v, so a decided fact was applied as a no-op", err)
		}
		if state != 1.5 {
			t.Fatalf("a refused fold advanced the state to %v, and a carried refusal is checked over the whole list first", state)
		}
	})

	t.Run("a payload over the kernel ceiling is refused where no store is known", func(t *testing.T) {
		aggregate := Define[carrier]("notes.large", accountKey)
		written := Declare(aggregate, "notes.written", From[note](aliasCodec{}), carryNote)
		atTheCeiling := written.New(acme, note{Body: make([]byte, MaxPayloadBytes)})
		if atTheCeiling.Err() != nil {
			t.Fatalf("a payload at the kernel ceiling was refused: %v", atTheCeiling.Err())
		}
		over := written.New(acme, note{Body: make([]byte, MaxPayloadBytes+1)})
		if !errors.Is(over.Err(), ErrTooLarge) {
			t.Fatalf("a payload one byte over the kernel ceiling was minted with %v, and no store is known at a decision", over.Err())
		}
		if _, err := aggregate.Fold(acme, carrier{}, over); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("the fold of an over-large change answered %v", err)
		}
	})

	t.Run("a decode that fails mid list returns the state as of the last change applied", func(t *testing.T) {
		aggregate := Define[account]("accounts.halfread", accountKey)
		credited := Declare(aggregate, "accounts.credited",
			From[creditedV2](refusingDecode{JSON[creditedV2]()}), creditAccount)
		state, err := aggregate.Fold(acme, account{},
			credited.New(acme, creditedV2{Minor: 10, Reason: "first"}),
			credited.New(acme, creditedV2{Minor: -1, Reason: "second"}),
			credited.New(acme, creditedV2{Minor: 5, Reason: "third"}))
		if !errors.Is(err, ErrPayload) {
			t.Fatalf("a payload the declaration cannot read answered %v", err)
		}
		if state.Balance != 10 || state.Applied["second"] || state.Applied["third"] {
			t.Fatalf("the fold returned %+v, and cause four returns the state as of the last change it applied", state)
		}
	})

	t.Run("two aggregates over one state type are told apart by the family", func(t *testing.T) {
		accounts := declareAccounts(t)
		savings := Define[account]("savings.account", accountKey)
		saved := Declare(savings, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), creditAccount)
		crossing := saved.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		if _, err := savings.Fold(acme, account{}, crossing); err != nil {
			t.Fatalf("the control fold through the aggregate that minted the change was refused: %v", err)
		}
		if _, err := accounts.aggregate.Fold(acme, account{}, crossing); !errors.Is(err, ErrWrongStream) {
			t.Fatalf("one state type made two aggregates one Go type and the crossing folded with %v", err)
		}
	})
}

func TestAReferenceKindStateFoldsWithoutAliasing(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}

	t.Run("a fold that aliases a reference kind out of a change cannot disturb a later fold", func(t *testing.T) {
		aggregate := Define[carrier]("notes.note", accountKey)
		written := Declare(aggregate, "notes.written", From[note](aliasCodec{}), carryNote)
		change := written.New(acme, note{Body: []byte("one")})

		first, err := aggregate.Fold(acme, carrier{}, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		if string(first.Body) != "one" {
			t.Fatalf("the fold published %q rather than the decided payload", first.Body)
		}
		first.Body[0] = 'X'

		second, err := aggregate.Fold(acme, carrier{}, change)
		if err != nil {
			t.Fatalf("the second fold was refused: %v", err)
		}
		if string(second.Body) != "one" {
			t.Fatalf("the second fold published %q, so the change's frozen bytes are what the first fold handed the application and a mutation of a state rewrote a decided fact", second.Body)
		}
	})

	t.Run("a scalar-only payload behaves identically", func(t *testing.T) {
		declared := declareAccounts(t)
		change := declared.credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		first, err := declared.aggregate.Fold(acme, account{}, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		first.Balance = 0
		second, err := declared.aggregate.Fold(acme, account{}, change)
		if err != nil || second.Balance != 250 {
			t.Fatalf("a scalar-only payload folded to %+v (%v), so the property is an accident of the reference kind", second, err)
		}
	})

	t.Run("a state that is a reference kind is advanced in place, and a scalar one is not", func(t *testing.T) {
		ledgers := Define[ledger]("ledgers.ledger", accountKey)
		credited := Declare(ledgers, "ledgers.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2),
			func(this ledger, event creditedV2) ledger {
				if this == nil {
					this = ledger{}
				}
				this[event.Reason] += event.Minor
				return this
			})
		held := ledger{}
		change := credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		advanced, err := ledgers.Fold(acme, held, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		if held["deposit"] != 250 || advanced["deposit"] != 250 {
			t.Fatalf("the argument holds %d and the result %d; Fold is the loop the caller would have written and a state type that is a map has no other kind of fold",
				held["deposit"], advanced["deposit"])
		}
		if _, err := ledgers.Fold(acme, advanced, change); err != nil {
			t.Fatalf("the second fold was refused: %v", err)
		}
		if held["deposit"] != 500 {
			t.Fatalf("folding one list twice left the argument at %d, so the reload that disagrees with a doubly applied state is not the authority the contract says it is", held["deposit"])
		}

		balances := Define[balance]("balances.balance", accountKey)
		scalar := Declare(balances, "balances.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2),
			func(this balance, event creditedV2) balance { return this + balance(event.Minor) })
		var untouched balance
		result, err := balances.Fold(acme, untouched, scalar.New(acme, creditedV2{Minor: 250, Reason: "deposit"}))
		if err != nil {
			t.Fatalf("the scalar fold was refused: %v", err)
		}
		if untouched != 0 || result != 250 {
			t.Fatalf("a scalar state read %d after a fold that returned %d, so the case would pass for every state type", untouched, result)
		}
	})
}

func TestAChangeRetainsNoApplicationValue(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	aggregate := Define[carrier]("notes.note", accountKey)
	written := Declare(aggregate, "notes.written", From[note](aliasCodec{}), carryNote)

	t.Run("mutating the payload after deciding cannot change what was recorded", func(t *testing.T) {
		payload := note{Body: []byte("one")}
		change := written.New(acme, payload)
		if change.Err() != nil {
			t.Fatalf("the change carried a refusal: %v", change.Err())
		}
		if change.Stream() != (Stream{Family: "notes.note", Key: "acme/A-17"}) {
			t.Fatalf("the change names the stream %v rather than the one its identity renders", change.Stream())
		}
		payload.Body[0] = 'X'
		folded, err := aggregate.Fold(acme, carrier{}, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		if string(folded.Body) != "one" {
			t.Fatalf("the recorded fact reads %q after the caller mutated its own payload, so the fact recorded is not the fact decided", folded.Body)
		}
	})

	t.Run("a scalar-only payload behaves identically", func(t *testing.T) {
		declared := declareAccounts(t)
		payload := creditedV2{Minor: 250, Reason: "deposit"}
		change := declared.credited.New(acme, payload)
		payload.Minor = 9000
		folded, err := declared.aggregate.Fold(acme, account{}, change)
		if err != nil || folded.Balance != 250 {
			t.Fatalf("a scalar payload folded to %+v (%v), so the property is an accident of the reference kind", folded, err)
		}
	})

	t.Run("two folds of one change produce two independent values", func(t *testing.T) {
		change := written.New(acme, note{Body: []byte("one")})
		first, err := aggregate.Fold(acme, carrier{}, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		second, err := aggregate.Fold(acme, carrier{}, change)
		if err != nil {
			t.Fatalf("the second fold was refused: %v", err)
		}
		if !bytes.Equal(first.Body, second.Body) {
			t.Fatalf("two folds of one change produced %q and %q", first.Body, second.Body)
		}
		if &first.Body[0] == &second.Body[0] {
			t.Fatal("two folds of one change published one array, so the change carries a decoded value the application can reach and mutate")
		}
	})
}
