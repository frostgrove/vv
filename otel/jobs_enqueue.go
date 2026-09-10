package vvotel

import (
	"context"

	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/trace"
)

func Enqueue[P any](ctx context.Context, t *Telemetry, queue *jobs.Queue, definition jobs.DefinitionOf[P], payload P, options ...jobs.EnqueueOption) (jobs.InvocationID, error) {
	return executeJobsEnqueue(ctx, t, SignalJobsEnqueueSpan, OpJobsEnqueueEnqueue, OutcomeOk, func(jobs.InvocationID) string {
		return OutcomeOk
	}, func(next context.Context) (jobs.InvocationID, error) {
		return jobs.Enqueue(next, queue, definition, payload, options...)
	})
}

type enqueueOnceResult struct {
	id      jobs.InvocationID
	outcome jobs.EnqueueOnceOutcome
}

func EnqueueOnce[P any](ctx context.Context, t *Telemetry, queue *jobs.Queue, definition jobs.DefinitionOf[P], intent jobs.ProducerIntent, payload P, options ...jobs.EnqueueOption) (jobs.InvocationID, jobs.EnqueueOnceOutcome, error) {
	result, err := executeJobsEnqueue(ctx, t, SignalJobsEnqueueSpan, OpJobsEnqueueEnqueueOnce, "created", func(result enqueueOnceResult) string {
		outcome, _ := JobsEnqueueOnceOutcomeName(result.outcome.String())
		return outcome
	}, func(next context.Context) (enqueueOnceResult, error) {
		id, outcome, err := jobs.EnqueueOnce(next, queue, definition, intent, payload, options...)
		return enqueueOnceResult{id: id, outcome: outcome}, err
	})
	return result.id, result.outcome, err
}

func EnqueueIn[P any](ctx context.Context, t *Telemetry, queue *jobs.Queue, stager jobs.Stager, definition jobs.DefinitionOf[P], payload P, options ...jobs.EnqueueOption) (jobs.Staged, error) {
	return executeJobsEnqueue(ctx, t, SignalJobsEnqueueStagedSpan, OpJobsEnqueueEnqueueIn, "staged", func(jobs.Staged) string {
		return "staged"
	}, func(next context.Context) (jobs.Staged, error) {
		return jobs.EnqueueIn(next, queue, stager, definition, payload, options...)
	})
}

func EnqueueOnceIn[P any](ctx context.Context, t *Telemetry, queue *jobs.Queue, stager jobs.Stager, definition jobs.DefinitionOf[P], intent jobs.ProducerIntent, payload P, options ...jobs.EnqueueOption) (jobs.Staged, error) {
	return executeJobsEnqueue(ctx, t, SignalJobsEnqueueStagedSpan, OpJobsEnqueueEnqueueOnceIn, "staged", func(jobs.Staged) string {
		return "staged"
	}, func(next context.Context) (jobs.Staged, error) {
		return jobs.EnqueueOnceIn(next, queue, stager, definition, intent, payload, options...)
	})
}

func executeJobsEnqueue[T any](ctx context.Context, t *Telemetry, spanSignal Signal, operation string, startOutcome string, success func(T) string, fn func(context.Context) (T, error)) (T, error) {
	if t == nil {
		return fn(ctx)
	}
	spanName, spanKind, ok := jobsEnqueueSpan(spanSignal, operation)
	if !ok {
		return fn(ctx)
	}
	tracer := t.tracer
	if !t.signalEnabled(spanSignal) {
		tracer = nil
	}
	return executeOperationResult(ctx, operationSpec{
		tracer:           tracer,
		histogram:        t.float64Histogram(SignalJobsEnqueueDuration),
		spanName:         spanName,
		spanKind:         spanKind,
		operation:        operation,
		startOutcome:     startOutcome,
		classifyError:    classifyJobsError,
		spanAttributes:   jobsEnqueueSpanAttributes(spanSignal),
		metricAttributes: jobsEnqueueMetricAttributes,
	}, success, fn)
}

func jobsEnqueueSpan(signal Signal, operation string) (string, trace.SpanKind, bool) {
	switch signal {
	case SignalJobsEnqueueSpan:
		name, ok := SpanJobsEnqueueName(operation)
		return name, trace.SpanKindProducer, ok
	case SignalJobsEnqueueStagedSpan:
		name, ok := SpanJobsEnqueueStagedName(operation)
		return name, trace.SpanKindInternal, ok
	default:
		return "", trace.SpanKindUnspecified, false
	}
}
