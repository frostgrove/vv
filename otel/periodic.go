package vvotel

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
)

type PeriodicOption func(*periodicSettings)

type periodicSettings struct {
	resourceName ApprovedName
}

func WithPeriodicResource(name ApprovedName) PeriodicOption {
	return func(settings *periodicSettings) {
		settings.resourceName = name
	}
}

func Periodic(t *Telemetry, pass func(context.Context) error, options ...PeriodicOption) func(context.Context) error {
	if t == nil || pass == nil || !periodicSignalsEnabled(t) {
		return pass
	}
	settings := periodicSettings{resourceName: t.resourceName()}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	if t.signalEnabled(SignalRuntimePeriodicSpan) && !nilInterface(t.tracer) {
		settings.resourceName = ApprovedName(t.boundResourceName(settings.resourceName))
	} else {
		settings.resourceName = ApprovedName(normalizeResourceName(settings.resourceName.Value()))
	}

	return func(ctx context.Context) error {
		var tracer = t.tracer
		if !t.signalEnabled(SignalRuntimePeriodicSpan) {
			tracer = nil
		}
		_, err := executeOperation(ctx, operationSpec{
			tracer:        tracer,
			histogram:     t.float64Histogram(SignalRuntimePeriodicDuration),
			spanName:      SpanRuntimePeriodic,
			operation:     OpRuntimePeriodicPass,
			resourceName:  settings.resourceName.Value(),
			classifyError: classifyPeriodicError,
			spanAttributes: func(operation string, outcome string, errorType string, resourceName string, extra []attribute.KeyValue) ([]attribute.KeyValue, bool) {
				return periodicSpanAttributes(operation, outcome, errorType, resourceName)
			},
			metricAttributes: periodicMetricAttributes,
		}, func(next context.Context) (struct{}, error) {
			return struct{}{}, pass(next)
		})
		return err
	}
}

func periodicSignalsEnabled(t *Telemetry) bool {
	return t.signalEnabled(SignalRuntimePeriodicSpan) || t.signalEnabled(SignalRuntimePeriodicDuration)
}

func periodicSpanAttributes(operation string, outcome string, errorType string, resourceName string) ([]attribute.KeyValue, bool) {
	attributes := operationAttributes(ComponentRuntimePeriodic, operation, outcome, errorType)
	if resourceName != "" {
		attributes = append(attributes, AttrResourceName.String(resourceName))
	}
	return admitSignalAttributes(SignalRuntimePeriodicSpan, spanStatus(outcome, errorType), attributes)
}

func periodicMetricAttributes(operation string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	return admitSignalAttributes(
		SignalRuntimePeriodicDuration,
		"",
		operationAttributes(ComponentRuntimePeriodic, operation, outcome, errorType),
	)
}

func classifyPeriodicError(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled, ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout, ErrorTypeTimeout
	}
	return OutcomeError, ErrorTypeInternal
}
