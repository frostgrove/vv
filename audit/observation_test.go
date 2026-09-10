package audit_test

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/audit"
)

func TestObserverFanoutIsOrderedAndPanicIsolated(t *testing.T) {
	var order []int
	observer, err := audit.Observers(
		audit.ObserverFunc(func(audit.AuditObservation) { order = append(order, 1) }),
		audit.ObserverFunc(func(audit.AuditObservation) { panic("observer") }),
		audit.ObserverFunc(func(audit.AuditObservation) { order = append(order, 3) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	observer.ObserveAudit(audit.AuditObservation{Phase: audit.ObservationStore, Kind: audit.ObservationRecord})
	if len(order) != 2 || order[0] != 1 || order[1] != 3 {
		t.Fatalf("observer order = %v", order)
	}
}

func TestObserverSetRejectsAnEmptyOrOversizedDeclaration(t *testing.T) {
	if _, err := audit.Observers(nil, audit.ObserverFunc(nil)); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("empty observer set = %v", err)
	}
	values := make([]audit.Observer, audit.MaxObservers+1)
	for index := range values {
		values[index] = audit.ObserverFunc(func(audit.AuditObservation) {})
	}
	if _, err := audit.Observers(values...); !errors.Is(err, audit.ErrTooLarge) {
		t.Fatalf("oversized observer set = %v", err)
	}
}
