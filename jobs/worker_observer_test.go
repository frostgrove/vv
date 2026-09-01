package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type workerObserverFixture struct {
	plan       WorkerPlan
	definition Name
	binding    BindingName
}

func newWorkerObserverFixture(t *testing.T) workerObserverFixture {
	t.Helper()
	definition := testQueueDefinition(t, "workers.observer", String(1))
	plan := MustWorkerPlan(
		MustCatalog(definition),
		On(definition, Handler[string](func(context.Context, string) error { return nil }), Binding("workers.observer.primary"), Concurrency(2)),
	)
	description := plan.Describe().Bindings[0]
	return workerObserverFixture{plan: plan, definition: description.Definition, binding: description.Binding}
}

func mustWorkerDeliveryResultCount(t *testing.T, mutation DeliveryMutationStatus, control DeliveryControlStatus, items int) WorkerDeliveryResultCount {
	t.Helper()
	result, err := newWorkerDeliveryResultCount(mutation, control, items)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func workerObserverSpec(t *testing.T, fixture workerObserverFixture, operation WorkerOperation, outcome WorkerOutcome) workerEventSpec {
	t.Helper()
	spec := workerEventSpec{Operation: operation, Outcome: outcome}
	if outcome == WorkerOutcomeFailed {
		spec.Failure = WorkerFailureRuntime
		if workerDriverOperation(operation) {
			spec.Failure = WorkerFailureDriver
		}
	}
	switch operation {
	case WorkerOperationRun:
		spec.Limit = fixture.plan.TotalConcurrency()
	case WorkerOperationDrain:
		spec.Limit = fixture.plan.TotalConcurrency()
		if outcome == WorkerOutcomeForced {
			spec.Active = 1
		}
	case WorkerOperationClaim:
		spec.Limit = fixture.plan.TotalConcurrency()
		if outcome == WorkerOutcomeComplete {
			spec.Items = 1
			spec.Bytes = 128
		}
		if outcome == WorkerOutcomeSaturated {
			spec.Active = 2
			spec.Limit = 2
		}
	case WorkerOperationRecover:
		spec.Limit = fixture.plan.TotalConcurrency()
		if outcome == WorkerOutcomeComplete {
			spec.Items = 1
			spec.Bytes = 128
		}
		if outcome == WorkerOutcomeSaturated {
			spec.Active = 2
			spec.Limit = 2
		}
	case WorkerOperationRenew:
		spec.Items = 1
		spec.Limit = fixture.plan.TotalConcurrency()
		if outcome == WorkerOutcomeComplete {
			spec.Results = []WorkerDeliveryResultCount{mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1)}
		}
	case WorkerOperationApply:
		spec.Definition = fixture.definition
		spec.Binding = fixture.binding
		spec.CommandKind = DeliveryCommandBeginAttempt
		spec.Items = 1
		spec.Limit = 2
		if outcome == WorkerOutcomeComplete {
			spec.Results = []WorkerDeliveryResultCount{mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1)}
		}
	case WorkerOperationAdmission:
		spec.Definition = fixture.definition
		spec.Binding = fixture.binding
		switch outcome {
		case WorkerOutcomeReady:
			spec.AdmissionSignal = AdmissionReady
			spec.Limit = 2
		case WorkerOutcomeHeld:
			spec.AdmissionSignal = AdmissionHeld
		case WorkerOutcomeStale:
			spec.AdmissionSignal = AdmissionStale
		case WorkerOutcomeInvalid:
			spec.AdmissionSignal = AdmissionInvalid
		case WorkerOutcomeSaturated:
			spec.AdmissionSignal = AdmissionReady
			spec.Active = 1
			spec.Limit = 1
		}
	}
	return spec
}

func workerObserverAllows(operation WorkerOperation, outcome WorkerOutcome) bool {
	switch operation {
	case WorkerOperationRun:
		return outcome == WorkerOutcomeStarted || outcome == WorkerOutcomeComplete || outcome == WorkerOutcomeCancelled || outcome == WorkerOutcomeFailed
	case WorkerOperationDrain:
		return outcome == WorkerOutcomeStarted || outcome == WorkerOutcomeComplete || outcome == WorkerOutcomeForced || outcome == WorkerOutcomeFailed
	case WorkerOperationClaim, WorkerOperationRecover:
		return outcome == WorkerOutcomeComplete || outcome == WorkerOutcomeEmpty || outcome == WorkerOutcomeSaturated || outcome == WorkerOutcomeTimedOut || outcome == WorkerOutcomeCancelled || outcome == WorkerOutcomeFailed
	case WorkerOperationRenew, WorkerOperationApply:
		return outcome == WorkerOutcomeComplete || outcome == WorkerOutcomeTimedOut || outcome == WorkerOutcomeCancelled || outcome == WorkerOutcomeFailed
	case WorkerOperationAdmission:
		return outcome == WorkerOutcomeReady || outcome == WorkerOutcomeHeld || outcome == WorkerOutcomeStale || outcome == WorkerOutcomeInvalid || outcome == WorkerOutcomeSaturated
	default:
		return false
	}
}

func TestWorkerObserverEnumsAreClosedAndTypeFirst(t *testing.T) {
	operations := []struct {
		value WorkerOperation
		name  string
	}{
		{WorkerOperationRun, "run"},
		{WorkerOperationDrain, "drain"},
		{WorkerOperationClaim, "claim"},
		{WorkerOperationRecover, "recover"},
		{WorkerOperationRenew, "renew"},
		{WorkerOperationApply, "apply"},
		{WorkerOperationAdmission, "admission"},
	}
	for _, test := range operations {
		if !test.value.Valid() || test.value.String() != test.name {
			t.Fatalf("operation %d = (%t, %q)", test.value, test.value.Valid(), test.value.String())
		}
	}
	if WorkerOperation(0).Valid() || WorkerOperation(255).Valid() || WorkerOperation(255).String() != "unknown" {
		t.Fatal("unknown worker operation was accepted")
	}
	outcomes := []struct {
		value WorkerOutcome
		name  string
	}{
		{WorkerOutcomeStarted, "started"},
		{WorkerOutcomeComplete, "complete"},
		{WorkerOutcomeEmpty, "empty"},
		{WorkerOutcomeReady, "ready"},
		{WorkerOutcomeHeld, "held"},
		{WorkerOutcomeStale, "stale"},
		{WorkerOutcomeInvalid, "invalid"},
		{WorkerOutcomeSaturated, "saturated"},
		{WorkerOutcomeTimedOut, "timed_out"},
		{WorkerOutcomeCancelled, "cancelled"},
		{WorkerOutcomeFailed, "failed"},
		{WorkerOutcomeForced, "forced"},
	}
	for _, test := range outcomes {
		if !test.value.Valid() || test.value.String() != test.name {
			t.Fatalf("outcome %d = (%t, %q)", test.value, test.value.Valid(), test.value.String())
		}
	}
	if WorkerOutcome(0).Valid() || WorkerOutcome(255).Valid() || WorkerOutcome(255).String() != "unknown" {
		t.Fatal("unknown worker outcome was accepted")
	}
	failures := []struct {
		value WorkerFailure
		name  string
	}{
		{WorkerFailureNone, "none"},
		{WorkerFailureDriver, "driver"},
		{WorkerFailureDriverContract, "driver_contract"},
		{WorkerFailureDriverPanic, "driver_panic"},
		{WorkerFailureRuntime, "runtime"},
	}
	for _, test := range failures {
		if !test.value.Valid() || test.value.String() != test.name {
			t.Fatalf("failure %d = (%t, %q)", test.value, test.value.Valid(), test.value.String())
		}
	}
	if WorkerFailure(255).Valid() || WorkerFailure(255).String() != "unknown" {
		t.Fatal("unknown worker failure was accepted")
	}
}

func TestWorkerEventAcceptsExactlyTheOperationOutcomeMatrix(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	for operation := WorkerOperationRun; operation <= WorkerOperationAdmission; operation++ {
		for outcome := WorkerOutcomeStarted; outcome <= WorkerOutcomeForced; outcome++ {
			spec := workerObserverSpec(t, fixture, operation, outcome)
			event, err := newWorkerEvent(fixture.plan, spec)
			if workerObserverAllows(operation, outcome) {
				if err != nil || !event.valid() {
					t.Fatalf("valid %s/%s = (%v, %v)", operation, outcome, event, err)
				}
				continue
			}
			if !errors.Is(err, ErrInvalid) || event.valid() {
				t.Fatalf("invalid %s/%s = (%v, %v)", operation, outcome, event, err)
			}
		}
	}
	for _, spec := range []workerEventSpec{
		workerObserverSpec(t, fixture, 0, WorkerOutcomeComplete),
		workerObserverSpec(t, fixture, WorkerOperation(255), WorkerOutcomeComplete),
		workerObserverSpec(t, fixture, WorkerOperationRun, 0),
		workerObserverSpec(t, fixture, WorkerOperationRun, WorkerOutcome(255)),
	} {
		if event, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) || event.valid() {
			t.Fatalf("unknown enum event = (%v, %v)", event, err)
		}
	}
}

func TestWorkerDeliveryResultCountAcceptsExactlySevenPairs(t *testing.T) {
	pairs := []struct {
		mutation DeliveryMutationStatus
		control  DeliveryControlStatus
	}{
		{DeliveryMutationApplied, DeliveryControlNone},
		{DeliveryMutationApplied, DeliveryControlCancelRequested},
		{DeliveryMutationApplied, DeliveryControlTerminated},
		{DeliveryMutationLeaseLost, DeliveryControlNone},
		{DeliveryMutationLeaseLost, DeliveryControlCancelRequested},
		{DeliveryMutationLeaseLost, DeliveryControlTerminated},
		{DeliveryMutationAmbiguous, DeliveryControlNone},
	}
	if len(pairs) != maxWorkerDeliveryResultCounts {
		t.Fatal("delivery result pair bound changed")
	}
	allowed := make(map[[2]uint8]struct{}, len(pairs))
	for _, pair := range pairs {
		allowed[[2]uint8{uint8(pair.mutation), uint8(pair.control)}] = struct{}{}
		result, err := newWorkerDeliveryResultCount(pair.mutation, pair.control, MaxClaimItems)
		if err != nil || !result.valid() || result.Mutation() != pair.mutation || result.Control() != pair.control || result.Items() != MaxClaimItems {
			t.Fatalf("result pair %s/%s = (%v, %v)", pair.mutation, pair.control, result, err)
		}
	}
	for mutation := DeliveryMutationStatus(0); mutation <= DeliveryMutationAmbiguous+1; mutation++ {
		for control := DeliveryControlStatus(0); control <= DeliveryControlTerminated+1; control++ {
			_, expected := allowed[[2]uint8{uint8(mutation), uint8(control)}]
			result, err := newWorkerDeliveryResultCount(mutation, control, 1)
			if expected && (err != nil || !result.valid()) || !expected && (!errors.Is(err, ErrInvalid) || result.valid()) {
				t.Fatalf("pair %d/%d = (%v, %v), allowed=%t", mutation, control, result, err, expected)
			}
		}
	}
	invalid := []struct {
		mutation DeliveryMutationStatus
		control  DeliveryControlStatus
		items    int
		want     error
	}{
		{0, DeliveryControlNone, 1, ErrInvalid},
		{DeliveryMutationStatus(255), DeliveryControlNone, 1, ErrInvalid},
		{DeliveryMutationApplied, DeliveryControlStatus(255), 1, ErrInvalid},
		{DeliveryMutationAmbiguous, DeliveryControlCancelRequested, 1, ErrInvalid},
		{DeliveryMutationAmbiguous, DeliveryControlTerminated, 1, ErrInvalid},
		{DeliveryMutationApplied, DeliveryControlNone, 0, ErrInvalid},
		{DeliveryMutationApplied, DeliveryControlNone, -1, ErrInvalid},
		{DeliveryMutationApplied, DeliveryControlNone, MaxClaimItems + 1, ErrTooLarge},
	}
	for _, test := range invalid {
		if result, err := newWorkerDeliveryResultCount(test.mutation, test.control, test.items); !errors.Is(err, test.want) || result.valid() {
			t.Fatalf("invalid result count = (%v, %v)", result, err)
		}
	}
}

func TestWorkerEventCanonicalizesAndClonesRenewResults(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	input := []WorkerDeliveryResultCount{
		mustWorkerDeliveryResultCount(t, DeliveryMutationAmbiguous, DeliveryControlNone, 1),
		mustWorkerDeliveryResultCount(t, DeliveryMutationLeaseLost, DeliveryControlTerminated, 1),
		mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlCancelRequested, 1),
	}
	spec := workerObserverSpec(t, fixture, WorkerOperationRenew, WorkerOutcomeComplete)
	spec.Items = 3
	spec.Results = input
	event, err := newWorkerEvent(fixture.plan, spec)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = WorkerDeliveryResultCount{}
	results := event.Results()
	if len(results) != 3 || results[0].Mutation() != DeliveryMutationApplied || results[1].Mutation() != DeliveryMutationLeaseLost || results[2].Mutation() != DeliveryMutationAmbiguous {
		t.Fatalf("canonical results = %#v", results)
	}
	results[0] = WorkerDeliveryResultCount{}
	if event.Results()[0].Mutation() != DeliveryMutationApplied {
		t.Fatal("worker event result getter exposed internal storage")
	}

	duplicate := spec
	duplicate.Items = 2
	duplicate.Results = []WorkerDeliveryResultCount{
		mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1),
		mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1),
	}
	if _, err := newWorkerEvent(fixture.plan, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate results = %v", err)
	}
	overBound := spec
	overBound.Results = make([]WorkerDeliveryResultCount, maxWorkerDeliveryResultCounts+1)
	if _, err := newWorkerEvent(fixture.plan, overBound); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized results = %v", err)
	}
	zero := spec
	zero.Results = []WorkerDeliveryResultCount{{}}
	if _, err := newWorkerEvent(fixture.plan, zero); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero result = %v", err)
	}
	mismatch := spec
	mismatch.Items = MaxClaimItems
	mismatch.Results = []WorkerDeliveryResultCount{
		mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 200),
		mustWorkerDeliveryResultCount(t, DeliveryMutationLeaseLost, DeliveryControlNone, 100),
	}
	if _, err := newWorkerEvent(fixture.plan, mismatch); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overflow-safe result sum = %v", err)
	}
}

func TestWorkerEventEnforcesRenewAndApplyResultRules(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	terminated := mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlTerminated, 1)
	renew := workerObserverSpec(t, fixture, WorkerOperationRenew, WorkerOutcomeComplete)
	renew.Results = []WorkerDeliveryResultCount{terminated}
	if _, err := newWorkerEvent(fixture.plan, renew); !errors.Is(err, ErrInvalid) {
		t.Fatalf("renew applied termination = %v", err)
	}
	for command := DeliveryCommandBeginAttempt; command <= DeliveryCommandRevokeAttempt; command++ {
		apply := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
		apply.CommandKind = command
		apply.Results = []WorkerDeliveryResultCount{terminated}
		_, err := newWorkerEvent(fixture.plan, apply)
		allowed := command == DeliveryCommandArbitrateAttemptDeadline || command == DeliveryCommandRevokeAttempt
		if allowed && err != nil || !allowed && !errors.Is(err, ErrInvalid) {
			t.Fatalf("apply terminated command %s = %v", command, err)
		}
	}
	cancelRequested := mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlCancelRequested, 1)
	for command := DeliveryCommandBeginAttempt; command <= DeliveryCommandRevokeAttempt; command++ {
		apply := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
		apply.CommandKind = command
		apply.Results = []WorkerDeliveryResultCount{cancelRequested}
		_, err := newWorkerEvent(fixture.plan, apply)
		allowed := command == DeliveryCommandProgress || command == DeliveryCommandFinishAttempt || command == DeliveryCommandArbitrateAttemptDeadline
		if allowed && err != nil || !allowed && !errors.Is(err, ErrInvalid) {
			t.Fatalf("apply cancel-requested command %s = %v", command, err)
		}
	}
	for _, operation := range []WorkerOperation{WorkerOperationRenew, WorkerOperationApply} {
		for _, outcome := range []WorkerOutcome{WorkerOutcomeTimedOut, WorkerOutcomeCancelled, WorkerOutcomeFailed} {
			spec := workerObserverSpec(t, fixture, operation, outcome)
			spec.Results = []WorkerDeliveryResultCount{mustWorkerDeliveryResultCount(t, DeliveryMutationLeaseLost, DeliveryControlNone, 1)}
			if _, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) {
				t.Fatalf("uncertain %s/%s results = %v", operation, outcome, err)
			}
		}
	}
	apply := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
	apply.Results = nil
	if _, err := newWorkerEvent(fixture.plan, apply); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply without result = %v", err)
	}
	apply = workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
	apply.Results = []WorkerDeliveryResultCount{
		mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1),
		mustWorkerDeliveryResultCount(t, DeliveryMutationLeaseLost, DeliveryControlNone, 1),
	}
	if _, err := newWorkerEvent(fixture.plan, apply); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply with multiple results = %v", err)
	}
}

func TestWorkerEventTracksRecoverCountsWithoutConflation(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	recovered := workerObserverSpec(t, fixture, WorkerOperationRecover, WorkerOutcomeComplete)
	recovered.Items = 2
	recovered.Bytes = 256
	recovered.Released = 3
	recovered.More = true
	event, err := newWorkerEvent(fixture.plan, recovered)
	if err != nil || event.Items() != 2 || event.Released() != 3 || event.Bytes() != 256 || !event.More() {
		t.Fatalf("recover counts = (%v, %v)", event, err)
	}
	releasedOnly := workerObserverSpec(t, fixture, WorkerOperationRecover, WorkerOutcomeComplete)
	releasedOnly.Items = 0
	releasedOnly.Bytes = 0
	releasedOnly.Released = 1
	if _, err := newWorkerEvent(fixture.plan, releasedOnly); err != nil {
		t.Fatalf("released-only recovery = %v", err)
	}
	maximumReleased := releasedOnly
	maximumReleased.Released = MaxReclaimBatch
	maximumReleased.More = true
	if _, err := newWorkerEvent(fixture.plan, maximumReleased); err != nil {
		t.Fatalf("maximum released recovery = %v", err)
	}
	empty := workerObserverSpec(t, fixture, WorkerOperationRecover, WorkerOutcomeEmpty)
	if _, err := newWorkerEvent(fixture.plan, empty); err != nil {
		t.Fatalf("empty recovery = %v", err)
	}
	invalid := []workerEventSpec{
		func() workerEventSpec { value := empty; value.More = true; return value }(),
		func() workerEventSpec { value := recovered; value.Outcome = WorkerOutcomeEmpty; return value }(),
		func() workerEventSpec { value := recovered; value.Items = 1; value.Bytes = 0; return value }(),
		func() workerEventSpec { value := recovered; value.Items = 0; value.Bytes = 1; return value }(),
		func() workerEventSpec {
			value := recovered
			value.Items = 0
			value.Released = 0
			value.Bytes = 0
			value.More = false
			return value
		}(),
		func() workerEventSpec {
			value := recovered
			value.Items = 1
			value.Released = MaxReclaimBatch
			value.Bytes = 1
			return value
		}(),
		func() workerEventSpec {
			value := recovered
			value.Outcome = WorkerOutcomeFailed
			value.Failure = WorkerFailureDriver
			return value
		}(),
	}
	for _, spec := range invalid {
		if _, err := newWorkerEvent(fixture.plan, spec); err == nil {
			t.Fatalf("invalid recovery accepted: %#v", spec)
		}
	}
}

func TestWorkerEventEnforcesAggregateAndExactScopes(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	for _, operation := range []WorkerOperation{WorkerOperationRun, WorkerOperationDrain, WorkerOperationClaim, WorkerOperationRecover, WorkerOperationRenew} {
		outcome := WorkerOutcomeComplete
		if operation == WorkerOperationRun || operation == WorkerOperationDrain {
			outcome = WorkerOutcomeStarted
		}
		spec := workerObserverSpec(t, fixture, operation, outcome)
		spec.Definition = fixture.definition
		spec.Binding = fixture.binding
		if _, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("non-aggregate %s = %v", operation, err)
		}
	}
	for _, operation := range []WorkerOperation{WorkerOperationApply, WorkerOperationAdmission} {
		outcome := WorkerOutcomeComplete
		if operation == WorkerOperationAdmission {
			outcome = WorkerOutcomeReady
		}
		spec := workerObserverSpec(t, fixture, operation, outcome)
		spec.Definition = Name{}
		spec.Binding = BindingName{}
		if _, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("aggregate %s = %v", operation, err)
		}
	}
	foreign := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
	foreign.Definition = testJobName(t, "workers.observer.foreign")
	if _, err := newWorkerEvent(fixture.plan, foreign); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreign definition = %v", err)
	}
	partial := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
	partial.Binding = BindingName{}
	if _, err := newWorkerEvent(fixture.plan, partial); !errors.Is(err, ErrInvalid) {
		t.Fatalf("partial scope = %v", err)
	}
	oversized := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
	oversized.Definition = Name{value: strings.Repeat("a", MaxNameBytes+1)}
	if _, err := newWorkerEvent(fixture.plan, oversized); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized scope = %v", err)
	}
}

func TestWorkerEventFindsWiredAutomaticBindingInSortedPlan(t *testing.T) {
	handler := Handler[string](func(context.Context, string) error { return nil })
	automatic := Auto(handler)
	MustWire(automatic, WireSpec[string]{Name: testJobName(t, "workers.observer.middle"), Codec: String(1)})
	alpha := testQueueDefinition(t, "workers.observer.alpha", String(1))
	zulu := testQueueDefinition(t, "workers.observer.zulu", String(1))
	plan := MustWorkerPlan(
		MustCatalog(zulu, automatic, alpha),
		On(zulu, handler, Binding("workers.observer.zulu"), Concurrency(1)),
		automatic,
		On(alpha, handler, Binding("workers.observer.alpha"), Concurrency(1)),
	)
	var description WorkerBindingDescription
	for _, candidate := range plan.Describe().Bindings {
		if candidate.Definition == automatic.Name() {
			description = candidate
		}
	}
	spec := workerEventSpec{
		Operation:   WorkerOperationApply,
		Outcome:     WorkerOutcomeComplete,
		Definition:  description.Definition,
		Binding:     description.Binding,
		CommandKind: DeliveryCommandBeginAttempt,
		Results:     []WorkerDeliveryResultCount{mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1)},
		Items:       1,
		Limit:       description.Concurrency,
	}
	if event, err := newWorkerEvent(plan, spec); err != nil || event.Definition() != automatic.Name() || event.Binding() != description.Binding {
		t.Fatalf("automatic binding event = (%v, %v)", event, err)
	}
}

func TestWorkerEventEnforcesClaimEvidenceAndPlanAvailability(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	valid := workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeComplete)
	if _, err := newWorkerEvent(fixture.plan, valid); err != nil {
		t.Fatal(err)
	}
	invalid := []workerEventSpec{
		func() workerEventSpec { value := valid; value.Bytes = 0; return value }(),
		func() workerEventSpec { value := valid; value.Items = 0; return value }(),
		func() workerEventSpec {
			value := valid
			value.Items = fixture.plan.TotalConcurrency() + 1
			return value
		}(),
		func() workerEventSpec {
			value := workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeEmpty)
			value.Bytes = 1
			return value
		}(),
		func() workerEventSpec {
			value := workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeFailed)
			value.Items = 1
			value.Bytes = 1
			return value
		}(),
	}
	for _, spec := range invalid {
		if _, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid claim = %v", err)
		}
	}
}

func TestWorkerEventRejectsFieldsOwnedByOtherOperations(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	base := workerObserverSpec(t, fixture, WorkerOperationRun, WorkerOutcomeComplete)
	invalid := []workerEventSpec{
		func() workerEventSpec { value := base; value.CommandKind = DeliveryCommandBeginAttempt; return value }(),
		func() workerEventSpec { value := base; value.AdmissionSignal = AdmissionReady; return value }(),
		func() workerEventSpec {
			value := base
			value.Results = []WorkerDeliveryResultCount{mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1)}
			return value
		}(),
		func() workerEventSpec { value := base; value.Released = 1; return value }(),
		func() workerEventSpec { value := base; value.More = true; return value }(),
		func() workerEventSpec { value := base; value.Bytes = 1; return value }(),
		func() workerEventSpec {
			value := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
			value.Bytes = 1
			return value
		}(),
		func() workerEventSpec {
			value := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
			value.Active = fixture.plan.TotalConcurrency() + 1
			return value
		}(),
	}
	for _, spec := range invalid {
		if _, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("foreign operation field = %v", err)
		}
	}
}

func TestWorkerEventValidatesFailuresAdmissionAndElapsed(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	for _, failure := range []WorkerFailure{WorkerFailureDriver, WorkerFailureDriverContract, WorkerFailureDriverPanic, WorkerFailureRuntime} {
		for _, operation := range []WorkerOperation{WorkerOperationClaim, WorkerOperationRecover, WorkerOperationRenew, WorkerOperationApply} {
			spec := workerObserverSpec(t, fixture, operation, WorkerOutcomeFailed)
			spec.Failure = failure
			if _, err := newWorkerEvent(fixture.plan, spec); err != nil {
				t.Fatalf("%s/%s failure = %v", operation, failure, err)
			}
		}
	}
	for _, operation := range []WorkerOperation{WorkerOperationRun, WorkerOperationDrain} {
		spec := workerObserverSpec(t, fixture, operation, WorkerOutcomeFailed)
		spec.Failure = WorkerFailureDriver
		if _, err := newWorkerEvent(fixture.plan, spec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("lifecycle driver failure = %v", err)
		}
	}
	missing := workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeFailed)
	missing.Failure = WorkerFailureNone
	if _, err := newWorkerEvent(fixture.plan, missing); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing failure = %v", err)
	}
	unexpected := workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeEmpty)
	unexpected.Failure = WorkerFailureDriver
	if _, err := newWorkerEvent(fixture.plan, unexpected); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unexpected failure = %v", err)
	}

	admissions := []struct {
		signal  AdmissionSignal
		outcome WorkerOutcome
		active  int
		limit   int
	}{
		{AdmissionUninitialized, WorkerOutcomeInvalid, 1, 0},
		{AdmissionReady, WorkerOutcomeReady, 0, 2},
		{AdmissionReady, WorkerOutcomeSaturated, 1, 1},
		{AdmissionHeld, WorkerOutcomeHeld, 1, 0},
		{AdmissionStale, WorkerOutcomeStale, 1, 0},
		{AdmissionInvalid, WorkerOutcomeInvalid, 1, 0},
	}
	for _, test := range admissions {
		spec := workerObserverSpec(t, fixture, WorkerOperationAdmission, test.outcome)
		spec.AdmissionSignal = test.signal
		spec.Active = test.active
		spec.Limit = test.limit
		if _, err := newWorkerEvent(fixture.plan, spec); err != nil {
			t.Fatalf("admission %s/%s = %v", test.signal, test.outcome, err)
		}
	}
	invalidAdmission := workerObserverSpec(t, fixture, WorkerOperationAdmission, WorkerOutcomeReady)
	invalidAdmission.AdmissionSignal = AdmissionSignal(255)
	if _, err := newWorkerEvent(fixture.plan, invalidAdmission); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid admission = %v", err)
	}

	long := workerObserverSpec(t, fixture, WorkerOperationRun, WorkerOutcomeComplete)
	long.Elapsed = time.Duration(1<<63 - 1)
	event, err := newWorkerEvent(fixture.plan, long)
	if err != nil || event.Elapsed() != long.Elapsed {
		t.Fatalf("long elapsed = (%v, %v)", event.Elapsed(), err)
	}
	negative := long
	negative.Elapsed = -1
	if _, err := newWorkerEvent(fixture.plan, negative); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative elapsed = %v", err)
	}
	started := workerObserverSpec(t, fixture, WorkerOperationRun, WorkerOutcomeStarted)
	started.Elapsed = time.Nanosecond
	if _, err := newWorkerEvent(fixture.plan, started); !errors.Is(err, ErrInvalid) {
		t.Fatalf("started elapsed = %v", err)
	}
	admission := workerObserverSpec(t, fixture, WorkerOperationAdmission, WorkerOutcomeReady)
	admission.Elapsed = time.Nanosecond
	if _, err := newWorkerEvent(fixture.plan, admission); !errors.Is(err, ErrInvalid) {
		t.Fatalf("admission elapsed = %v", err)
	}
}

func TestWorkerEventSurfaceIsImmutableMetricSafeAndRedacted(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	spec := workerObserverSpec(t, fixture, WorkerOperationApply, WorkerOutcomeComplete)
	spec.Active = 1
	spec.Elapsed = 25 * time.Millisecond
	event, err := newWorkerEvent(fixture.plan, spec)
	if err != nil {
		t.Fatal(err)
	}
	if event.Operation() != spec.Operation || event.Outcome() != spec.Outcome || event.Failure() != spec.Failure || event.Definition() != spec.Definition || event.Binding() != spec.Binding || event.CommandKind() != spec.CommandKind || event.AdmissionSignal() != spec.AdmissionSignal || event.Items() != spec.Items || event.Released() != spec.Released || event.Bytes() != spec.Bytes || event.Active() != spec.Active || event.Limit() != spec.Limit || event.More() != spec.More || event.Elapsed() != spec.Elapsed || !reflect.DeepEqual(event.Results(), spec.Results) {
		t.Fatalf("worker event getters = %#v", event)
	}
	eventType := reflect.TypeFor[WorkerEvent]()
	for index := 0; index < eventType.NumField(); index++ {
		field := eventType.Field(index)
		if field.PkgPath == "" {
			t.Fatalf("worker event field %q is externally mutable", field.Name)
		}
		if field.Type.Implements(reflect.TypeFor[error]()) {
			t.Fatalf("worker event field %q exposes an error", field.Name)
		}
	}
	countType := reflect.TypeFor[WorkerDeliveryResultCount]()
	for index := 0; index < countType.NumField(); index++ {
		if countType.Field(index).PkgPath == "" {
			t.Fatalf("delivery result field %q is externally mutable", countType.Field(index).Name)
		}
	}
	allowed := map[string]reflect.Type{
		"Operation":       reflect.TypeFor[WorkerOperation](),
		"Outcome":         reflect.TypeFor[WorkerOutcome](),
		"Failure":         reflect.TypeFor[WorkerFailure](),
		"Definition":      reflect.TypeFor[Name](),
		"Binding":         reflect.TypeFor[BindingName](),
		"CommandKind":     reflect.TypeFor[DeliveryCommandKind](),
		"AdmissionSignal": reflect.TypeFor[AdmissionSignal](),
		"Results":         reflect.TypeFor[[]WorkerDeliveryResultCount](),
		"Items":           reflect.TypeFor[int](),
		"Released":        reflect.TypeFor[int](),
		"Bytes":           reflect.TypeFor[int](),
		"Active":          reflect.TypeFor[int](),
		"Limit":           reflect.TypeFor[int](),
		"More":            reflect.TypeFor[bool](),
		"Elapsed":         reflect.TypeFor[time.Duration](),
	}
	specType := reflect.TypeFor[workerEventSpec]()
	if specType.NumField() != len(allowed) {
		t.Fatalf("worker event spec fields = %d, want %d", specType.NumField(), len(allowed))
	}
	for index := 0; index < specType.NumField(); index++ {
		field := specType.Field(index)
		if want, ok := allowed[field.Name]; !ok || field.Type != want {
			t.Fatalf("worker event spec exposes unsafe field %s %s", field.Name, field.Type)
		}
	}
	result := mustWorkerDeliveryResultCount(t, DeliveryMutationApplied, DeliveryControlNone, 1)
	secrets := []string{fixture.definition.Value(), fixture.binding.Value(), "private driver error", "private panic value"}
	for _, value := range []any{spec, event, result} {
		for _, rendered := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			for _, secret := range secrets {
				if strings.Contains(rendered, secret) {
					t.Fatalf("formatted %T leaked %q in %q", value, secret, rendered)
				}
			}
		}
		if _, marshalErr := json.Marshal(value); !errors.Is(marshalErr, ErrUnsupported) {
			t.Fatalf("worker observer JSON %T = %v", value, marshalErr)
		}
	}
	for _, rendered := range []string{event.LogValue().String(), result.LogValue().String(), slog.AnyValue(event).Resolve().String()} {
		for _, secret := range secrets {
			if strings.Contains(rendered, secret) {
				t.Fatalf("worker observer log leaked %q in %q", secret, rendered)
			}
		}
	}
}

type workerObserverRecorder struct {
	mu     sync.Mutex
	events []WorkerEvent
}

func (recorder *workerObserverRecorder) Observe(_ context.Context, event WorkerEvent) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

func (recorder *workerObserverRecorder) count() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.events)
}

type workerObserverNilContext struct{}

func (*workerObserverNilContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*workerObserverNilContext) Done() <-chan struct{}       { return nil }
func (*workerObserverNilContext) Err() error                  { return nil }
func (*workerObserverNilContext) Value(any) any               { return nil }

func TestSafeObserveIsSynchronousNilSafeAndPanicContained(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	event, err := newWorkerEvent(fixture.plan, workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeEmpty))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	observer := WorkerObserverFunc(func(context.Context, WorkerEvent) { calls.Add(1) })
	safeObserve(observer, context.Background(), event)
	if calls.Load() != 1 {
		t.Fatal("observer did not run synchronously")
	}
	var nilPointer *workerObserverRecorder
	var nilFunction WorkerObserverFunc
	var nilContext *workerObserverNilContext
	safeObserve(nil, context.Background(), event)
	safeObserve(nilPointer, context.Background(), event)
	safeObserve(nilFunction, context.Background(), event)
	safeObserve(observer, nil, event)
	safeObserve(observer, nilContext, event)
	safeObserve(observer, context.Background(), WorkerEvent{})
	if calls.Load() != 1 {
		t.Fatal("nil or invalid observer input was invoked")
	}
	safeObserve(WorkerObserverFunc(func(context.Context, WorkerEvent) { panic("private observer panic") }), context.Background(), event)
	safeObserve(observer, context.Background(), event)
	if calls.Load() != 2 {
		t.Fatal("observer panic escaped or poisoned later observation")
	}
}

func TestSafeObserveSupportsConcurrentCallers(t *testing.T) {
	fixture := newWorkerObserverFixture(t)
	event, err := newWorkerEvent(fixture.plan, workerObserverSpec(t, fixture, WorkerOperationClaim, WorkerOutcomeEmpty))
	if err != nil {
		t.Fatal(err)
	}
	recorder := &workerObserverRecorder{}
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			safeObserve(recorder, context.Background(), event)
		}()
	}
	wait.Wait()
	if recorder.count() != 64 {
		t.Fatalf("concurrent observer count = %d", recorder.count())
	}
}
