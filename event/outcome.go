package event

type Outcome uint8

const (
	Unclassified Outcome = iota // the zero value: the door's fail-safe default
	Conflict                    // the stream was not at AppendRequest.Expected
	NotWritten                  // the write certainly did not land
	Unconfirmed                 // the write was issued and never confirmed
	Closed                      // this store refused rather than tried
	BadCursor                   // not minted over this backing, unparseable, or retired
	Refused                     // refused as a matter of this store's own policy
)

func (this Outcome) String() string {
	switch this {
	case Conflict:
		return "[outcome conflict]"
	case NotWritten:
		return "[outcome not written]"
	case Unconfirmed:
		return "[outcome unconfirmed]"
	case Closed:
		return "[outcome closed]"
	case BadCursor:
		return "[outcome bad cursor]"
	case Refused:
		return "[outcome refused]"
	default:
		return "[outcome unclassified]"
	}
}

// A classified failure deliberately does not unwrap to its cause, so a
// classified cancellation cannot read as a bare one however the kernel orders
// its questions. It renders its outcome and nothing of its cause, for the same
// reason a refusal does: a decorator that logs the failure it is forwarding
// would otherwise print a driver's text, and a driver's text names the key, the
// version and sometimes the credentials it connected with.
type failure struct {
	outcome Outcome
	cause   error
}

// The whole of the store-to-kernel error channel: a store adds no sentinel of
// its own, it selects one of these and the kernel maps the selection. A nil
// cause is legal and says the store has nothing to add. An outcome outside the
// seven becomes Unclassified here, before the value can travel, so the map's
// totality is a property of this constructor rather than a hope about callers.
// A cancellation is returned bare and never classified: wrapped in a failure it
// loses its identity to the door's own answer, by design.
func Failure(outcome Outcome, cause error) error {
	if outcome > Refused {
		outcome = Unclassified
	}
	return &failure{outcome: outcome, cause: cause}
}

func (this *failure) Error() string {
	return "event: the store reported " + this.outcome.String()
}
