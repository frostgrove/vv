package vvotel

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/health"
	"go.opentelemetry.io/otel/attribute"
)

const (
	healthStatePassing = "passing"
	healthStateFailing = "failing"
)

type HealthOption func(*healthSettings)

type healthSettings struct {
	resourceName ApprovedName
}

func WithHealthResource(name ApprovedName) HealthOption {
	return func(settings *healthSettings) {
		settings.resourceName = name
	}
}

func Health(t *Telemetry, contribution health.Contribution, options ...HealthOption) health.Contribution {
	if t == nil || contribution.Importance == health.Disabled || nilInterface(contribution.Probe) {
		return contribution
	}
	importance, known := HealthImportanceName(string(contribution.Importance))
	if !known || !healthSignalsEnabled(t) {
		return contribution
	}

	settings := healthSettings{resourceName: t.resourceName()}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	if t.signalEnabled(SignalHealthSpan) && !nilInterface(t.tracer) {
		settings.resourceName = ApprovedName(t.boundResourceName(settings.resourceName))
	} else {
		settings.resourceName = ApprovedName(normalizeResourceName(settings.resourceName.Value()))
	}

	wrapped := contribution
	wrapped.Probe = &healthProbe{
		inner:        contribution.Probe,
		tel:          t,
		importance:   importance,
		resourceName: settings.resourceName.Value(),
	}
	return wrapped
}

func healthSignalsEnabled(t *Telemetry) bool {
	return t.signalEnabled(SignalHealthChecks) ||
		t.signalEnabled(SignalHealthDuration) ||
		t.signalEnabled(SignalHealthSpan)
}

type healthProbe struct {
	inner        health.Probe
	tel          *Telemetry
	importance   string
	resourceName string
}

func (probe *healthProbe) Check(ctx context.Context) error {
	var tracer = probe.tel.tracer
	if !probe.tel.signalEnabled(SignalHealthSpan) {
		tracer = nil
	}
	_, err := executeOperation(ctx, operationSpec{
		tracer:        tracer,
		histogram:     probe.tel.float64Histogram(SignalHealthDuration),
		counter:       probe.tel.int64Counter(SignalHealthChecks),
		spanName:      SpanHealth,
		operation:     OpHealthCheck,
		resourceName:  probe.resourceName,
		classifyError: classifyHealthError,
		spanAttributes: func(operation string, outcome string, errorType string, resourceName string, extra []attribute.KeyValue) ([]attribute.KeyValue, bool) {
			return healthSpanAttributes(probe.importance, operation, outcome, errorType, resourceName)
		},
		metricAttributes: func(_ string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
			return healthMetricAttributes(SignalHealthDuration, probe.importance, outcome, errorType)
		},
		counterAttributes: func(_ string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
			return healthMetricAttributes(SignalHealthChecks, probe.importance, outcome, errorType)
		},
	}, func(next context.Context) (struct{}, error) {
		return struct{}{}, probe.inner.Check(next)
	})
	return err
}

func healthSpanAttributes(importance string, operation string, outcome string, errorType string, resourceName string) ([]attribute.KeyValue, bool) {
	attributes := operationAttributes(ComponentHealth, operation, outcome, errorType)
	attributes = append(attributes, AttrImportance.String(importance))
	if resourceName != "" {
		attributes = append(attributes, AttrResourceName.String(resourceName))
	}
	return admitSignalAttributes(SignalHealthSpan, spanStatus(outcome, errorType), attributes)
}

func healthMetricAttributes(signal Signal, importance string, outcome string, errorType string) ([]attribute.KeyValue, bool) {
	state := healthStatePassing
	if outcome != OutcomeOk {
		state = healthStateFailing
	}
	attributes := []attribute.KeyValue{
		AttrComponent.String(ComponentHealth),
		AttrImportance.String(importance),
		AttrState.String(state),
	}
	if errorType != "" {
		attributes = append(attributes, AttrErrorType.String(errorType))
	}
	return admitSignalAttributes(signal, "", attributes)
}

func classifyHealthError(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled, ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout, ErrorTypeTimeout
	}
	return OutcomeError, ErrorTypeInternal
}
