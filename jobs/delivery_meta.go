package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type ProgressReporter interface {
	Pulse(context.Context) error
}

type AdapterHandler[P any] func(context.Context, P, DeliveryMeta, ProgressReporter) error

type DeliveryMetaSpec struct {
	Invocation       InvocationID
	Definition       Name
	Binding          BindingName
	Build            BuildID
	Attempt          AttemptOrdinal
	CreatedAt        time.Time
	EligibleAt       time.Time
	StartedAt        time.Time
	AttemptDeadline  time.Time
	MaxElapsedAt     time.Time
	ProgressDeadline time.Time
}

func (DeliveryMetaSpec) String() string { return "[job delivery metadata specification]" }
func (spec DeliveryMetaSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, spec.String())
}
func (spec DeliveryMetaSpec) LogValue() slog.Value { return slog.StringValue(spec.String()) }
func (DeliveryMetaSpec) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery metadata specification cannot be serialized", ErrUnsupported)
}

type DeliveryMeta struct {
	invocation       InvocationID
	definition       Name
	binding          BindingName
	build            BuildID
	attempt          AttemptOrdinal
	createdAt        time.Time
	eligibleAt       time.Time
	startedAt        time.Time
	attemptDeadline  time.Time
	maxElapsedAt     time.Time
	progressDeadline time.Time
}

func NewDeliveryMeta(spec DeliveryMetaSpec) (DeliveryMeta, error) {
	if !spec.Invocation.valid() || !spec.Definition.valid() || !spec.Binding.valid() || !spec.Build.valid() || spec.Attempt.IsZero() || !spec.Attempt.valid() {
		return DeliveryMeta{}, invalid("delivery metadata identity")
	}
	createdAt, err := requiredTime(spec.CreatedAt, "delivery creation time")
	if err != nil {
		return DeliveryMeta{}, err
	}
	eligibleAt, err := requiredTime(spec.EligibleAt, "delivery eligibility time")
	if err != nil {
		return DeliveryMeta{}, err
	}
	startedAt, err := requiredTime(spec.StartedAt, "delivery attempt start")
	if err != nil {
		return DeliveryMeta{}, err
	}
	attemptDeadline, err := requiredTime(spec.AttemptDeadline, "delivery attempt deadline")
	if err != nil {
		return DeliveryMeta{}, err
	}
	maxElapsedAt, err := requiredTime(spec.MaxElapsedAt, "delivery elapsed deadline")
	if err != nil {
		return DeliveryMeta{}, err
	}
	progressDeadline, err := optionalTime(spec.ProgressDeadline, "delivery progress deadline")
	if err != nil {
		return DeliveryMeta{}, err
	}
	if eligibleAt.Before(createdAt) || eligibleAt.Sub(createdAt) > MaxRetention || startedAt.Before(eligibleAt) || !attemptDeadline.After(startedAt) || attemptDeadline.Sub(startedAt) > MaximumAttemptTimeout || !maxElapsedAt.After(eligibleAt) || maxElapsedAt.Sub(eligibleAt) > MaximumMaxElapsed || attemptDeadline.After(maxElapsedAt) {
		return DeliveryMeta{}, invalid("delivery metadata time bounds")
	}
	if !progressDeadline.IsZero() && (!progressDeadline.After(startedAt) || progressDeadline.After(attemptDeadline)) {
		return DeliveryMeta{}, invalid("delivery metadata progress deadline")
	}
	return DeliveryMeta{
		invocation:       spec.Invocation,
		definition:       spec.Definition,
		binding:          spec.Binding,
		build:            spec.Build,
		attempt:          spec.Attempt,
		createdAt:        createdAt,
		eligibleAt:       eligibleAt,
		startedAt:        startedAt,
		attemptDeadline:  attemptDeadline,
		maxElapsedAt:     maxElapsedAt,
		progressDeadline: progressDeadline,
	}, nil
}

func (m DeliveryMeta) InvocationID() InvocationID     { return m.invocation }
func (m DeliveryMeta) Definition() Name               { return m.definition }
func (m DeliveryMeta) Binding() BindingName           { return m.binding }
func (m DeliveryMeta) Build() BuildID                 { return m.build }
func (m DeliveryMeta) AttemptOrdinal() AttemptOrdinal { return m.attempt }
func (m DeliveryMeta) CreatedAt() time.Time           { return m.createdAt }
func (m DeliveryMeta) EligibleAt() time.Time          { return m.eligibleAt }
func (m DeliveryMeta) StartedAt() time.Time           { return m.startedAt }
func (m DeliveryMeta) AttemptDeadline() time.Time     { return m.attemptDeadline }
func (m DeliveryMeta) MaxElapsedAt() time.Time        { return m.maxElapsedAt }
func (m DeliveryMeta) ProgressDeadline() time.Time    { return m.progressDeadline }
func (m DeliveryMeta) String() string                 { return "[job delivery metadata]" }
func (m DeliveryMeta) LogValue() slog.Value           { return slog.StringValue(m.String()) }
func (m DeliveryMeta) IsZero() bool                   { return m.invocation.IsZero() }
func (m DeliveryMeta) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, m.String()) }
func (DeliveryMeta) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery metadata cannot be serialized", ErrUnsupported)
}
func (m DeliveryMeta) valid() bool {
	canonical, err := NewDeliveryMeta(DeliveryMetaSpec{
		Invocation:       m.invocation,
		Definition:       m.definition,
		Binding:          m.binding,
		Build:            m.build,
		Attempt:          m.attempt,
		CreatedAt:        m.createdAt,
		EligibleAt:       m.eligibleAt,
		StartedAt:        m.startedAt,
		AttemptDeadline:  m.attemptDeadline,
		MaxElapsedAt:     m.maxElapsedAt,
		ProgressDeadline: m.progressDeadline,
	})
	return err == nil && canonical == m
}
