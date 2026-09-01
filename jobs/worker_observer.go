package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

const maxWorkerDeliveryResultCounts = 7

type WorkerOperation uint8

const (
	WorkerOperationRun WorkerOperation = iota + 1
	WorkerOperationDrain
	WorkerOperationClaim
	WorkerOperationRecover
	WorkerOperationRenew
	WorkerOperationApply
	WorkerOperationAdmission
)

func (operation WorkerOperation) Valid() bool {
	return operation >= WorkerOperationRun && operation <= WorkerOperationAdmission
}

func (operation WorkerOperation) String() string {
	switch operation {
	case WorkerOperationRun:
		return "run"
	case WorkerOperationDrain:
		return "drain"
	case WorkerOperationClaim:
		return "claim"
	case WorkerOperationRecover:
		return "recover"
	case WorkerOperationRenew:
		return "renew"
	case WorkerOperationApply:
		return "apply"
	case WorkerOperationAdmission:
		return "admission"
	default:
		return "unknown"
	}
}

type WorkerOutcome uint8

const (
	WorkerOutcomeStarted WorkerOutcome = iota + 1
	WorkerOutcomeComplete
	WorkerOutcomeEmpty
	WorkerOutcomeReady
	WorkerOutcomeHeld
	WorkerOutcomeStale
	WorkerOutcomeInvalid
	WorkerOutcomeSaturated
	WorkerOutcomeTimedOut
	WorkerOutcomeCancelled
	WorkerOutcomeFailed
	WorkerOutcomeForced
)

func (outcome WorkerOutcome) Valid() bool {
	return outcome >= WorkerOutcomeStarted && outcome <= WorkerOutcomeForced
}

func (outcome WorkerOutcome) String() string {
	switch outcome {
	case WorkerOutcomeStarted:
		return "started"
	case WorkerOutcomeComplete:
		return "complete"
	case WorkerOutcomeEmpty:
		return "empty"
	case WorkerOutcomeReady:
		return "ready"
	case WorkerOutcomeHeld:
		return "held"
	case WorkerOutcomeStale:
		return "stale"
	case WorkerOutcomeInvalid:
		return "invalid"
	case WorkerOutcomeSaturated:
		return "saturated"
	case WorkerOutcomeTimedOut:
		return "timed_out"
	case WorkerOutcomeCancelled:
		return "cancelled"
	case WorkerOutcomeFailed:
		return "failed"
	case WorkerOutcomeForced:
		return "forced"
	default:
		return "unknown"
	}
}

type WorkerFailure uint8

const (
	WorkerFailureNone WorkerFailure = iota
	WorkerFailureDriver
	WorkerFailureDriverContract
	WorkerFailureDriverPanic
	WorkerFailureRuntime
)

func (failure WorkerFailure) Valid() bool {
	return failure <= WorkerFailureRuntime
}

func (failure WorkerFailure) String() string {
	switch failure {
	case WorkerFailureNone:
		return "none"
	case WorkerFailureDriver:
		return "driver"
	case WorkerFailureDriverContract:
		return "driver_contract"
	case WorkerFailureDriverPanic:
		return "driver_panic"
	case WorkerFailureRuntime:
		return "runtime"
	default:
		return "unknown"
	}
}

type WorkerDeliveryResultCount struct {
	mutation DeliveryMutationStatus
	control  DeliveryControlStatus
	items    int
}

func newWorkerDeliveryResultCount(mutation DeliveryMutationStatus, control DeliveryControlStatus, items int) (WorkerDeliveryResultCount, error) {
	result := WorkerDeliveryResultCount{mutation: mutation, control: control, items: items}
	if items > MaxClaimItems {
		return WorkerDeliveryResultCount{}, tooLarge("worker delivery result count")
	}
	if !result.valid() {
		return WorkerDeliveryResultCount{}, invalid("worker delivery result count")
	}
	return result, nil
}

func (result WorkerDeliveryResultCount) Mutation() DeliveryMutationStatus { return result.mutation }
func (result WorkerDeliveryResultCount) Control() DeliveryControlStatus   { return result.control }
func (result WorkerDeliveryResultCount) Items() int                       { return result.items }
func (WorkerDeliveryResultCount) String() string {
	return "[job worker delivery result count]"
}
func (result WorkerDeliveryResultCount) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, result.String())
}
func (result WorkerDeliveryResultCount) LogValue() slog.Value {
	return slog.StringValue(result.String())
}
func (WorkerDeliveryResultCount) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: worker delivery result count cannot be serialized", ErrUnsupported)
}
func (result WorkerDeliveryResultCount) valid() bool {
	return validWorkerDeliveryResultPair(result.mutation, result.control) && result.items > 0 && result.items <= MaxClaimItems
}

func validWorkerDeliveryResultPair(mutation DeliveryMutationStatus, control DeliveryControlStatus) bool {
	switch mutation {
	case DeliveryMutationApplied, DeliveryMutationLeaseLost:
		return control == DeliveryControlNone || control == DeliveryControlCancelRequested || control == DeliveryControlTerminated
	case DeliveryMutationAmbiguous:
		return control == DeliveryControlNone
	default:
		return false
	}
}

type workerEventSpec struct {
	Operation       WorkerOperation
	Outcome         WorkerOutcome
	Failure         WorkerFailure
	Definition      Name
	Binding         BindingName
	AdmissionGroup  WorkerAdmissionGroup
	CommandKind     DeliveryCommandKind
	AdmissionSignal AdmissionSignal
	Results         []WorkerDeliveryResultCount
	Items           int
	Released        int
	Bytes           int
	Active          int
	Limit           int
	More            bool
	Elapsed         time.Duration
}

func (workerEventSpec) String() string { return "[job worker event spec]" }
func (spec workerEventSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, spec.String())
}
func (spec workerEventSpec) LogValue() slog.Value { return slog.StringValue(spec.String()) }
func (workerEventSpec) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: worker event spec cannot be serialized", ErrUnsupported)
}

type WorkerEvent struct {
	operation       WorkerOperation
	outcome         WorkerOutcome
	failure         WorkerFailure
	definition      Name
	binding         BindingName
	admissionGroup  WorkerAdmissionGroup
	commandKind     DeliveryCommandKind
	admissionSignal AdmissionSignal
	results         []WorkerDeliveryResultCount
	items           int
	released        int
	bytes           int
	active          int
	limit           int
	more            bool
	elapsed         time.Duration
	validated       bool
}

func newWorkerEvent(plan WorkerPlan, spec workerEventSpec) (WorkerEvent, error) {
	results, err := canonicalWorkerDeliveryResults(spec.Results)
	if err != nil {
		return WorkerEvent{}, err
	}
	spec.Results = results
	if err := validateWorkerEventSpec(plan, spec); err != nil {
		return WorkerEvent{}, err
	}
	return WorkerEvent{
		operation:       spec.Operation,
		outcome:         spec.Outcome,
		failure:         spec.Failure,
		definition:      spec.Definition,
		binding:         spec.Binding,
		admissionGroup:  spec.AdmissionGroup,
		commandKind:     spec.CommandKind,
		admissionSignal: spec.AdmissionSignal,
		results:         results,
		items:           spec.Items,
		released:        spec.Released,
		bytes:           spec.Bytes,
		active:          spec.Active,
		limit:           spec.Limit,
		more:            spec.More,
		elapsed:         spec.Elapsed,
		validated:       true,
	}, nil
}

func (event WorkerEvent) Operation() WorkerOperation { return event.operation }
func (event WorkerEvent) Outcome() WorkerOutcome     { return event.outcome }
func (event WorkerEvent) Failure() WorkerFailure     { return event.failure }
func (event WorkerEvent) Definition() Name           { return event.definition }
func (event WorkerEvent) Binding() BindingName       { return event.binding }
func (event WorkerEvent) AdmissionGroup() WorkerAdmissionGroup {
	return event.admissionGroup
}
func (event WorkerEvent) CommandKind() DeliveryCommandKind {
	return event.commandKind
}
func (event WorkerEvent) AdmissionSignal() AdmissionSignal { return event.admissionSignal }
func (event WorkerEvent) Results() []WorkerDeliveryResultCount {
	return append([]WorkerDeliveryResultCount(nil), event.results...)
}
func (event WorkerEvent) Items() int             { return event.items }
func (event WorkerEvent) Released() int          { return event.released }
func (event WorkerEvent) Bytes() int             { return event.bytes }
func (event WorkerEvent) Active() int            { return event.active }
func (event WorkerEvent) Limit() int             { return event.limit }
func (event WorkerEvent) More() bool             { return event.more }
func (event WorkerEvent) Elapsed() time.Duration { return event.elapsed }
func (event WorkerEvent) String() string {
	return fmt.Sprintf("[job worker event operation=%s outcome=%s]", event.operation, event.outcome)
}
func (event WorkerEvent) LogValue() slog.Value { return slog.StringValue(event.String()) }
func (WorkerEvent) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: worker event cannot be serialized", ErrUnsupported)
}
func (event WorkerEvent) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, event.String()) }
func (event WorkerEvent) valid() bool {
	return event.validated && event.operation.Valid() && event.outcome.Valid() && event.failure.Valid()
}

type WorkerObserver interface {
	Observe(context.Context, WorkerEvent)
}

type WorkerObserverFunc func(context.Context, WorkerEvent)

func (observe WorkerObserverFunc) Observe(ctx context.Context, event WorkerEvent) {
	observe(ctx, event)
}

func safeObserve(observer WorkerObserver, ctx context.Context, event WorkerEvent) {
	if nilInterface(observer) || nilInterface(ctx) || !event.valid() {
		return
	}
	defer func() { _ = recover() }()
	observer.Observe(ctx, event)
}

func canonicalWorkerDeliveryResults(values []WorkerDeliveryResultCount) ([]WorkerDeliveryResultCount, error) {
	if len(values) > maxWorkerDeliveryResultCounts {
		return nil, tooLarge("worker delivery result counts")
	}
	results := append([]WorkerDeliveryResultCount(nil), values...)
	for _, result := range results {
		if !result.valid() {
			return nil, invalid("worker delivery result count")
		}
	}
	sort.Slice(results, func(left, right int) bool {
		if results[left].mutation != results[right].mutation {
			return results[left].mutation < results[right].mutation
		}
		return results[left].control < results[right].control
	})
	for index := 1; index < len(results); index++ {
		if results[index-1].mutation == results[index].mutation && results[index-1].control == results[index].control {
			return nil, fmt.Errorf("%w: duplicate worker delivery result count", ErrConflict)
		}
	}
	return results, nil
}

func validateWorkerEventSpec(plan WorkerPlan, spec workerEventSpec) error {
	if !validWorkerEventPlan(plan) || !spec.Operation.Valid() || !spec.Outcome.Valid() || !spec.Failure.Valid() {
		return invalid("worker event operation, outcome, failure, or plan")
	}
	if spec.Items < 0 || spec.Released < 0 || spec.Bytes < 0 || spec.Active < 0 || spec.Limit < 0 || spec.Elapsed < 0 {
		return invalid("worker event metrics")
	}
	if spec.Items > MaxWorkerConcurrency || spec.Released > MaxReclaimBatch || spec.Bytes > MaxWorkerInFlightBytes || spec.Active > MaxWorkerConcurrency || spec.Limit > MaxWorkerConcurrency {
		return tooLarge("worker event metrics")
	}
	if !validWorkerOperationOutcome(spec.Operation, spec.Outcome) {
		return invalid("worker event operation outcome")
	}
	if (spec.Outcome == WorkerOutcomeFailed) != (spec.Failure != WorkerFailureNone) {
		return invalid("worker event failure")
	}
	if spec.Failure != WorkerFailureNone && spec.Failure != WorkerFailureRuntime && !workerDriverOperation(spec.Operation) {
		return invalid("worker event driver failure")
	}
	concurrency := 0
	exact := false
	if spec.Operation == WorkerOperationAdmission {
		var err error
		concurrency, err = workerAdmissionEventScope(plan, spec.Definition, spec.Binding, spec.AdmissionGroup)
		if err != nil {
			return err
		}
	} else {
		if !spec.AdmissionGroup.IsZero() {
			return invalid("worker event admission group")
		}
		var err error
		concurrency, exact, err = workerEventScope(plan, spec.Definition, spec.Binding)
		if err != nil {
			return err
		}
	}
	if spec.Active > concurrency || spec.Limit > concurrency {
		return invalid("worker event scope metrics")
	}
	if err := validateWorkerEventIdentity(spec, exact); err != nil {
		return err
	}
	return validateWorkerEventOperationFields(spec, concurrency)
}

func workerAdmissionEventScope(plan WorkerPlan, definition Name, binding BindingName, group WorkerAdmissionGroup) (int, error) {
	if !definition.IsZero() || !binding.IsZero() || !group.valid() {
		return 0, invalid("worker admission event scope")
	}
	concurrency := 0
	for _, planned := range plan.bindings {
		if planned.admissionGroup == group {
			concurrency += planned.concurrency
		}
	}
	if concurrency == 0 {
		return 0, invalid("worker admission group is not in the plan")
	}
	return concurrency, nil
}

func validWorkerEventPlan(plan WorkerPlan) bool {
	return len(plan.bindings) > 0 && len(plan.bindings) == len(plan.descriptions) && len(plan.bindings) <= MaxDefinitions && plan.totalConcurrency > 0 && plan.totalConcurrency <= MaxWorkerConcurrency && plan.catalogFingerprint != ""
}

func workerEventScope(plan WorkerPlan, definition Name, binding BindingName) (int, bool, error) {
	if len(definition.value) > MaxNameBytes || len(binding.value) > MaxBindingNameBytes {
		return 0, false, tooLarge("worker event scope")
	}
	if definition.IsZero() != binding.IsZero() {
		return 0, false, invalid("worker event scope")
	}
	if definition.IsZero() {
		return plan.totalConcurrency, false, nil
	}
	if !definition.valid() || !binding.valid() {
		return 0, false, invalid("worker event scope")
	}
	index := sort.Search(len(plan.descriptions), func(index int) bool {
		return plan.descriptions[index].Definition.Value() >= definition.Value()
	})
	if index == len(plan.descriptions) || plan.descriptions[index].Definition != definition || plan.descriptions[index].Binding != binding {
		return 0, false, invalid("worker event scope is not in the plan")
	}
	return plan.descriptions[index].Concurrency, true, nil
}

func validateWorkerEventIdentity(spec workerEventSpec, exact bool) error {
	if spec.Operation == WorkerOperationApply {
		if !exact {
			return invalid("worker event requires a plan binding")
		}
		return nil
	}
	if spec.Operation == WorkerOperationAdmission {
		return nil
	}
	if exact {
		return invalid("aggregate worker event scope")
	}
	return nil
}

func validateWorkerEventOperationFields(spec workerEventSpec, concurrency int) error {
	if spec.Operation == WorkerOperationApply {
		if !spec.CommandKind.Valid() {
			return invalid("worker apply event command")
		}
	} else if spec.CommandKind != 0 {
		return invalid("worker event command")
	}
	if spec.Operation == WorkerOperationAdmission {
		return validateWorkerAdmissionEvent(spec)
	}
	if spec.AdmissionSignal != AdmissionUninitialized {
		return invalid("worker event admission signal")
	}
	if spec.Operation != WorkerOperationClaim && spec.Operation != WorkerOperationRecover && spec.Bytes != 0 {
		return invalid("worker event bytes")
	}
	if spec.Operation != WorkerOperationRecover && (spec.Released != 0 || spec.More) {
		return invalid("worker event recovery metrics")
	}
	if spec.Operation != WorkerOperationRenew && spec.Operation != WorkerOperationApply && len(spec.Results) != 0 {
		return invalid("worker event delivery results")
	}
	if spec.Outcome == WorkerOutcomeSaturated && (spec.Limit == 0 || spec.Active < spec.Limit) {
		return invalid("saturated worker event metrics")
	}
	switch spec.Operation {
	case WorkerOperationRun, WorkerOperationDrain:
		return validateWorkerLifecycleEvent(spec)
	case WorkerOperationClaim:
		return validateWorkerClaimEvent(spec, concurrency)
	case WorkerOperationRecover:
		return validateWorkerRecoverEvent(spec)
	case WorkerOperationRenew:
		return validateWorkerRenewEvent(spec)
	case WorkerOperationApply:
		return validateWorkerApplyEvent(spec)
	default:
		return invalid("worker event operation")
	}
}

func validateWorkerLifecycleEvent(spec workerEventSpec) error {
	if spec.Items != 0 || spec.Bytes != 0 || len(spec.Results) != 0 || spec.Outcome == WorkerOutcomeStarted && spec.Elapsed != 0 {
		return invalid("worker lifecycle event metrics")
	}
	if spec.Outcome == WorkerOutcomeForced && spec.Active == 0 {
		return invalid("forced worker event active count")
	}
	return nil
}

func validateWorkerClaimEvent(spec workerEventSpec, concurrency int) error {
	if spec.Items > MaxClaimItems || spec.Bytes > MaxClaimBytes {
		return tooLarge("worker claim event metrics")
	}
	if spec.Items > concurrency {
		return invalid("worker claim event exceeds plan availability")
	}
	if spec.Outcome == WorkerOutcomeComplete {
		if spec.Items == 0 || spec.Bytes == 0 {
			return invalid("complete worker claim event")
		}
		return nil
	}
	if spec.Items != 0 || spec.Bytes != 0 {
		return invalid("worker claim event result")
	}
	return nil
}

func validateWorkerRecoverEvent(spec workerEventSpec) error {
	if spec.Items > MaxReclaimBatch || spec.Bytes > MaxClaimBytes {
		return tooLarge("worker recover event metrics")
	}
	if spec.Items > MaxReclaimBatch-spec.Released {
		return tooLarge("worker recover event items")
	}
	if spec.Items > 0 && spec.Bytes == 0 || spec.Items == 0 && spec.Bytes != 0 {
		return invalid("worker recover event bytes")
	}
	if spec.Outcome == WorkerOutcomeComplete {
		if spec.Items+spec.Released == 0 {
			return invalid("complete worker recover event")
		}
		return nil
	}
	if spec.Outcome == WorkerOutcomeEmpty {
		if spec.Items != 0 || spec.Released != 0 || spec.Bytes != 0 || spec.More {
			return invalid("empty worker recover event")
		}
		return nil
	}
	if spec.Items != 0 || spec.Released != 0 || spec.Bytes != 0 || spec.More {
		return invalid("worker recover event result")
	}
	return nil
}

func validateWorkerRenewEvent(spec workerEventSpec) error {
	if spec.Items < 1 {
		return invalid("worker renew event items")
	}
	if spec.Items > MaxClaimItems {
		return tooLarge("worker renew event items")
	}
	if spec.Outcome != WorkerOutcomeComplete {
		if len(spec.Results) != 0 {
			return invalid("uncertain worker renew results")
		}
		return nil
	}
	if len(spec.Results) == 0 || !workerResultItemsMatch(spec.Results, spec.Items) {
		return invalid("worker renew result counts")
	}
	for _, result := range spec.Results {
		if result.mutation == DeliveryMutationApplied && result.control == DeliveryControlTerminated {
			return invalid("worker renew applied termination")
		}
	}
	return nil
}

func validateWorkerApplyEvent(spec workerEventSpec) error {
	if spec.Items != 1 {
		return invalid("worker apply event items")
	}
	if spec.Outcome != WorkerOutcomeComplete {
		if len(spec.Results) != 0 {
			return invalid("uncertain worker apply results")
		}
		return nil
	}
	if len(spec.Results) != 1 || spec.Results[0].items != 1 {
		return invalid("worker apply result count")
	}
	result := spec.Results[0]
	if result.mutation == DeliveryMutationApplied && result.control == DeliveryControlTerminated && spec.CommandKind != DeliveryCommandArbitrateAttemptDeadline && spec.CommandKind != DeliveryCommandRevokeAttempt {
		return invalid("worker apply termination command")
	}
	if result.mutation == DeliveryMutationApplied && result.control == DeliveryControlCancelRequested && spec.CommandKind != DeliveryCommandProgress && spec.CommandKind != DeliveryCommandFinishAttempt && spec.CommandKind != DeliveryCommandArbitrateAttemptDeadline {
		return invalid("worker apply cancellation command")
	}
	return nil
}

func validateWorkerAdmissionEvent(spec workerEventSpec) error {
	if !spec.AdmissionSignal.Valid() || spec.Items != 0 || spec.Released != 0 || spec.Bytes != 0 || len(spec.Results) != 0 || spec.More || spec.Elapsed != 0 || spec.Failure != WorkerFailureNone {
		return invalid("worker admission event")
	}
	switch spec.AdmissionSignal {
	case AdmissionReady, AdmissionUnrestricted:
		if spec.Limit == 0 {
			return invalid("ready worker admission limit")
		}
		if spec.Active >= spec.Limit {
			if spec.Outcome != WorkerOutcomeSaturated {
				return invalid("saturated worker admission outcome")
			}
			return nil
		}
		if spec.Outcome != WorkerOutcomeReady {
			return invalid("ready worker admission outcome")
		}
	case AdmissionHeld:
		if spec.Outcome != WorkerOutcomeHeld || spec.Limit != 0 {
			return invalid("held worker admission outcome")
		}
	case AdmissionStale:
		if spec.Outcome != WorkerOutcomeStale || spec.Limit != 0 {
			return invalid("stale worker admission outcome")
		}
	case AdmissionUninitialized, AdmissionInvalid:
		if spec.Outcome != WorkerOutcomeInvalid || spec.Limit != 0 {
			return invalid("invalid worker admission outcome")
		}
	default:
		return invalid("worker admission signal")
	}
	return nil
}

func workerResultItemsMatch(results []WorkerDeliveryResultCount, items int) bool {
	remaining := items
	for _, result := range results {
		if result.items > remaining {
			return false
		}
		remaining -= result.items
	}
	return remaining == 0
}

func validWorkerOperationOutcome(operation WorkerOperation, outcome WorkerOutcome) bool {
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

func workerDriverOperation(operation WorkerOperation) bool {
	return operation == WorkerOperationClaim || operation == WorkerOperationRecover || operation == WorkerOperationRenew || operation == WorkerOperationApply
}
