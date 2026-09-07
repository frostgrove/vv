package event

import (
	"bytes"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
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
			if !strings.Contains(err.Error(), "accounts.credited") {
				t.Fatalf("the crossing was refused with %q, which names no decision; every other refusal in this package says what broke and this one is the only bare sentinel", err)
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
		held := account{Balance: 7, Applied: map[string]bool{"seed": true}, Tags: []string{"acme"}}
		state, err := aggregate.Fold(accountID{}, held, correct)
		if !errors.Is(err, ErrKey) || errors.Is(err, ErrWrongStream) {
			t.Fatalf("a blank identity answered %v, and reporting it as a stream crossing puts a request-class fault in the wiring class", err)
		}
		if state.Balance != 7 || !state.Applied["seed"] || !slices.Equal(state.Tags, []string{"acme"}) {
			t.Fatalf("the refusal answered %+v rather than the state it was given; the first cause runs before any fold does, so the caller still holds a usable value and the contract says which one", state)
		}
	})

	t.Run("a change minted for an identity that renders no key answers as a key refusal", func(t *testing.T) {
		aggregate := Define[account]("accounts.blanked", func(id accountID) Key { return Compose(id.number) })
		credited := Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), creditAccount)
		blank := credited.New(accountID{tenant: "acme"}, creditedV2{Minor: 250, Reason: "deposit"})
		if !errors.Is(blank.Err(), ErrKey) {
			t.Fatalf("an identity rendering no legal key minted a change carrying %v", blank.Err())
		}
		state, err := aggregate.Fold(acme, account{Balance: 7}, blank)
		if !errors.Is(err, ErrKey) || errors.Is(err, ErrWrongStream) {
			t.Fatalf("a change that never reached a stream, folded under a legal identity, answered %v; a zero stream equals no legal one, and reporting it as a crossing tells the caller the server broke over data only the caller can correct", err)
		}
		if state.Balance != 7 {
			t.Fatalf("a refused fold advanced the state to %d", state.Balance)
		}
	})

	t.Run("a change no fact ever decided is refused rather than folded", func(t *testing.T) {
		declared := declareAccounts(t)
		state, err := declared.aggregate.Fold(acme, account{Balance: 7}, Change[account]{})
		if !errors.Is(err, ErrWrongStream) {
			t.Fatalf("the zero change folded with %v; it names no stream, carries no refusal and applies nothing, and folding it as a no-op hides a decision that was never taken", err)
		}
		if !strings.Contains(err.Error(), "no fact ever decided") {
			t.Fatalf("a change no fact minted was refused with %q, which reads as a crossing between two streams and sends the reader looking for the other one", err)
		}
		if state.Balance != 7 {
			t.Fatalf("a refused fold advanced the state to %d", state.Balance)
		}
	})

	t.Run("a rendered key over the kernel cap is refused, and one at the cap is not", func(t *testing.T) {
		aggregate := Define[account]("accounts.longkeys", func(id accountID) Key { return Key(id.number) })
		credited := Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), creditAccount)
		atTheCap := accountID{number: strings.Repeat("k", MaxKeyBytes)}
		change := credited.New(atTheCap, creditedV2{Minor: 250, Reason: "deposit"})
		if change.Err() != nil {
			t.Fatalf("an identity rendering a key at the kernel cap was refused: %v", change.Err())
		}
		if _, err := aggregate.Fold(atTheCap, account{}, change); err != nil {
			t.Fatalf("a fold under a key at the kernel cap was refused: %v", err)
		}
		over := accountID{number: strings.Repeat("k", MaxKeyBytes+1)}
		if err := credited.New(over, creditedV2{Minor: 250, Reason: "deposit"}).Err(); !errors.Is(err, ErrKey) {
			t.Fatalf("an identity rendering a key one byte over the kernel cap minted a change carrying %v, and a key is checked at every door it crosses", err)
		}
		state, err := aggregate.Fold(over, account{Balance: 7, Applied: map[string]bool{"seed": true}}, change)
		if !errors.Is(err, ErrKey) {
			t.Fatalf("a fold under a key one byte over the kernel cap answered %v", err)
		}
		if state.Balance != 7 || !state.Applied["seed"] {
			t.Fatalf("the refusal answered %+v rather than the state it was given", state)
		}
	})

	t.Run("a crossing and a carried refusal in one list answer in the order the append performs them", func(t *testing.T) {
		aggregate := Define[float64]("sensors.ordered", accountKey)
		read := Declare(aggregate, "sensors.read", From(JSON[reading]()),
			func(this float64, event reading) float64 { return this + event.Rate })
		theirs := read.New(other, reading{Rate: 2})
		unencodable := read.New(acme, reading{Rate: math.Inf(1)})
		if !errors.Is(unencodable.Err(), ErrEncode) {
			t.Fatalf("the payload its codec cannot encode minted a change carrying %v, so the list below holds one refusal and not two", unencodable.Err())
		}
		for _, list := range [][]Change[float64]{
			{theirs, unencodable},
			{unencodable, theirs},
		} {
			state, err := aggregate.Fold(acme, 1.5, list...)
			if !errors.Is(err, ErrWrongStream) {
				t.Fatalf("a list holding another instance's change beside one carrying its own refusal answered %v; the crossing is compared over the whole list before any carried refusal is read, and which of the two the caller is told decides whether it retries or corrects its request", err)
			}
			if state != 1.5 {
				t.Fatalf("a refused fold advanced the state to %v", state)
			}
		}
	})

	t.Run("recorded bytes the shipped codec cannot read are refused by the fold", func(t *testing.T) {
		declared := declareAccounts(t)
		decided := declared.credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		if decided.Err() != nil {
			t.Fatalf("the control decision carried a refusal: %v", decided.Err())
		}
		for _, stored := range []struct {
			what    string
			payload []byte
		}{
			{"an array where the declaration reads an object", []byte(`[1,2,3]`)},
			{"a field of a JSON type the declaration does not read", []byte(`{"Minor":"250"}`)},
			{"bytes that are not JSON at all", []byte("\xff\xfe")},
			{"no bytes at all", nil},
		} {
			recorded := decided
			recorded.payload = stored.payload
			state, err := declared.aggregate.Fold(acme, account{Balance: 7}, recorded)
			if !errors.Is(err, ErrPayload) {
				t.Fatalf("%s folded with %v; the shipped codec is the one every application starts with, and a decoder that drops encoding/json's error replays a truncated row, a hand-edited one or an older deploy's shape as the zero value of the reader type with no refusal at any door", stored.what, err)
			}
			if state.Balance != 7 {
				t.Fatalf("%s advanced the state to %d", stored.what, state.Balance)
			}
		}
		if _, err := declared.aggregate.Fold(acme, account{}, decided); err != nil {
			t.Fatalf("the same declaration over the bytes it wrote itself was refused (%v), so the rows above pass by refusing every payload", err)
		}
		if _, err := JSON[creditedV2]().Decode([]byte(`[1,2,3]`)); err == nil {
			t.Fatal("the shipped codec read an array into a struct and said nothing")
		}
		if read, err := JSON[creditedV2]().Decode([]byte(`{"Minor":250,"Reason":"deposit"}`)); err != nil || read.Minor != 250 {
			t.Fatalf("the shipped codec refused a payload it wrote itself: %+v %v", read, err)
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

	t.Run("the frozen array has no capacity beyond the bytes that were decided", func(t *testing.T) {
		declared := declareAccounts(t)
		encoded, err := JSON[opened]().Encode(opened{Owner: "a"})
		if err != nil {
			t.Fatalf("the shipped codec refused the payload: %v", err)
		}
		if cap(bytes.Clone(encoded)) == len(encoded) {
			t.Fatal("a clone of this encoding has no spare capacity of its own, so the assertion below holds whatever the freeze does and a different payload length is needed")
		}
		change := declared.opened.New(acme, opened{Owner: "a"})
		if cap(change.payload) != len(change.payload) {
			t.Fatalf("the frozen payload has %d bytes of capacity behind its %d, so a store or a decorator that appends to the record it was handed writes into the fact rather than into a copy",
				cap(change.payload)-len(change.payload), len(change.payload))
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

// The checkable half of §INV-004, and it is a proxy rather than the invariant: a
// fold that reads a clock or a package counter compiles and the framework cannot
// see it. What it pins is that one (state, changes) pair produces one result, so
// the two states must be constructed independently — folding one value twice
// asks §UC-068's question instead, and the third case is what keeps the two
// apart.
func TestAFoldIsPureOverTheStateItIsGiven(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}

	t.Run("one list folded from two independently constructed states produces two equal results", func(t *testing.T) {
		declared := declareAccounts(t)
		changes := []Change[account]{
			declared.opened.New(acme, opened{Owner: "acme"}),
			declared.credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"}),
			declared.credited.New(acme, creditedV2{Minor: 40, Reason: "interest"}),
		}
		first, err := declared.aggregate.Fold(acme, account{Applied: map[string]bool{}}, changes...)
		if err != nil {
			t.Fatalf("the first fold was refused: %v", err)
		}
		second, err := declared.aggregate.Fold(acme, account{Applied: map[string]bool{}}, changes...)
		if err != nil {
			t.Fatalf("the second fold was refused: %v", err)
		}
		if first.Balance != second.Balance || !maps.Equal(first.Applied, second.Applied) || !slices.Equal(first.Tags, second.Tags) {
			t.Fatalf("one list folded from two equal states produced %+v and %+v, so the result is a function of something other than the state and the changes", first, second)
		}
	})

	t.Run("a fold whose result is not a function of its two arguments fails the same proxy", func(t *testing.T) {
		var applied int64
		aggregate := Define[balance]("accounts.impure", accountKey)
		credited := Declare(aggregate, "accounts.credited", From(JSON[creditedV2]()),
			func(this balance, event creditedV2) balance {
				applied++
				return this + balance(event.Minor) + balance(applied)
			})
		change := credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		first, err := aggregate.Fold(acme, 0, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		second, err := aggregate.Fold(acme, 0, change)
		if err != nil {
			t.Fatalf("the second fold was refused: %v", err)
		}
		if first == second {
			t.Fatal("a fold that counts how often it has run answered the same twice, so the case above passes for a fold that reads a clock too and proves nothing")
		}
	})

	t.Run("a state the fold advances in place is a different question and still answers", func(t *testing.T) {
		ledgers := Define[ledger]("ledgers.pure", accountKey)
		credited := Declare(ledgers, "ledgers.credited", From(JSON[creditedV2]()),
			func(this ledger, event creditedV2) ledger {
				this[event.Reason] += event.Minor
				return this
			})
		change := credited.New(acme, creditedV2{Minor: 250, Reason: "deposit"})
		first, err := ledgers.Fold(acme, ledger{}, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		second, err := ledgers.Fold(acme, ledger{}, change)
		if err != nil {
			t.Fatalf("the second fold was refused: %v", err)
		}
		if !maps.Equal(first, second) {
			t.Fatalf("a fold that writes through the state it was handed produced %v and %v from two empty ledgers, and writing in place is what §UC-068 blesses rather than what this proxy asks about", first, second)
		}
	})
}
