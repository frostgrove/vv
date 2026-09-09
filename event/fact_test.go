package event

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type carriedBytes struct{ Body []byte }

// The codec the ownership rule is written for: Decode answers a value that
// points into the payload it was handed, which the contract permits and which
// this file pins as a case rather than a sentence.
type aliasingCodec struct{}

func (aliasingCodec) CanEncode() error { return nil }

func (aliasingCodec) Encode(value carriedBytes) ([]byte, error) { return value.Body, nil }

func (aliasingCodec) Decode(payload []byte) (carriedBytes, error) {
	return carriedBytes{Body: payload}, nil
}

func creditedRead(t *testing.T) *Fact[account, accountID, creditedV2] {
	t.Helper()
	return declareAccounts(t).credited
}

// One decoder, reached two ways. Read runs this fact's own reader chain and
// every declared upcaster, so an envelope a projection reads and one a replay
// folds cannot be read two ways; and the four history classes are raised by the
// party each sentinel names, carrying neither the key nor the payload.
func TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses(t *testing.T) {
	stream := accountsAt("acme", "A-17")
	secret := "a-payload-fragment-nobody-may-see"
	padded := func(size int) []byte {
		frame := `{"Minor":5,"Reason":"` + `"}`
		return []byte(`{"Minor":5,"Reason":"` + strings.Repeat("f", size-len(frame)) + `"}`)
	}
	oversized := padded(MaxPayloadBytes + 1)
	if len(oversized) != MaxPayloadBytes+1 {
		t.Fatalf("the oversized payload is %d bytes against a ceiling of %d, so the row below says nothing about where the bound falls", len(oversized), MaxPayloadBytes)
	}

	t.Run("a declared upcaster runs", func(t *testing.T) {
		credited := creditedRead(t)
		read, err := credited.Read(Envelope{Stream: stream, Version: 1, Position: 1,
			Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)})
		if err != nil {
			t.Fatalf("a stored revision 1 was refused with %v", err)
		}
		if read.Minor != 5 || read.Reason != "migrated" {
			t.Fatalf("revision 1 read back as %+v, so the declared upcaster between it and the current type did not run", read)
		}
		if credited.Family() != "accounts.account" {
			t.Fatalf("the fact names the family %q where its aggregate is declared on another", credited.Family())
		}
	})

	for _, one := range []struct {
		what     string
		envelope Envelope
		want     error
		unspoken string
	}{
		{"an envelope of another family",
			Envelope{Stream: Stream{Family: "ledgers.ledger", Key: stream.Key}, Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)},
			ErrUnknownType, string(stream.Key)},
		{"an envelope of another declared type",
			Envelope{Stream: stream, Type: "accounts.opened", Revision: 1, Payload: []byte(`{"Owner":"acme"}`)},
			ErrUnknownType, ""},
		{"a type name no declaration could have produced",
			Envelope{Stream: stream, Type: "accounts.\ncredited", Revision: 1, Payload: []byte(`{"Minor":5}`)},
			ErrUnknownType, "accounts.\ncredited"},
		{"a type name longer than a declared identifier may be",
			Envelope{Stream: stream, Type: strings.Repeat("f", MaxNameBytes+1), Revision: 1, Payload: []byte(`{"Minor":5}`)},
			ErrUnknownType, strings.Repeat("f", MaxNameBytes+1)},
		{"a revision below the first this fact retains",
			Envelope{Stream: stream, Type: "accounts.credited", Revision: 0, Payload: []byte(`{"Minor":5}`)},
			ErrRevision, ""},
		{"a revision above the last this fact retains",
			Envelope{Stream: stream, Type: "accounts.credited", Revision: 3, Payload: []byte(`{"Minor":5}`)},
			ErrRevision, ""},
		{"bytes this revision's codec cannot read",
			Envelope{Stream: stream, Type: "accounts.credited", Revision: 2, Payload: []byte(`{"Minor":"` + secret + `"}`)},
			ErrPayload, secret},
		{"a payload over the kernel's own ceiling, which this revision's codec reads perfectly well",
			Envelope{Stream: stream, Type: "accounts.credited", Revision: 2, Payload: oversized},
			ErrPayload, ""},
	} {
		credited := creditedRead(t)
		read, err := credited.Read(one.envelope)
		if !errors.Is(err, one.want) {
			t.Fatalf("%s answered %v where the party that refuses it raises %v", one.what, err, one.want)
		}
		if read != (creditedV2{}) {
			t.Fatalf("%s answered %+v beside its refusal, so a caller that ignores the error decides on a value nothing recorded", one.what, read)
		}
		if escaped := strconv.Quote(one.unspoken); one.unspoken != "" &&
			(strings.Contains(err.Error(), one.unspoken) || strings.Contains(err.Error(), escaped[1:len(escaped)-1])) {
			t.Fatalf("%s rendered the store's own data in %q, and a refusal names the rule that was broken and never the value that broke it", one.what, err)
		}
	}

	t.Run("a payload of exactly the ceiling is read", func(t *testing.T) {
		credited := creditedRead(t)
		read, err := credited.Read(Envelope{Stream: stream, Type: "accounts.credited", Revision: 2, Payload: padded(MaxPayloadBytes)})
		if err != nil || read.Minor != 5 {
			t.Fatalf("a payload of exactly %d bytes answered %v, and the ceiling is the last payload a fact may read rather than the first it may not", MaxPayloadBytes, err)
		}
	})

	t.Run("an upcaster that refuses hands the caller its own error", func(t *testing.T) {
		aggregate := Define[account]("accounts.refusing", accountKey)
		credited := Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), func(creditedV1) (creditedV2, error) {
				return creditedV2{}, errUnreadableShape
			}), creditAccount)

		_, err := credited.Read(Envelope{Stream: Stream{Family: "accounts.refusing", Key: stream.Key},
			Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)})
		if !errors.Is(err, ErrUpcast) || !errors.Is(err, errUnreadableShape) {
			t.Fatalf("an upcaster's refusal reached the caller as %v, and the application's own error is what says which stored value it was about", err)
		}
	})

	t.Run("the replay path reads the same envelope the same way", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		recorded := Record{Type: "accounts.credited", Revision: 1, Payload: []byte(`{"Minor":5}`)}
		store.history(stream, recorded)

		folded, _, err := repo.Load(context.Background(), accountID{tenant: "acme", number: "A-17"})
		if err != nil {
			t.Fatalf("the control's replay was refused with %v", err)
		}
		read, err := declared.credited.Read(Envelope{Stream: stream, Version: 1, Position: 1,
			Type: recorded.Type, Revision: recorded.Revision, Payload: recorded.Payload})
		if err != nil {
			t.Fatalf("the control's Read was refused with %v", err)
		}
		if creditAccount(account{}, read).Balance != folded.Balance {
			t.Fatalf("Read folded to a balance of %d where the replay path folded to %d, so a projection and a load are two decoders of one envelope",
				creditAccount(account{}, read).Balance, folded.Balance)
		}
	})
}

// The value Read answers may point into the envelope's payload, exactly as a
// replay's may. Nothing is cloned on the way in, so whoever owns the page owns
// what came out of it — which is why a sender that re-reads what it handed over
// pays for a copy of its own.
func TestTheValueFactReadAnswersMayAliasThePayload(t *testing.T) {
	aggregate := Define[account]("blobs.blob", accountKey)
	carried := Declare(aggregate, "blobs.carried", From(Codec[carriedBytes](aliasingCodec{})),
		func(this account, _ carriedBytes) account { return this })

	payload := []byte("the recorded bytes")
	read, err := carried.Read(Envelope{Stream: Stream{Family: "blobs.blob", Key: Compose("acme")},
		Type: "blobs.carried", Revision: 1, Payload: payload})
	if err != nil {
		t.Fatalf("the aliasing codec's own payload was refused with %v", err)
	}
	if !bytes.Equal(read.Body, payload) {
		t.Fatalf("the value read back carries %d bytes where the payload holds %d", len(read.Body), len(payload))
	}

	payload[0] = 'T'
	if read.Body[0] != 'T' {
		t.Fatal("Read copied the payload on the way in, so the ownership rule the page contract rests on is not the one the kernel implements and a sender that re-reads its own page is paying for a copy twice")
	}

	credited := creditedRead(t)
	json := []byte(`{"Minor":5,"Reason":"one"}`)
	decoded, err := credited.Read(Envelope{Stream: accountsAt("acme", "A-17"),
		Type: "accounts.credited", Revision: 2, Payload: json})
	if err != nil {
		t.Fatalf("the control's payload was refused with %v", err)
	}
	json[0] = 'x'
	if decoded.Reason != "one" {
		t.Fatal("a codec that copies answered a value that moved with the payload, so the case above says nothing about the codec it was written for")
	}
}
