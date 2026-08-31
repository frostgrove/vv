package jobs

import (
	"fmt"
	"time"
)

type AttemptOrdinal struct{ value uint16 }

func NewAttemptOrdinal(value uint16) (AttemptOrdinal, error) {
	if value > MaxAttemptOrdinal {
		return AttemptOrdinal{}, tooLarge("attempt ordinal")
	}
	return AttemptOrdinal{value: value}, nil
}

func (o AttemptOrdinal) Value() uint16 { return o.value }
func (o AttemptOrdinal) IsZero() bool  { return o.value == 0 }
func (o AttemptOrdinal) valid() bool   { return o.value <= MaxAttemptOrdinal }

type RetrySpent struct{ value uint16 }

func NewRetrySpent(value uint16) (RetrySpent, error) {
	if value > MaximumRetries {
		return RetrySpent{}, tooLarge("retry budget spent")
	}
	return RetrySpent{value: value}, nil
}

func (s RetrySpent) Value() uint16 { return s.value }
func (s RetrySpent) IsZero() bool  { return s.value == 0 }
func (s RetrySpent) valid() bool   { return s.value <= MaximumRetries }

type HandlerDeferrals struct{ value uint16 }

func NewHandlerDeferrals(value uint16) (HandlerDeferrals, error) {
	if value > MaximumHandlerDeferrals {
		return HandlerDeferrals{}, tooLarge("handler deferrals")
	}
	return HandlerDeferrals{value: value}, nil
}

func (d HandlerDeferrals) Value() uint16 { return d.value }
func (d HandlerDeferrals) IsZero() bool  { return d.value == 0 }
func (d HandlerDeferrals) valid() bool   { return d.value <= MaximumHandlerDeferrals }

type AttemptState uint8

const (
	AttemptRunning AttemptState = iota + 1
	AttemptFinished
)

func (s AttemptState) Valid() bool { return s >= AttemptRunning && s <= AttemptFinished }
func (s AttemptState) String() string {
	switch s {
	case AttemptRunning:
		return "running"
	case AttemptFinished:
		return "finished"
	default:
		return "unknown"
	}
}

type BeginAttemptSpec struct {
	Binding   BindingName
	Build     BuildID
	StartedAt time.Time
}

func (s BeginAttemptSpec) String() string { return "[job attempt start]" }
func (s BeginAttemptSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type FinishAttemptSpec struct {
	FinishedAt  time.Time
	Disposition Disposition
	AvailableAt time.Time
}

func (s FinishAttemptSpec) String() string { return "[job attempt finish]" }
func (s FinishAttemptSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type AttemptRecord struct {
	Invocation       InvocationID
	Ordinal          AttemptOrdinal
	Binding          BindingName
	Build            BuildID
	State            AttemptState
	StartedAt        time.Time
	Deadline         time.Time
	ProgressedAt     time.Time
	ProgressDeadline time.Time
	FinishedAt       time.Time
	Disposition      Disposition
}

func (r AttemptRecord) String() string { return "[job attempt record]" }
func (r AttemptRecord) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}

type Attempt struct {
	invocation       InvocationID
	ordinal          AttemptOrdinal
	binding          BindingName
	build            BuildID
	state            AttemptState
	startedAt        time.Time
	deadline         time.Time
	progressedAt     time.Time
	progressDeadline time.Time
	finishedAt       time.Time
	disposition      Disposition
}

func (a Attempt) InvocationID() InvocationID  { return a.invocation }
func (a Attempt) Ordinal() AttemptOrdinal     { return a.ordinal }
func (a Attempt) Binding() BindingName        { return a.binding }
func (a Attempt) Build() BuildID              { return a.build }
func (a Attempt) State() AttemptState         { return a.state }
func (a Attempt) StartedAt() time.Time        { return a.startedAt }
func (a Attempt) Deadline() time.Time         { return a.deadline }
func (a Attempt) ProgressedAt() time.Time     { return a.progressedAt }
func (a Attempt) ProgressDeadline() time.Time { return a.progressDeadline }
func (a Attempt) FinishedAt() time.Time       { return a.finishedAt }
func (a Attempt) Disposition() Disposition    { return a.disposition }
func (a Attempt) IsZero() bool                { return a.invocation.IsZero() }
func (a Attempt) Record() AttemptRecord {
	return AttemptRecord{
		Invocation:       a.invocation,
		Ordinal:          a.ordinal,
		Binding:          a.binding,
		Build:            a.build,
		State:            a.state,
		StartedAt:        a.startedAt,
		Deadline:         a.deadline,
		ProgressedAt:     a.progressedAt,
		ProgressDeadline: a.progressDeadline,
		FinishedAt:       a.finishedAt,
		Disposition:      a.disposition,
	}
}
func (a Attempt) String() string { return "[job attempt]" }
func (a Attempt) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, a.String())
}

func attemptFromRecord(record AttemptRecord) Attempt {
	return Attempt{
		invocation:       record.Invocation,
		ordinal:          record.Ordinal,
		binding:          record.Binding,
		build:            record.Build,
		state:            record.State,
		startedAt:        record.StartedAt,
		deadline:         record.Deadline,
		progressedAt:     record.ProgressedAt,
		progressDeadline: record.ProgressDeadline,
		finishedAt:       record.FinishedAt,
		disposition:      record.Disposition,
	}
}
