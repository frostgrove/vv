package projection

import (
	"strconv"

	"github.com/frostgrove/vv/event"
)

// A sequence is the set of events that must be applied in the order the log holds
// them: two envelopes with one key are ordered with respect to each other, and
// two with different keys are not.
//
// SequenceOf is total, pure and stable for the life of the log. It reads the
// envelope and nothing else — no clock, no map iteration, no process-local state
// — because the key an envelope produced in one release is the key it must
// produce in every later one. There is no error: an envelope that could not be
// assigned to a sequence has no sequence to be parked in, so a panic out of one
// halts the projection rather than failing its page. None of the three is checked
// at run time, and the supported way to change a key is a new generation.
//
// Name is recorded on every parked letter, and a redrive whose spec names a
// different sequencer than the letter carries is refused.
type Sequencer interface {
	Name() string
	SequenceOf(envelope event.Envelope) string
}

// The stream a fact belongs to, and the default when Spec.Sequence is nil. It is
// the kernel's own composition of the family and the key rather than the stream's
// rendering, which carries the family alone: rendered, every stream of one family
// would be one sequence, so a four-way cover would put the whole family in one
// partition and one poison order would park every order in the system.
func ByStream() Sequencer {
	return sequencer{name: "by-stream", of: func(envelope event.Envelope) string {
		return string(event.Compose(envelope.Stream.Family, string(envelope.Stream.Key)))
	}}
}

// Every envelope its own sequence, which is the honest name for a projection with
// no ordering requirement. The key is the envelope's position and not a fresh
// one: a re-delivery after a restart has to land in the partition that read it,
// because vv's partitions are separate processes and a key that moved is applied
// twice or not at all.
func Unordered() Sequencer {
	return sequencer{name: "unordered", of: func(envelope event.Envelope) string {
		return strconv.FormatUint(uint64(envelope.Position), 10)
	}}
}

// One key for the whole log, which is a projection with no concurrency at all.
// It is what a park is for when one poison event should stop everything.
func OneSequence() Sequencer {
	return sequencer{name: "one-sequence", of: func(event.Envelope) string { return "all" }}
}

// The application's own key, by the read model it conflicts on. An empty name or
// a nil function answers a sequencer New refuses, because a refusal at boot is
// cheaper than a nil call on the first page.
func SequenceBy(name string, of func(event.Envelope) string) Sequencer {
	return sequencer{name: name, of: of}
}

type sequencer struct {
	name string
	of   func(event.Envelope) string
}

func (this sequencer) Name() string { return this.name }

func (this sequencer) SequenceOf(envelope event.Envelope) string { return this.of(envelope) }

// What New refuses about a Sequence, and it is two questions rather than one: any
// sequencer at all must name itself, because the name is what a parked letter
// records, and one of this package's own must carry the function it was given.
func unusable(sequence Sequencer) string {
	if sequence.Name() == "" {
		return "Sequence names a sequencer with no name, and the name is what a parked letter records so a redrive can refuse a queue built under another key"
	}
	if held, ours := sequence.(sequencer); ours && held.of == nil {
		return "Sequence is a SequenceBy over a nil function, which has no key to answer for any envelope"
	}
	return ""
}
