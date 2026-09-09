package vvotel

import (
	"context"

	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type JobAdapterOption interface {
	applyJobAdapter(*jobAdapterConfig)
}

type jobAdapterOption func(*jobAdapterConfig)

func (option jobAdapterOption) applyJobAdapter(config *jobAdapterConfig) { option(config) }

type jobAdapterConfig struct {
	parentChild bool
}

func ParentChild() JobAdapterOption {
	return jobAdapterOption(func(config *jobAdapterConfig) {
		config.parentChild = true
	})
}

func Job[P any](t *Telemetry, definition jobs.DefinitionOf[P], handler jobs.Handler[P], workerOptions ...jobs.WorkerOption) jobs.Consumer {
	var adapter jobs.AdapterHandler[P]
	if handler != nil {
		adapter = func(ctx context.Context, payload P, _ jobs.DeliveryMeta, _ jobs.AttemptController) error {
			return handler(ctx, payload)
		}
	}
	return jobs.OnAdapter(definition, JobAdapter(t, adapter), workerOptions...)
}

func JobAdapter[P any](t *Telemetry, adapter jobs.AdapterHandler[P], options ...JobAdapterOption) jobs.AdapterHandler[P] {
	if adapter == nil {
		return nil
	}
	config := jobAdapterConfig{}
	for _, option := range options {
		if nilInterface(option) {
			continue
		}
		applyJobAdapterOption(option, &config)
	}
	return func(ctx context.Context, payload P, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
		return executeJobHandler(ctx, t, meta, config, func(next context.Context) error {
			return adapter(next, payload, meta, controller)
		})
	}
}

func applyJobAdapterOption(option JobAdapterOption, config *jobAdapterConfig) {
	defer func() { _ = recover() }()
	option.applyJobAdapter(config)
}

func executeJobHandler(ctx context.Context, t *Telemetry, meta jobs.DeliveryMeta, config jobAdapterConfig, fn func(context.Context) error) error {
	if t == nil {
		return fn(ctx)
	}
	tracer := t.tracer
	if !t.signalEnabled(SignalJobsHandlerSpan) {
		tracer = nil
	}
	recorder := beginOperation(ctx, operationSpec{
		tracer:           tracer,
		histogram:        t.float64Histogram(SignalJobsHandlerDuration),
		spanName:         SpanJobsHandler,
		spanKind:         trace.SpanKindConsumer,
		spanStartOptions: jobsHandlerStartOptions(config),
		operation:        OpJobsHandlerHandle,
		classifyError:    classifyJobsError,
		spanAttributes:   jobsHandlerSpanAttributes,
		metricAttributes: jobsHandlerMetricAttributes,
	})
	invocationContext := ctx
	if !nilInterface(recorder.span) {
		invocationContext = trace.ContextWithSpan(ctx, recorder.span)
		recorder.context = invocationContext
	}
	recordJobHandlerMeasurements(t, invocationContext, meta)
	if !recorder.instrumented {
		return fn(ctx)
	}

	completed := false
	defer func() {
		if !completed {
			recorder.finishGoexit()
		}
	}()
	_, err, panicked, panicValue := invokeOperation(invocationContext, func(next context.Context) (struct{}, error) {
		return struct{}{}, fn(next)
	})
	completed = true
	if panicked {
		recorder.finishPanic()
		panic(panicValue)
	}
	recorder.finish(err)
	return err
}

func jobsHandlerStartOptions(config jobAdapterConfig) func(context.Context) []trace.SpanStartOption {
	if config.parentChild {
		return nil
	}
	return func(ctx context.Context) []trace.SpanStartOption {
		options := []trace.SpanStartOption{trace.WithNewRoot()}
		parent := safeSpanContextFromContext(ctx)
		if parent.IsValid() {
			options = append(options, trace.WithLinks(trace.Link{SpanContext: parent}))
		}
		return options
	}
}

func safeSpanContextFromContext(ctx context.Context) (spanContext trace.SpanContext) {
	defer func() { _ = recover() }()
	return trace.SpanContextFromContext(ctx)
}

func recordJobHandlerMeasurements(t *Telemetry, ctx context.Context, meta jobs.DeliveryMeta) {
	if meta.IsZero() {
		return
	}
	attributes, admitted := jobsHandlerMeasurementAttributes()
	if !admitted {
		return
	}
	delay := meta.StartedAt().Sub(meta.EligibleAt())
	if delay >= 0 {
		histogram := t.float64Histogram(SignalJobsQueueDelay)
		if !nilInterface(histogram) {
			safeRecord(histogram, ctx, delay.Seconds(), metric.WithAttributes(attributes...))
		}
	}
	attempt := int64(meta.AttemptOrdinal().Value())
	if attempt >= 1 && attempt <= jobs.MaxAttemptOrdinal {
		histogram := t.int64Histogram(SignalJobsHandlerAttempt)
		if !nilInterface(histogram) {
			safeRecordInt64(histogram, ctx, attempt, metric.WithAttributes(attributes...))
		}
	}
}
