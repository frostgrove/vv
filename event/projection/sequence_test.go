package projection_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

func envelopeOf(family, key string, position event.Position) event.Envelope {
	return event.Envelope{
		Stream:   event.Stream{Family: family, Key: event.Key(key)},
		Version:  event.Version(position),
		Position: position,
		Type:     family + ".placed",
		Revision: 1,
		Payload:  []byte(`{"placed":true}`),
	}
}

// Total, pure and stable are three contract obligations the type cannot carry, so
// what is measured here is what a caller can see: every sequencer answers a key
// for every envelope, answers the same key twice, and names itself. The last arm
// is the correction the default rests on — a stream renders its family and drops
// its key, so a default spelled as that rendering would put every stream of one
// family in one sequence.
func TestEverySequencerIsTotalPureAndStable(t *testing.T) {
	envelopes := []event.Envelope{
		envelopeOf("orders", "a", 1),
		envelopeOf("orders", "b", 2),
		envelopeOf("orders", "a", 3),
		envelopeOf("shipments", "a", 4),
		envelopeOf("orders", "", 5),
		envelopeOf("", "a", 6),
	}

	for _, named := range []struct {
		what      string
		sequencer projection.Sequencer
		names     string
		distinct  int
	}{
		{"by stream", projection.ByStream(), "by-stream", 5},
		{"unordered", projection.Unordered(), "unordered", 6},
		{"one sequence", projection.OneSequence(), "one-sequence", 1},
		{"by the application's own key", projection.SequenceBy("by-order", func(envelope event.Envelope) string {
			return string(envelope.Stream.Key)
		}), "by-order", 3},
	} {
		t.Run(named.what, func(t *testing.T) {
			if named.sequencer.Name() != named.names {
				t.Fatalf("%s names itself %q, and the name is what a parked letter records", named.what, named.sequencer.Name())
			}
			seen := map[string]bool{}
			for _, envelope := range envelopes {
				first := named.sequencer.SequenceOf(envelope)
				if second := named.sequencer.SequenceOf(envelope); first != second {
					t.Fatalf("%s answered %q and then %q for one envelope, and a key that moves is applied twice or not at all", named.what, first, second)
				}
				seen[first] = true
			}
			if len(seen) != named.distinct {
				t.Fatalf("%s answered %d distinct keys over %d envelopes where %d were expected", named.what, len(seen), len(envelopes), named.distinct)
			}
		})
	}

	t.Run("unordered is stable rather than fresh, so a re-read lands where it did", func(t *testing.T) {
		held := projection.Unordered()
		envelope := envelopeOf("orders", "a", 7)
		if held.SequenceOf(envelope) != held.SequenceOf(envelopeOf("orders", "a", 7)) {
			t.Fatal("two reads of one envelope answered two keys, and a partition a re-delivery moves to has already applied the ones before it")
		}
		if held.SequenceOf(envelope) == held.SequenceOf(envelopeOf("orders", "a", 8)) {
			t.Fatal("two envelopes at two positions answered one key, so Unordered orders what it says it does not")
		}
	})

	t.Run("by stream carries the key, and the rendering it is not carries only the family", func(t *testing.T) {
		held := projection.ByStream()
		first, second := envelopeOf("orders", "a", 1), envelopeOf("orders", "b", 2)
		if held.SequenceOf(first) == held.SequenceOf(second) {
			t.Fatalf("two streams of one family answered the key %q, so a four-way cover puts the whole family in one partition and one poison order parks every order there is", held.SequenceOf(first))
		}
		if first.Stream.String() != second.Stream.String() {
			t.Fatalf("the control assumed a stream renders its family alone and %q and %q came back, so the arm above measures nothing", first.Stream, second.Stream)
		}
	})

	t.Run("by stream distinguishes two part lists a separator would render alike", func(t *testing.T) {
		held := projection.ByStream()
		if held.SequenceOf(envelopeOf("orders/paid", "a", 1)) == held.SequenceOf(envelopeOf("orders", "paid/a", 2)) {
			t.Fatal("two distinct streams answered one key, and a composition that is not injective puts two streams in one sequence for ever")
		}
	})
}

// The two panics are told apart, and the difference is where they leave the
// envelope. A handler's is that page's failure and reaches the configured policy
// with the page delivered. A sequencer's is not recoverable into anything: an
// envelope that could not be assigned to a sequence belongs to no partition, so
// there is nothing for a retry, a quarantine or a park to be about, and the
// projection stops.
func TestASequencerPanicHaltsAndAHandlerPanicDoesNot(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "k0", "one")

	spec := stand.spec("orders", applies(stand.model))
	spec.Partition = partitionOf(t, 0, 1)
	spec.Sequence = projection.SequenceBy("by-order", func(event.Envelope) string {
		panic("this envelope has no key")
	})
	running(t, newProjection(t, spec))

	halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
		return state.Phase == projection.PhaseHalted
	})
	if !errors.Is(halted.Err, projection.ErrHalted) {
		t.Fatalf("a sequencer that panicked was answered %v, and an envelope with no sequence is a halt", halted.Err)
	}
	if !strings.Contains(halted.Err.Error(), "by-order") {
		t.Fatalf("the halt reads %q and does not name the sequencer, which is the one thing an operator has to change", halted.Err)
	}
	if rows := stand.model.rows(); len(rows) != 0 {
		t.Fatalf("the handler applied %v for a page no envelope of which could be assigned to a partition", rows)
	}
	if row := stand.row(t, "orders#0.1"); !row.Fresh() {
		t.Fatalf("the checkpoint stands at advance %d after a sequencer panicked, so a page nothing could be matched in was accounted for", row.Advance)
	}

	t.Run("the control: a handler panic on the same envelope is that page's failure", func(t *testing.T) {
		other := newStand(t, eventmemory.Spec{})
		other.append(t, "k0", "one")

		var calls atomic.Int64
		spec := other.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
			calls.Add(1)
			panic("this payload is not one I read")
		}))
		spec.Partition, spec.Sequence = partitionOf(t, 0, 1), projection.SequenceBy("by-order", func(event.Envelope) string {
			return "k0"
		})
		running(t, newProjection(t, spec))

		halted := other.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !strings.Contains(halted.Err.Error(), "the handler panicked") {
			t.Fatalf("a handler that panicked was answered %q, which does not name the handler, so the two panics are not told apart", halted.Err)
		}
		if calls.Load() == 0 {
			t.Fatal("the handler was never called, so this control does not exercise the page a handler panics on")
		}
	})
}
