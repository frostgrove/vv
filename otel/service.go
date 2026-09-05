package vvotel

import (
	"context"
	"errors"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type ServiceOption func(*serviceSettings)

type serviceSettings struct {
	resourceName string
}

func WithServiceResource(name string) ServiceOption {
	return func(s *serviceSettings) {
		s.resourceName = name
	}
}

func Service[M any, ID comparable, U any](t *Telemetry, opts ...ServiceOption) port.ServiceMiddleware[M, ID, U] {
	var s serviceSettings
	if t != nil {
		s.resourceName = t.resourceName()
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&s)
		}
	}
	if t != nil {
		s.resourceName = t.boundResourceName(s.resourceName)
	} else {
		s.resourceName = normalizeResourceName(s.resourceName)
	}
	return func(next port.Service[M, ID, U]) port.Service[M, ID, U] {
		if next == nil {
			return nil
		}
		return &serviceDecorator[M, ID, U]{
			inner:        next,
			tel:          t,
			resourceName: s.resourceName,
		}
	}
}

type serviceDecorator[M any, ID comparable, U any] struct {
	inner        port.Service[M, ID, U]
	tel          *Telemetry
	resourceName string
}

func (d *serviceDecorator[M, ID, U]) Meta() *crud.Meta {
	return d.inner.Meta()
}

func (d *serviceDecorator[M, ID, U]) Paths() errs.Resolver {
	return d.inner.Paths()
}

func (d *serviceDecorator[M, ID, U]) List(ctx context.Context, cmd port.ListCommand) (crud.PaginatedResponse[M], error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandList, func(c context.Context) (crud.PaginatedResponse[M], error) {
		return d.inner.List(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Count(ctx context.Context, cmd port.CountCommand) (int64, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandCount, func(c context.Context) (int64, error) {
		return d.inner.Count(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Get(ctx context.Context, cmd port.GetCommand[ID]) (M, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandGet, func(c context.Context) (M, error) {
		return d.inner.Get(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Create(ctx context.Context, cmd port.CreateCommand[M]) (M, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandCreate, func(c context.Context) (M, error) {
		return d.inner.Create(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Update(ctx context.Context, cmd port.UpdateCommand[ID, U]) (M, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandUpdate, func(c context.Context) (M, error) {
		return d.inner.Update(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Replace(ctx context.Context, cmd port.ReplaceCommand[ID, M]) (M, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandReplace, func(c context.Context) (M, error) {
		return d.inner.Replace(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Delete(ctx context.Context, cmd port.DeleteCommand[ID]) (int64, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandDelete, func(c context.Context) (int64, error) {
		return d.inner.Delete(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) DeleteMany(ctx context.Context, cmd port.BulkDeleteCommand[ID]) (int64, error) {
	return executeCommand(ctx, d.tel, d.resourceName, OpCommandDeleteMany, func(c context.Context) (int64, error) {
		return d.inner.DeleteMany(c, cmd)
	})
}

func (d *serviceDecorator[M, ID, U]) Restorable() (port.RestorableService[ID], bool) {
	underlying, ok := port.RestorableOf[ID](d.inner)
	if !ok {
		return nil, false
	}
	return &restorableDecorator[ID]{
		inner:        underlying,
		tel:          d.tel,
		resourceName: d.resourceName,
	}, true
}

type restorableDecorator[ID comparable] struct {
	inner        port.RestorableService[ID]
	tel          *Telemetry
	resourceName string
}

func (r *restorableDecorator[ID]) Restore(ctx context.Context, cmd port.RestoreCommand[ID]) (int64, error) {
	return executeCommand(ctx, r.tel, r.resourceName, OpCommandRestore, func(c context.Context) (int64, error) {
		return r.inner.Restore(c, cmd)
	})
}

func (r *restorableDecorator[ID]) RestoreMany(ctx context.Context, cmd port.BulkRestoreCommand[ID]) (int64, error) {
	return executeCommand(ctx, r.tel, r.resourceName, OpCommandRestoreMany, func(c context.Context) (int64, error) {
		return r.inner.RestoreMany(c, cmd)
	})
}

func executeCommand[T any](
	ctx context.Context,
	t *Telemetry,
	resourceName string,
	op string,
	fn func(context.Context) (T, error),
) (res T, err error) {
	if t == nil {
		return fn(ctx)
	}

	var span trace.Span
	traceEnabled := !t.traceDisabled(false) && !nilInterface(t.tracer)
	histogram := t.commandDurationInstrument()
	if !traceEnabled && histogram == nil {
		return fn(ctx)
	}

	callContext := ctx

	if traceEnabled {
		var started bool
		callContext, span, started = safeStart(
			t.tracer,
			ctx,
			CommandSpanName(op),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				AttrComponent.String(ComponentCommand),
				AttrOperationName.String(op),
			),
		)
		if !started {
			return fn(ctx)
		}
		if resourceName != "" && !safeSetAttributes(span, AttrResourceName.String(resourceName)) {
			safeEnd(span)
			return fn(ctx)
		}
	}
	start := time.Now()

	returned := false
	defer func() {
		if !returned {
			dur := durationSince(start)
			if traceEnabled && span != nil {
				safeSetStatus(span, codes.Error, "")
				safeSetAttributes(span,
					AttrOperationOutcome.String(OutcomeError),
					AttrErrorType.String(ErrorTypePanic),
				)
				safeEnd(span)
			}
			if histogram != nil {
				attrs := []attribute.KeyValue{
					AttrComponent.String(ComponentCommand),
					AttrOperationName.String(op),
					AttrOperationOutcome.String(OutcomeError),
					AttrErrorType.String(ErrorTypePanic),
				}
				safeRecord(histogram, ctx, dur, metric.WithAttributes(attrs...))
			}
		}
	}()

	res, err = fn(callContext)
	returned = true

	dur := durationSince(start)

	if err == nil {
		if traceEnabled && span != nil {
			safeSetAttributes(span, AttrOperationOutcome.String(OutcomeOk))
			safeEnd(span)
		}
		if histogram != nil {
			attrs := []attribute.KeyValue{
				AttrComponent.String(ComponentCommand),
				AttrOperationName.String(op),
				AttrOperationOutcome.String(OutcomeOk),
			}
			safeRecord(histogram, ctx, dur, metric.WithAttributes(attrs...))
		}
		return res, nil
	}

	outcome, errType := classifyCommandError(err)
	if traceEnabled && span != nil {
		safeSetStatus(span, codes.Error, "")
		safeSetAttributes(span,
			AttrOperationOutcome.String(outcome),
			AttrErrorType.String(errType),
		)
		var fault *errs.Fault
		if errors.As(err, &fault) && fault.Code != "" {
			if code, ok := AllowedErrorCode(string(fault.Code)); ok {
				safeSetAttributes(span, AttrErrorCode.String(code))
			}
		}
		safeEnd(span)
	}
	if histogram != nil {
		attrs := []attribute.KeyValue{
			AttrComponent.String(ComponentCommand),
			AttrOperationName.String(op),
			AttrOperationOutcome.String(outcome),
			AttrErrorType.String(errType),
		}
		safeRecord(histogram, ctx, dur, metric.WithAttributes(attrs...))
	}
	return res, err
}

func classifyCommandError(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled, ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout, ErrorTypeTimeout
	}
	if errors.Is(err, crud.ErrStaleVersion) {
		return OutcomeError, ErrorTypeStaleVersion
	}
	var fault *errs.Fault
	if errors.As(err, &fault) && fault.Code == errs.CodeStaleVersion {
		return OutcomeError, ErrorTypeStaleVersion
	}
	k := port.KindOf(err)
	switch k {
	case errs.KindNotFound:
		return OutcomeError, ErrorTypeNotFound
	case errs.KindForbidden, errs.KindUnauthorized:
		return OutcomeError, ErrorTypeForbidden
	case errs.KindConflict:
		return OutcomeError, ErrorTypeConflict
	case errs.KindValidation, errs.KindBadRequest, errs.KindTooLarge, errs.KindMethodNotAllowed:
		return OutcomeError, ErrorTypeInvalid
	default:
		return OutcomeError, ErrorTypeInternal
	}
}
