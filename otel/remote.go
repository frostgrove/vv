package vvotel

import (
	"context"
	"encoding/json"

	"github.com/frostgrove/vv/remote"
)

func Remote(t *Telemetry, next remote.Transport) remote.Transport {
	if nilInterface(next) {
		return nil
	}
	return &remoteDecorator{inner: next, tel: t}
}

type remoteDecorator struct {
	inner remote.Transport
	tel   *Telemetry
}

func (d *remoteDecorator) Do(ctx context.Context, call *remote.Call) (json.RawMessage, error) {
	if call == nil {
		return d.inner.Do(ctx, call)
	}
	operation, ok := RemoteOperationName(string(call.Method))
	if !ok {
		return d.inner.Do(ctx, call)
	}
	spanName, ok := SpanRemoteName(operation)
	if !ok || d.tel == nil {
		return d.inner.Do(ctx, call)
	}
	tracer := d.tel.tracer
	if !d.tel.signalEnabled(SignalRemoteSpan) {
		tracer = nil
	}
	return executeOperation(ctx, operationSpec{
		tracer:           tracer,
		histogram:        d.tel.float64Histogram(SignalRemoteDuration),
		spanName:         spanName,
		operation:        operation,
		classifyError:    classifyCommandError,
		spanAttributes:   remoteSpanAttributes,
		metricAttributes: remoteMetricAttributes,
	}, func(operationContext context.Context) (json.RawMessage, error) {
		return d.inner.Do(operationContext, call)
	})
}
