package projection

import (
	"context"
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

const (
	Halt Failure = iota
	Quarantine
)

func (this Failure) Valid() bool { return this == Halt || this == Quarantine }

// What a sink is handed: the projection whose page could not apply this
// envelope, the envelope itself, and the failure its classifier called
// permanent.
type Quarantined struct {
	Projection string
	Envelope   event.Envelope
	Cause      error
}

// Where an envelope goes when the classifier calls its failure permanent and the
// spec asks for it to be passed rather than to stop the projection.
//
// It is called INSIDE the unit of work under InUnit, through the context it is
// given: a sink called outside it is the one write that survives the rollback of
// the advance, and its record then names an envelope that is redelivered and
// recorded a second time with no error anywhere. An error from it ends the
// isolation pass and halts the projection, because a policy with nowhere to
// record is a skip with extra words.
type Quarantines interface {
	Quarantine(ctx context.Context, quarantined Quarantined) error
}
