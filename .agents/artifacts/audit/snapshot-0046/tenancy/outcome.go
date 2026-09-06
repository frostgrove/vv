package tenancy

import "errors"

type Outcome string

const (
	OutcomeOk           Outcome = "ok"
	OutcomeAbsent       Outcome = "absent"
	OutcomeUntrusted    Outcome = "untrusted"
	OutcomeInactive     Outcome = "inactive"
	OutcomeStale        Outcome = "stale"
	OutcomeIncompatible Outcome = "incompatible"
	OutcomeUnmapped     Outcome = "unmapped"
	OutcomeGrant        Outcome = "grant"
	OutcomePinned       Outcome = "pinned"
	OutcomeCapacity     Outcome = "capacity"
	OutcomeUnavailable  Outcome = "unavailable"
	OutcomeError        Outcome = "error"
)

func Outcomes() []Outcome {
	return []Outcome{
		OutcomeOk, OutcomeAbsent, OutcomeUntrusted, OutcomeInactive, OutcomeStale,
		OutcomeIncompatible, OutcomeUnmapped, OutcomeGrant, OutcomePinned,
		OutcomeCapacity, OutcomeUnavailable, OutcomeError,
	}
}

var outcomeOf = map[error]Outcome{
	ErrNoScope:       OutcomeAbsent,
	ErrUntrusted:     OutcomeUntrusted,
	ErrInactive:      OutcomeInactive,
	ErrStale:         OutcomeStale,
	ErrIncompatible:  OutcomeIncompatible,
	ErrUnmapped:      OutcomeUnmapped,
	ErrGrantRequired: OutcomeGrant,
	ErrPinned:        OutcomePinned,
	ErrCapacity:      OutcomeCapacity,
	ErrUnavailable:   OutcomeUnavailable,
	ErrMalformed:     OutcomeUntrusted,
}

// The whole point of this function is that a signal built from it cannot carry a
// tenant, a database or a driver's error text: the answer comes from a closed
// set of twelve constants and never from the error's message.
func OutcomeFor(err error) Outcome {
	if err == nil {
		return OutcomeOk
	}
	for _, refusal := range refusals {
		if errors.Is(err, refusal) {
			return outcomeOf[refusal]
		}
	}
	if errors.Is(err, ErrMalformed) {
		return OutcomeUntrusted
	}
	return OutcomeError
}
