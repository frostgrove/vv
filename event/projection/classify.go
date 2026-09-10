package projection

import (
	"errors"

	"github.com/frostgrove/vv/event"
)

type Verdict uint8

const (
	Retryable Verdict = iota
	Permanent
)

type Classifier func(err error) Verdict

// The history class and ErrUnrouted are permanent — a payload this build cannot
// read does not become readable by trying again, and neither does a type nothing
// routes — and everything else is retryable. ErrUnrouted is permanent by this
// rule rather than by membership of a class it is not in, so one refusal covers
// the forgotten registration and the foreign family a router was told to refuse.
func Classify(err error) Verdict {
	for _, terminal := range []error{
		event.ErrUnknownType, event.ErrRevision, event.ErrPayload, event.ErrUpcast, ErrUnrouted,
	} {
		if errors.Is(err, terminal) {
			return Permanent
		}
	}
	return Retryable
}

type Failure uint8

// ParkSequence and not Park: what it parks is the SEQUENCE — the failing
// envelope and every later envelope of the same sequence, which never reach a
// handler at all — and a package-level const Park could not stand beside the
// Park interface the letters go to.
const (
	Halt Failure = iota
	ParkSequence
)

func (this Failure) Valid() bool { return this == Halt || this == ParkSequence }
