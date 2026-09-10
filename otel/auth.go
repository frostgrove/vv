package vvotel

import (
	"context"

	"github.com/frostgrove/vv/auth"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func Auth(t *Telemetry) auth.Observer {
	return &authObserver{tel: t}
}

func AuthEvents(t *Telemetry) auth.Observer {
	return &authEventObserver{tel: t}
}

type authObserver struct {
	tel *Telemetry
}

func (o *authObserver) Refused(ctx context.Context, reason auth.Reason) {
	if o == nil || o.tel == nil {
		return
	}
	reasonName, ok := AuthRefusalReasonName(string(reason.Kind))
	if !ok {
		return
	}
	counter := o.tel.int64Counter(SignalAuthRefusals)
	attributes, admitted := authRefusalAttributes(SignalAuthRefusals, reasonName)
	if nilInterface(counter) || !admitted {
		return
	}
	safeAdd(counter, ctx, 1, metric.WithAttributes(attributes...))
}

type authEventObserver struct {
	tel *Telemetry
}

func (o *authEventObserver) Refused(ctx context.Context, reason auth.Reason) {
	if o == nil || o.tel == nil || !o.tel.signalEnabled(SignalAuthRefusalEvent) {
		return
	}
	reasonName, ok := AuthRefusalReasonName(string(reason.Kind))
	if !ok {
		return
	}
	span := safeSpanFromContext(ctx)
	attributes, admitted := authRefusalAttributes(SignalAuthRefusalEvent, reasonName)
	if nilInterface(span) || !safeIsRecording(span) || !admitted {
		return
	}
	safeAddEvent(span, EventAuthRefusal, trace.WithAttributes(attributes...))
}
