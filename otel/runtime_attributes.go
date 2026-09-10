package vvotel

import (
	vvruntime "github.com/frostgrove/vv/runtime"
	"go.opentelemetry.io/otel/attribute"
)

func runtimeTransitionAttributes(state vvruntime.RunnerState) ([]attribute.KeyValue, bool) {
	phase, ok := RuntimePhaseName(string(state.Phase))
	if !ok {
		return nil, false
	}
	attributes := []attribute.KeyValue{
		AttrComponent.String(ComponentRuntimeLifecycle),
		AttrPhase.String(phase),
	}
	attributes = appendRuntimeDeclaration(attributes, state.Declaration)
	return admitSignalAttributes(SignalRuntimeTransitions, "", attributes)
}

func runtimeLifecycleAttributes(signal Signal, event vvruntime.LifecycleEvent) ([]attribute.KeyValue, bool) {
	operation, ok := runtimeLifecycleOperation(event.Operation())
	if !ok {
		return nil, false
	}
	outcome, ok := runtimeLifecycleOutcome(event.Outcome())
	if !ok {
		return nil, false
	}
	attributes := operationAttributes(ComponentRuntimeLifecycle, operation, outcome, runtimeLifecycleErrorType(event))
	attributes = appendRuntimeDeclaration(attributes, event.Declaration())
	return admitSignalAttributes(signal, "", attributes)
}

func appendRuntimeDeclaration(attributes []attribute.KeyValue, declaration vvruntime.Declaration) []attribute.KeyValue {
	if placement, ok := RuntimePlacementName(string(declaration.Placement)); ok {
		attributes = append(attributes, AttrPlacement.String(placement))
	}
	if durability, ok := RuntimeDurabilityName(string(declaration.Durability)); ok {
		attributes = append(attributes, AttrDurability.String(durability))
	}
	return attributes
}

func runtimeLifecycleOperation(operation vvruntime.LifecycleOperation) (string, bool) {
	switch operation {
	case vvruntime.LifecycleOperationRun:
		return OpRuntimeLifecycleRun, true
	case vvruntime.LifecycleOperationDrain:
		return OpRuntimeLifecycleDrain, true
	default:
		return "", false
	}
}

func runtimeLifecycleOutcome(outcome vvruntime.LifecycleOutcome) (string, bool) {
	switch outcome {
	case vvruntime.LifecycleOutcomeOK:
		return OutcomeOk, true
	case vvruntime.LifecycleOutcomeError:
		return OutcomeError, true
	case vvruntime.LifecycleOutcomeCanceled:
		return OutcomeCanceled, true
	case vvruntime.LifecycleOutcomeTimeout:
		return OutcomeTimeout, true
	default:
		return "", false
	}
}
