package audit

import (
	"fmt"
	"slices"
	"time"
)

type Observer interface {
	ObserveAudit(AuditObservation)
}

type ObserverFunc func(AuditObservation)

func (f ObserverFunc) ObserveAudit(observation AuditObservation) {
	if f != nil {
		f(observation)
	}
}

type ObservationPhase uint8

const (
	ObservationResolve ObservationPhase = iota + 1
	ObservationPrepare
	ObservationStore
	ObservationVerify
	ObservationEvidence
)

type ObservationKind uint8

const (
	ObservationRecord ObservationKind = iota + 1
	ObservationGroup
	ObservationHistory
	ObservationReconstruction
	ObservationControl
	ObservationAttempt
)

type FailureClass uint8

const (
	NoFailure FailureClass = iota
	InvalidFailure
	DeniedFailure
	ConflictFailure
	NotWrittenFailure
	UnconfirmedFailure
	UnreadableFailure
	BackendFailure
)

type AuditWorkView struct {
	StoreCalls        uint32
	SnapshotReads     uint32
	PagesScanned      uint32
	RevisionsScanned  uint32
	BytesScanned      uint64
	RevisionsVerified uint32
	UnknownFields     uint32
	Gaps              uint32
	BudgetStopped     bool
}

type AuditObservation struct {
	Phase       ObservationPhase
	Kind        ObservationKind
	Items       uint16
	Bytes       uint64
	Duration    time.Duration
	Disposition AppendDisposition
	Settlement  Settlement
	Failure     FailureClass
	Work        AuditWorkView
}

type observerSet struct {
	observers []Observer
}

func Observers(observers ...Observer) (Observer, error) {
	if len(observers) > MaxObservers {
		return nil, auditTooLarge("observers", MaxObservers)
	}
	values := make([]Observer, 0, len(observers))
	for _, observer := range observers {
		if !nilByReflection(observer) {
			values = append(values, observer)
		}
	}
	if len(values) == 0 {
		return nil, auditErrorAt(ErrInvalid, "observers")
	}
	return observerSet{observers: slices.Clone(values)}, nil
}

func MustObservers(observers ...Observer) Observer {
	observer, err := Observers(observers...)
	if err != nil {
		panic(err)
	}
	return observer
}

func (set observerSet) ObserveAudit(observation AuditObservation) {
	for _, observer := range set.observers {
		func() {
			defer func() { _ = recover() }()
			observer.ObserveAudit(observation)
		}()
	}
}

func observe(observer Observer, observation AuditObservation) {
	if nilByReflection(observer) {
		return
	}
	defer func() { _ = recover() }()
	observer.ObserveAudit(observation)
}

func validObservation(observation AuditObservation) error {
	if observation.Phase < ObservationResolve || observation.Phase > ObservationEvidence {
		return fmt.Errorf("%w: observation phase is invalid", ErrInvalid)
	}
	if observation.Kind < ObservationRecord || observation.Kind > ObservationAttempt {
		return fmt.Errorf("%w: observation kind is invalid", ErrInvalid)
	}
	return nil
}
