package vvotel

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/auth"
)

func Authenticator(t *Telemetry, next auth.Authenticator) auth.Authenticator {
	if nilInterface(next) {
		return nil
	}
	return &authenticatorDecorator{inner: next, tel: t}
}

type authenticatorDecorator struct {
	inner auth.Authenticator
	tel   *Telemetry
}

func (d *authenticatorDecorator) Authenticate(ctx context.Context, credential auth.Credential) (auth.Principal, error) {
	if d.tel == nil {
		return d.inner.Authenticate(ctx, credential)
	}
	tracer := d.tel.tracer
	if !d.tel.signalEnabled(SignalAuthenticationSpan) {
		tracer = nil
	}
	return executeOperation(ctx, operationSpec{
		tracer:           tracer,
		histogram:        d.tel.float64Histogram(SignalAuthenticationDuration),
		spanName:         SpanAuthentication,
		operation:        OpAuthenticationAuthenticate,
		classifyError:    classifyAuthenticationError,
		spanAttributes:   authenticationSpanAttributes,
		metricAttributes: authenticationMetricAttributes,
	}, func(operationContext context.Context) (auth.Principal, error) {
		return d.inner.Authenticate(operationContext, credential)
	})
}

func classifyAuthenticationError(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled, ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout, ErrorTypeTimeout
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		return authenticationOutcomeRefused, ""
	}
	return classifyCommandError(err)
}
