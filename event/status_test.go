package event

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/frostgrove/vv/port/porthttp"
)

// The classes are only worth their split if both halves reach a transport. A
// caller's oversized payload is the caller's to correct and renders a client
// status; a stored payload the declaration cannot read is not, and renders the
// one a transport gives what it cannot explain. The second assertion is vacuous
// alone — the status table's default is 500, so "not a client error" is true of
// every sentinel it does not name — so this drives every member of the request
// class beside the two controls.
func TestARequestClassRefusalRendersAClientStatusAndAHistoryClassOneDoesNot(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	store := newRecordingStore(t)
	store.limits.MaxPayload = 8
	aggregate := Define[account]("accounts.account", accountKey)
	opening := Declare(aggregate, "accounts.opened", From(JSON[opened]()), openAccount)
	measuring := Declare(aggregate, "accounts.measured", From(JSON[reading]()),
		func(this account, _ reading) account { return this })
	repo, err := Bind(Open(store), aggregate)
	if err != nil {
		t.Fatalf("the fixture store was refused at Bind: %v", err)
	}

	stream := Stream{Family: "accounts.account", Key: Compose("acme", "A-17")}
	store.streams[stream] = []Envelope{{
		Stream:   stream,
		Version:  1,
		Position: 1,
		Type:     "accounts.opened",
		Revision: 1,
		Payload:  make([]byte, store.limits.MaxPayload*8),
	}}

	_, at, err := repo.Load(ctx, accountID{tenant: "acme", number: "A-99"})
	if err != nil {
		t.Fatalf("the token the request-class rows append through could not be minted: %v", err)
	}
	over := []Change[account]{}
	for range store.limits.MaxBatch + 1 {
		over = append(over, opening.New(accountID{tenant: "acme", number: "A-99"}, opened{Owner: "acme"}))
	}

	for _, one := range []struct {
		what     string
		sentinel error
		status   int
		refused  func() error
	}{
		{"an identity that renders no legal key", ErrKey, http.StatusBadRequest, func() error {
			_, _, err := repo.Load(ctx, accountID{tenant: strings.Repeat("x", store.limits.MaxKey+1)})
			return err
		}},
		{"a payload the declared codec cannot encode", ErrEncode, http.StatusBadRequest, func() error {
			_, _, err := repo.Append(ctx, at, measuring.New(accountID{tenant: "acme", number: "A-99"}, reading{Rate: math.NaN()}))
			return err
		}},
		{"a sample that cannot prove what a round trip claims", ErrSample, http.StatusBadRequest, func() error {
			_, err := opening.RoundTrip()
			return err
		}},
		{"a batch over the bound the store published", ErrTooLarge, http.StatusRequestEntityTooLarge, func() error {
			_, _, err := repo.Append(ctx, at, over...)
			return err
		}},
		{"a recorded payload this declaration cannot read", ErrPayload, http.StatusInternalServerError, func() error {
			_, _, err := repo.Load(ctx, acme)
			return err
		}},
		{"a cursor this store did not mint", ErrCursor, http.StatusInternalServerError, func() error {
			reader, err := Read(store, "not one this store minted")
			if err != nil {
				return err
			}
			_, err = reader.Next(ctx)
			return err
		}},
	} {
		err := one.refused()
		if !errors.Is(err, one.sentinel) {
			t.Fatalf("%s answered %v rather than %v, so the row below is about another refusal", one.what, err, one.sentinel)
		}
		if got := porthttp.Status(err); got != one.status {
			t.Fatalf("%s renders %d where its class is %d; a class a transport cannot read is a class that decides nothing",
				one.what, got, one.status)
		}
	}
}
