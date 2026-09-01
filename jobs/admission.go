package jobs

import (
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

const MaxHeldReasonBytes = 64

const (
	DefaultAdmissionFreshness = 30 * time.Second
	MaximumAdmissionFreshness = 24 * time.Hour
)

var (
	ErrAdmissionHeld          = errors.New("jobs: admission held")
	ErrAdmissionStale         = errors.New("jobs: admission snapshot is stale")
	ErrAdmissionUninitialized = errors.New("jobs: admission snapshot is uninitialized")
)

type HeldReason struct{ value string }

func ParseHeldReason(raw string) (HeldReason, error) {
	value, err := parseRegistryName(raw, MaxHeldReasonBytes, "admission held reason")
	if err != nil {
		return HeldReason{}, err
	}
	return HeldReason{value: value}, nil
}

func (r HeldReason) Value() string  { return r.value }
func (r HeldReason) String() string { return r.value }
func (r HeldReason) IsZero() bool   { return r.value == "" }
func (r HeldReason) valid() bool {
	return validRegistryName(r.value, MaxHeldReasonBytes)
}

type Admission struct {
	limit       int
	heldReason  HeldReason
	observedAt  time.Time
	initialized bool
}

func NewAdmission(limit int, heldReason HeldReason, observedAt time.Time) (Admission, error) {
	if limit < 0 {
		return Admission{}, admissionError(AdmissionInvalid, HeldReason{})
	}
	if limit > MaxWorkerConcurrency {
		return Admission{}, tooLarge("admission limit")
	}
	if !heldReason.IsZero() && !heldReason.valid() {
		return Admission{}, admissionError(AdmissionInvalid, HeldReason{})
	}
	if (limit == 0 && heldReason.IsZero()) || (limit > 0 && !heldReason.IsZero()) {
		return Admission{}, admissionError(AdmissionInvalid, HeldReason{})
	}
	observedAt, err := requiredTime(observedAt, "admission observation time")
	if err != nil {
		return Admission{}, admissionError(AdmissionInvalid, heldReason)
	}
	return Admission{
		limit:       limit,
		heldReason:  heldReason,
		observedAt:  observedAt,
		initialized: true,
	}, nil
}

func (a Admission) Limit() int             { return a.limit }
func (a Admission) HeldReason() HeldReason { return a.heldReason }
func (a Admission) ObservedAt() time.Time  { return a.observedAt }
func (a Admission) IsInitialized() bool    { return a.initialized }
func (Admission) String() string           { return "[job admission]" }
func (a Admission) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, a.String())
}
func (a Admission) LogValue() slog.Value { return slog.StringValue(a.String()) }
func (Admission) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: admission cannot be serialized", ErrUnsupported)
}
func (a Admission) valid() bool {
	if !a.initialized || a.limit < 0 || a.limit > MaxWorkerConcurrency || a.observedAt.IsZero() {
		return false
	}
	if !a.heldReason.IsZero() && !a.heldReason.valid() {
		return false
	}
	canonical, err := requiredTime(a.observedAt, "admission observation time")
	return err == nil && (a.limit == 0) == !a.heldReason.IsZero() && a.observedAt == canonical
}

type AdmissionSignal uint8

const (
	AdmissionUninitialized AdmissionSignal = iota
	AdmissionReady
	AdmissionUnrestricted
	AdmissionHeld
	AdmissionStale
	AdmissionInvalid
)

func (s AdmissionSignal) String() string {
	switch s {
	case AdmissionUninitialized:
		return "uninitialized"
	case AdmissionReady:
		return "ready"
	case AdmissionUnrestricted:
		return "unrestricted"
	case AdmissionHeld:
		return "held"
	case AdmissionStale:
		return "stale"
	case AdmissionInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

func (s AdmissionSignal) Valid() bool { return s <= AdmissionInvalid }

type AdmissionError struct {
	signal     AdmissionSignal
	heldReason HeldReason
}

func (e AdmissionError) Error() string {
	switch e.signal {
	case AdmissionHeld:
		return ErrAdmissionHeld.Error()
	case AdmissionStale:
		return ErrAdmissionStale.Error()
	case AdmissionUninitialized:
		return ErrAdmissionUninitialized.Error()
	default:
		return "jobs: invalid admission decision"
	}
}

func (e AdmissionError) Unwrap() error {
	switch e.signal {
	case AdmissionHeld:
		return ErrAdmissionHeld
	case AdmissionStale:
		return ErrAdmissionStale
	case AdmissionUninitialized:
		return ErrAdmissionUninitialized
	default:
		return ErrInvalid
	}
}

func (e AdmissionError) Signal() AdmissionSignal { return e.signal }
func (e AdmissionError) HeldReason() HeldReason  { return e.heldReason }
func (e AdmissionError) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, e.Error())
}
func (e AdmissionError) LogValue() slog.Value { return slog.StringValue(e.Error()) }
func (AdmissionError) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: admission error cannot be serialized", ErrUnsupported)
}

type AdmissionDecision struct {
	limit      int
	signal     AdmissionSignal
	heldReason HeldReason
	observedAt time.Time
}

func (d AdmissionDecision) Limit() int              { return d.limit }
func (d AdmissionDecision) Signal() AdmissionSignal { return d.signal }
func (d AdmissionDecision) HeldReason() HeldReason  { return d.heldReason }
func (d AdmissionDecision) ObservedAt() time.Time   { return d.observedAt }
func (d AdmissionDecision) Err() error {
	if d.signal == AdmissionReady || d.signal == AdmissionUnrestricted {
		return nil
	}
	return admissionError(d.signal, d.heldReason)
}
func (d AdmissionDecision) Available(inFlight int) (int, error) {
	if err := d.Err(); err != nil {
		return 0, err
	}
	if inFlight < 0 {
		return 0, admissionError(AdmissionInvalid, HeldReason{})
	}
	if inFlight >= d.limit {
		return 0, nil
	}
	return d.limit - inFlight, nil
}
func (AdmissionDecision) String() string { return "[job admission decision]" }
func (d AdmissionDecision) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, d.String())
}
func (d AdmissionDecision) LogValue() slog.Value { return slog.StringValue(d.String()) }
func (AdmissionDecision) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: admission decision cannot be serialized", ErrUnsupported)
}

type admissionState struct {
	admission  Admission
	signal     AdmissionSignal
	heldReason HeldReason
	observedAt time.Time
}

func (s admissionState) time() time.Time {
	if s.signal == AdmissionReady {
		return s.admission.observedAt
	}
	return s.observedAt
}

func (s admissionState) same(other admissionState) bool {
	if s.signal != other.signal || s.heldReason != other.heldReason || s.observedAt != other.observedAt {
		return false
	}
	return s.signal != AdmissionReady || s.admission == other.admission
}

type admissionCell struct {
	value atomic.Pointer[admissionState]
}

type AdmissionSnapshot struct {
	cell        *admissionCell
	freshness   time.Duration
	initialized bool
}

func NewAdmissionSnapshot(freshness time.Duration) (*AdmissionSnapshot, error) {
	if freshness <= 0 {
		return nil, invalid("admission freshness")
	}
	if freshness > MaximumAdmissionFreshness {
		return nil, tooLarge("admission freshness")
	}
	return &AdmissionSnapshot{
		cell:        &admissionCell{},
		freshness:   freshness,
		initialized: true,
	}, nil
}

func (s *AdmissionSnapshot) Publisher() AdmissionPublisher {
	if s == nil || !s.initialized {
		return AdmissionPublisher{}
	}
	return AdmissionPublisher{cell: s.cell, initialized: true}
}

func (s *AdmissionSnapshot) Reader() AdmissionReader {
	if s == nil || !s.initialized {
		return AdmissionReader{}
	}
	return AdmissionReader{cell: s.cell, freshness: s.freshness, initialized: true}
}

func (s *AdmissionSnapshot) Freshness() time.Duration {
	if s == nil || !s.initialized {
		return 0
	}
	return s.freshness
}

func (*AdmissionSnapshot) String() string { return "[job admission snapshot]" }
func (s *AdmissionSnapshot) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}
func (s *AdmissionSnapshot) LogValue() slog.Value { return slog.StringValue(s.String()) }
func (*AdmissionSnapshot) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: admission snapshot cannot be serialized", ErrUnsupported)
}

type AdmissionPublisher struct {
	cell        *admissionCell
	initialized bool
}

func (p AdmissionPublisher) Update(limit int, heldReason HeldReason, observedAt time.Time) error {
	admission, err := NewAdmission(limit, heldReason, observedAt)
	if err != nil {
		publishErr := p.publishInvalid(heldReason, observedAt)
		if publishErr != nil {
			return errors.Join(err, publishErr)
		}
		return err
	}
	return p.Publish(admission)
}

func (p AdmissionPublisher) Publish(admission Admission) error {
	if !p.initialized || p.cell == nil {
		return admissionError(AdmissionUninitialized, HeldReason{})
	}
	if !admission.valid() {
		return admissionError(AdmissionInvalid, HeldReason{})
	}
	return p.publish(admissionState{admission: admission, signal: AdmissionReady})
}

func (p AdmissionPublisher) Unrestricted(observedAt time.Time) error {
	if !p.initialized || p.cell == nil {
		return admissionError(AdmissionUninitialized, HeldReason{})
	}
	canonical, err := requiredTime(observedAt, "admission observation time")
	if err != nil {
		publishErr := p.publishInvalid(HeldReason{}, observedAt)
		if publishErr != nil {
			return errors.Join(err, publishErr)
		}
		return err
	}
	return p.publish(admissionState{signal: AdmissionUnrestricted, observedAt: canonical})
}

func (p AdmissionPublisher) publishInvalid(heldReason HeldReason, observedAt time.Time) error {
	if !p.initialized || p.cell == nil {
		return admissionError(AdmissionUninitialized, HeldReason{})
	}
	if !heldReason.IsZero() && !heldReason.valid() {
		heldReason = HeldReason{}
	}
	canonical, err := optionalTime(observedAt, "admission observation time")
	if err != nil {
		canonical = time.Time{}
	}
	return p.publish(admissionState{signal: AdmissionInvalid, heldReason: heldReason, observedAt: canonical})
}

func (p AdmissionPublisher) publish(next admissionState) error {
	for {
		current := p.cell.value.Load()
		if current != nil {
			currentAt := current.time()
			nextAt := next.time()
			if !currentAt.IsZero() && !nextAt.IsZero() {
				if nextAt.Before(currentAt) {
					return fmt.Errorf("%w: admission observed timestamp regressed", ErrConflict)
				}
				if nextAt == currentAt {
					if current.same(next) {
						return nil
					}
					return fmt.Errorf("%w: admission decisions disagree at one observation time", ErrConflict)
				}
			}
		}
		if p.cell.value.CompareAndSwap(current, &next) {
			return nil
		}
	}
}

func (AdmissionPublisher) String() string { return "[job admission publisher]" }
func (p AdmissionPublisher) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, p.String())
}
func (p AdmissionPublisher) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (AdmissionPublisher) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: admission publisher cannot be serialized", ErrUnsupported)
}

type AdmissionReader struct {
	cell        *admissionCell
	freshness   time.Duration
	initialized bool
}

func (r AdmissionReader) Evaluate(concurrency int, now time.Time) AdmissionDecision {
	if !r.initialized || r.cell == nil || r.freshness <= 0 || r.freshness > MaximumAdmissionFreshness {
		return AdmissionDecision{signal: AdmissionUninitialized}
	}
	if concurrency < 1 || concurrency > MaxWorkerConcurrency || now.IsZero() {
		return AdmissionDecision{signal: AdmissionInvalid}
	}
	current := r.cell.value.Load()
	if current == nil {
		return AdmissionDecision{signal: AdmissionUninitialized}
	}
	if current.signal == AdmissionInvalid {
		return AdmissionDecision{
			signal:     AdmissionInvalid,
			heldReason: current.heldReason,
			observedAt: current.observedAt,
		}
	}
	if current.signal == AdmissionUnrestricted {
		decision := AdmissionDecision{signal: AdmissionUnrestricted, observedAt: current.observedAt}
		now, err := requiredTime(now, "admission evaluation time")
		if err != nil || current.observedAt.After(now) {
			decision.signal = AdmissionInvalid
			return decision
		}
		if now.Sub(current.observedAt) > r.freshness {
			decision.signal = AdmissionStale
			return decision
		}
		decision.limit = concurrency
		return decision
	}
	admission := current.admission
	decision := AdmissionDecision{
		heldReason: admission.heldReason,
		observedAt: admission.observedAt,
	}
	if !admission.valid() || admission.limit > concurrency {
		decision.signal = AdmissionInvalid
		return decision
	}
	now, err := requiredTime(now, "admission evaluation time")
	if err != nil {
		decision.signal = AdmissionInvalid
		return decision
	}
	if admission.observedAt.After(now) {
		decision.signal = AdmissionInvalid
		return decision
	}
	if now.Sub(admission.observedAt) > r.freshness {
		decision.signal = AdmissionStale
		return decision
	}
	if admission.limit == 0 {
		decision.signal = AdmissionHeld
		return decision
	}
	decision.limit = admission.limit
	decision.signal = AdmissionReady
	return decision
}

func (r AdmissionReader) Freshness() time.Duration {
	if !r.initialized {
		return 0
	}
	return r.freshness
}
func (AdmissionReader) String() string { return "[job admission reader]" }
func (r AdmissionReader) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r AdmissionReader) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (AdmissionReader) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: admission reader cannot be serialized", ErrUnsupported)
}

func admissionError(signal AdmissionSignal, heldReason HeldReason) AdmissionError {
	if !signal.Valid() || signal == AdmissionReady {
		signal = AdmissionInvalid
	}
	if !heldReason.IsZero() && !heldReason.valid() {
		heldReason = HeldReason{}
	}
	return AdmissionError{signal: signal, heldReason: heldReason}
}
