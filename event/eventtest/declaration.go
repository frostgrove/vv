package eventtest

import (
	"bytes"

	"github.com/frostgrove/vv/event"
)

// The aggregate the suite declares for itself. Its state carries a scalar, a
// string and the bytes a codec decoded by aliasing the payload it was given,
// because the last is the only one of the three through which a store's hand-off
// of a page is observable from a folded state.
type ledger struct {
	Owner   string
	Balance int64
	Notes   [][]byte
}

type accountID struct {
	Tenant string
	Number string
}

type opened struct{ Owner string }

type credited struct{ Amount int64 }

// Encoded as itself and decoded by aliasing the payload, which is what a compact
// binary format does and what event.JSON happens not to do.
type noted struct{ Bytes []byte }

type rawCodec struct{}

func (rawCodec) Encode(value noted) ([]byte, error) { return value.Bytes, nil }

func (rawCodec) Decode(payload []byte) (noted, error) { return noted{Bytes: payload}, nil }

func (rawCodec) CanEncode() error { return nil }

type declaration struct {
	family    string
	aggregate *event.Aggregate[ledger, accountID]
	opened    *event.Fact[ledger, accountID, opened]
	credited  *event.Fact[ledger, accountID, credited]
	noted     *event.Fact[ledger, accountID, noted]
}

func declare(family string) (declaration, error) { return declareWith(family, keyOf) }

// The same aggregate under a mapper that renders what it was given. Compose
// escapes what the kernel's text rule refuses, so a control character can only
// reach a key through a mapper an application wrote itself — which is the one
// the door has to refuse.
func declareRaw(family string) (declaration, error) {
	return declareWith(family, func(id accountID) event.Key { return event.Key(id.Tenant + "/" + id.Number) })
}

func declareWith(family string, key func(accountID) event.Key) (declaration, error) {
	aggregate, err := event.TryDefine[ledger](family, key)
	if err != nil {
		return declaration{}, err
	}
	held := declaration{family: family, aggregate: aggregate}
	if held.opened, err = event.TryDeclare(aggregate, "eventtest.opened", event.From(event.JSON[opened]()), openLedger); err != nil {
		return declaration{}, err
	}
	if held.credited, err = event.TryDeclare(aggregate, "eventtest.credited", event.From(event.JSON[credited]()), creditLedger); err != nil {
		return declaration{}, err
	}
	if held.noted, err = event.TryDeclare(aggregate, "eventtest.noted", event.From(event.Codec[noted](rawCodec{})), noteLedger); err != nil {
		return declaration{}, err
	}
	return held, nil
}

func keyOf(id accountID) event.Key { return event.Compose(id.Tenant, id.Number) }

func openLedger(state ledger, fact opened) ledger {
	state.Owner = fact.Owner
	return state
}

func creditLedger(state ledger, fact credited) ledger {
	state.Balance += fact.Amount
	return state
}

func noteLedger(state ledger, fact noted) ledger {
	state.Notes = append(state.Notes, fact.Bytes)
	return state
}

func sameLedger(first, second ledger) bool {
	if first.Owner != second.Owner || first.Balance != second.Balance || len(first.Notes) != len(second.Notes) {
		return false
	}
	for index := range first.Notes {
		if !bytes.Equal(first.Notes[index], second.Notes[index]) {
			return false
		}
	}
	return true
}
