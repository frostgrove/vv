package vvotel

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type operationSpec struct {
	tracer              trace.Tracer
	histogram           metric.Float64Histogram
	counter             metric.Int64Counter
	spanName            string
	spanKind            trace.SpanKind
	spanStartOptions    func(context.Context) []trace.SpanStartOption
	operation           string
	startOutcome        string
	resourceName        string
	classifyError       func(error) (string, string)
	spanErrorAttributes func(error) []attribute.KeyValue
	spanAttributes      func(string, string, string, string, []attribute.KeyValue) ([]attribute.KeyValue, bool)
	metricAttributes    func(string, string, string) ([]attribute.KeyValue, bool)
	counterAttributes   func(string, string, string) ([]attribute.KeyValue, bool)
}

type operationRecorder struct {
	spec         operationSpec
	context      context.Context
	span         trace.Span
	metric       metric.Float64Histogram
	counter      metric.Int64Counter
	startedAt    time.Time
	instrumented bool
}

func executeOperation[T any](ctx context.Context, spec operationSpec, fn func(context.Context) (T, error)) (res T, err error) {
	return executeOperationResult(ctx, spec, nil, fn)
}

func executeOperationResult[T any](ctx context.Context, spec operationSpec, success func(T) string, fn func(context.Context) (T, error)) (res T, err error) {
	recorder := beginOperation(ctx, spec)
	if !recorder.instrumented {
		return fn(ctx)
	}

	completed := false
	defer func() {
		if completed {
			return
		}
		recorder.finishGoexit()
	}()

	res, err, panicked, panicValue := invokeOperation(recorder.context, fn)
	completed = true
	if panicked {
		recorder.finishPanic()
		panic(panicValue)
	}
	if err != nil {
		recorder.finish(err)
		return res, err
	}
	recorder.finishOutcome(safeResultOutcome(success, res), "", nil, true)
	return res, err
}

func invokeOperation[T any](ctx context.Context, fn func(context.Context) (T, error)) (res T, err error, panicked bool, panicValue any) {
	returned := false
	defer func() {
		if returned {
			return
		}
		panicValue = recover()
		panicked = true
	}()
	res, err = fn(ctx)
	returned = true
	return res, err, false, nil
}

func beginOperation(ctx context.Context, spec operationSpec) operationRecorder {
	recorder := operationRecorder{
		spec:    spec,
		context: ctx,
	}
	if !nilInterface(spec.histogram) {
		recorder.metric = spec.histogram
		recorder.instrumented = true
	}
	if !nilInterface(spec.counter) {
		recorder.counter = spec.counter
		recorder.instrumented = true
	}
	if !nilInterface(spec.tracer) {
		startOutcome := spec.startOutcome
		if startOutcome == "" {
			startOutcome = OutcomeOk
		}
		admitted, ok := safeOperationAttributes(spec.spanAttributes, spec.operation, startOutcome, "", spec.resourceName, nil)
		if !ok {
			if recorder.instrumented {
				recorder.startedAt = time.Now()
			}
			return recorder
		}
		kind := spec.spanKind
		if kind == trace.SpanKindUnspecified {
			kind = trace.SpanKindInternal
		}
		options := []trace.SpanStartOption{
			trace.WithSpanKind(kind),
			trace.WithAttributes(spanStartAttributes(admitted)...),
		}
		options = append(options, safeSpanStartOptions(spec.spanStartOptions, ctx)...)
		next, span, ok := safeStart(spec.tracer, ctx, spec.spanName, options...)
		if ok {
			if spec.resourceName == "" || safeSetAttributes(span, AttrResourceName.String(spec.resourceName)) {
				recorder.context = next
				recorder.span = span
				recorder.instrumented = true
			} else {
				safeEnd(span)
			}
		}
	}
	if recorder.instrumented {
		recorder.startedAt = time.Now()
	}
	return recorder
}

func safeSpanStartOptions(build func(context.Context) []trace.SpanStartOption, ctx context.Context) (options []trace.SpanStartOption) {
	if build == nil {
		return nil
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		options = nil
	}()
	options = build(ctx)
	completed = true
	return options
}

func safeResultOutcome[T any](classify func(T) string, result T) (outcome string) {
	outcome = OutcomeOk
	if classify == nil {
		return outcome
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		outcome = OutcomeOk
	}()
	outcome = classify(result)
	completed = true
	return outcome
}

func (r *operationRecorder) finish(err error) {
	if err == nil {
		r.finishOutcome(OutcomeOk, "", nil, true)
		return
	}
	outcome, errorType := safeClassifyError(r.spec.classifyError, err)
	r.finishOutcome(outcome, errorType, err, true)
}

func (r *operationRecorder) finishPanic() {
	r.finishOutcome(OutcomeError, ErrorTypePanic, nil, true)
}

func (r *operationRecorder) finishGoexit() {
	if nilInterface(r.span) {
		return
	}
	attributes, ok := safeOperationAttributes(r.spec.spanAttributes, r.spec.operation, OutcomeGoroutineExit, "", r.spec.resourceName, nil)
	if !ok {
		safeEnd(r.span)
		return
	}
	safeSetStatus(r.span, codes.Error, "")
	safeSetAttributes(r.span, spanTerminalAttributes(attributes)...)
	safeEnd(r.span)
}

func (r *operationRecorder) finishOutcome(outcome string, errorType string, err error, recordMetric bool) {
	if !nilInterface(r.span) {
		var extra []attribute.KeyValue
		if err != nil {
			extra = safeSpanErrorAttributes(r.spec.spanErrorAttributes, err)
		}
		attributes, admitted := safeOperationAttributes(r.spec.spanAttributes, r.spec.operation, outcome, errorType, r.spec.resourceName, extra)
		if admitted {
			if errorType != "" {
				safeSetStatus(r.span, codes.Error, "")
			}
			safeSetAttributes(r.span, spanTerminalAttributes(attributes)...)
		}
		safeEnd(r.span)
	}
	if !recordMetric || (nilInterface(r.metric) && nilInterface(r.counter)) {
		return
	}
	if !nilInterface(r.metric) {
		attributes, admitted := safeMetricAttributes(r.spec.metricAttributes, r.spec.operation, outcome, errorType)
		if admitted {
			safeRecord(r.metric, r.context, durationSince(r.startedAt), metric.WithAttributes(attributes...))
		}
	}
	if !nilInterface(r.counter) {
		build := r.spec.counterAttributes
		if build == nil {
			build = r.spec.metricAttributes
		}
		attributes, admitted := safeMetricAttributes(build, r.spec.operation, outcome, errorType)
		if admitted {
			safeAdd(r.counter, r.context, 1, metric.WithAttributes(attributes...))
		}
	}
}

func safeOperationAttributes(build func(string, string, string, string, []attribute.KeyValue) ([]attribute.KeyValue, bool), operation string, outcome string, errorType string, resourceName string, extra []attribute.KeyValue) (attributes []attribute.KeyValue, admitted bool) {
	if build == nil {
		return nil, false
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		attributes = nil
		admitted = false
	}()
	attributes, admitted = build(operation, outcome, errorType, resourceName, extra)
	completed = true
	return attributes, admitted
}

func safeMetricAttributes(build func(string, string, string) ([]attribute.KeyValue, bool), operation string, outcome string, errorType string) (attributes []attribute.KeyValue, admitted bool) {
	if build == nil {
		return nil, false
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		attributes = nil
		admitted = false
	}()
	attributes, admitted = build(operation, outcome, errorType)
	completed = true
	return attributes, admitted
}

func safeClassifyError(classify func(error) (string, string), err error) (outcome string, errorType string) {
	outcome = OutcomeError
	errorType = ErrorTypeInternal
	if classify == nil {
		return outcome, errorType
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		outcome = OutcomeError
		errorType = ErrorTypeInternal
	}()
	outcome, errorType = classify(err)
	completed = true
	return outcome, errorType
}

func safeSpanErrorAttributes(build func(error) []attribute.KeyValue, err error) (attributes []attribute.KeyValue) {
	if build == nil {
		return nil
	}
	completed := false
	defer func() {
		if completed {
			return
		}
		_ = recover()
		attributes = nil
	}()
	attributes = build(err)
	completed = true
	return attributes
}
