package event

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestTheKernelsValuesAnswerTheSameFromManyGoroutines(t *testing.T) {
	ledger := mustBack(t, &connection{dsn: "the ledger database"})
	held, err := NewAuthority(ledger, &connection{dsn: "one open transaction"})
	if err != nil {
		t.Fatalf("an authority was refused: %v", err)
	}
	reported := errors.New("the connection was reset")
	envelope := Envelope{
		Stream:   Stream{Family: "accounts.account", Key: "acme/A-17"},
		Version:  3,
		Position: 91,
		Type:     "accounts.deposited",
		Revision: 2,
		Payload:  []byte(`{"amount":42}`),
	}

	answers := func() []string {
		copiedBacking, copiedAuthority, copiedEnvelope := ledger, held, envelope
		return []string{
			fmt.Sprint(copiedBacking.Equal(ledger)),
			fmt.Sprint(copiedAuthority.Same(held)),
			fmt.Sprint(copiedAuthority.Valid()),
			copiedBacking.String(),
			copiedAuthority.String(),
			copiedEnvelope.Stream.String(),
			string(Compose("acme", "A-17")),
			Conflict.String(),
			Supported.String(),
			fmt.Sprint(errors.Is(refuseAppend(Failure(Refused, crud.ErrForbidden)), ErrRefused)),
			fmt.Sprint(errors.Is(refuseAppend(Failure(Refused, crud.ErrForbidden)), crud.ErrForbidden)),
			fmt.Sprint(errors.Is(refuseRead(Failure(BadCursor, reported)), ErrCursor)),
			fmt.Sprint(CauseOf(refuseAppend(Failure(Unconfirmed, reported))) == reported),
			fmt.Sprint(len(vocabulary())),
		}
	}

	read := make([][]string, 8)
	var released sync.WaitGroup
	released.Add(1)
	var readers sync.WaitGroup
	for reader := range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			released.Wait()
			read[reader] = answers()
			for range 200 {
				if got := answers(); !slices.Equal(got, read[reader]) {
					t.Errorf("goroutine %d read %v and then %v, so a value this subsystem hands out is computed once and remembered rather than computed from constants",
						reader, read[reader], got)
					return
				}
			}
		}()
	}
	released.Done()
	readers.Wait()

	for reader, got := range read {
		if !slices.Equal(got, read[0]) {
			t.Fatalf("goroutine %d read %v where goroutine 0 read %v, so a value this subsystem hands out is not safe to share",
				reader, got, read[0])
		}
	}
}
