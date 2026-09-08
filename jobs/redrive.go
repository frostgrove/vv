package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

var ErrInvocationNotFound = errors.New("jobs: invocation not found")

const DefaultListLimit = 100
const MaxListLimit = 1000
const MaxListOffset = 1_000_000
const MaxListDefinitions = 128
const DefaultPurgeLimit = 100
const MaxPurgeLimit = 1000

// After is a keyset cursor: the (CreatedAt, ID) of the last row the caller saw,
// in the same order List returns. Prefer it to Offset. An offset makes the
// database walk and discard every row before the page, so the cost of page N
// grows with N and a namespace with a long retention makes the last pages of the
// operations view unusable — while a cursor reads the page and nothing else.
// Offset is kept for the caller who has no previous row to point at.
type ListCursor struct {
	CreatedAt time.Time
	ID        InvocationID
}

type ListSpec struct {
	Definitions []Name
	States      []InvocationState
	Limit       int
	Offset      int
	After       *ListCursor
}

type Admin interface {
	Get(context.Context, InvocationID) (DeliveryView, error)
	List(context.Context, ListSpec) ([]DeliveryView, error)
	Count(context.Context, ListSpec) (int64, error)
	Redrive(context.Context, InvocationID) (DeliveryView, error)
	PurgeTerminal(context.Context, time.Time, int) (int, error)
}

type DeliveryView struct {
	invocation Invocation
	payload    EncodedPayload
}

func NewDeliveryView(invocation Invocation, payload EncodedPayload) (DeliveryView, error) {
	if invocation.IsZero() || payload.IsZero() || payload.encodedLength() > invocation.Policy().Payload().MaxBytes {
		return DeliveryView{}, invalid("delivery view")
	}
	return DeliveryView{invocation: invocation, payload: cloneEncodedPayload(payload)}, nil
}

func (v DeliveryView) Invocation() Invocation  { return v.invocation }
func (v DeliveryView) Payload() EncodedPayload { return cloneEncodedPayload(v.payload) }
func (v DeliveryView) IsZero() bool            { return v.invocation.IsZero() }
func (DeliveryView) String() string            { return "[job delivery view]" }
func (v DeliveryView) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, v.String())
}
func (v DeliveryView) LogValue() slog.Value { return slog.StringValue(v.String()) }
func (DeliveryView) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery view cannot be serialized", ErrUnsupported)
}

func RedriveInvocation(invocation Invocation, at time.Time) (Invocation, error) {
	if invocation.IsZero() {
		return Invocation{}, invalid("redrive invocation")
	}
	if !invocation.IsTerminal() {
		return Invocation{}, transitionConflict("only a terminal invocation can be redriven")
	}
	at, err := requiredTime(at, "redrive time")
	if err != nil {
		return Invocation{}, err
	}
	startBefore := time.Time{}
	if current := invocation.StartBefore(); !current.IsZero() {
		startBefore, err = requiredTime(at.Add(current.Sub(invocation.EligibleAt())), "redrive start deadline")
		if err != nil {
			return Invocation{}, err
		}
	}
	legacy, _ := invocation.LegacyIntent()
	return NewInvocation(InvocationSpec{
		ID:           invocation.ID(),
		Namespace:    invocation.Namespace(),
		Partition:    invocation.Partition(),
		Definition:   invocation.Definition(),
		Queue:        invocation.Queue(),
		Mode:         invocation.Mode(),
		Intent:       invocation.Intent(),
		LegacyIntent: legacy,
		Priority:     invocation.Priority(),
		CreatedAt:    at,
		EligibleAt:   at,
		StartBefore:  startBefore,
		Policy:       invocation.Policy(),
		Context:      invocation.Context(),
	})
}
