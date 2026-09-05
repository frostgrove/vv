package vvotel

import (
	"context"
	"errors"
	"io"

	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type StorageOption func(*storageSettings)

type storageSettings struct {
	resourceName string
}

func WithStorageResource(name string) StorageOption {
	return func(s *storageSettings) {
		s.resourceName = name
	}
}

func Store(t *Telemetry, opts ...StorageOption) storage.Middleware {
	var s storageSettings
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
	return func(next storage.Store) storage.Store {
		if next == nil {
			return nil
		}
		return &storeDecorator{
			inner:        next,
			tel:          t,
			resourceName: s.resourceName,
		}
	}
}

type storeDecorator struct {
	inner        storage.Store
	tel          *Telemetry
	resourceName string
}

func (d *storeDecorator) Put(ctx context.Context, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStoragePut, func(c context.Context) (storage.Info, error) {
		return d.inner.Put(c, key, source, options)
	})
}

func (d *storeDecorator) Open(ctx context.Context, key storage.Key) (io.ReadCloser, storage.Info, error) {
	if d.tel == nil || d.tel.traceDisabled(true) || nilInterface(d.tel.tracer) {
		return d.inner.Open(ctx, key)
	}

	c, span, started := safeStart(
		d.tel.tracer,
		ctx,
		StorageSpanName(OpStorageOpen),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			AttrComponent.String(ComponentStorage),
			AttrOperationName.String(OpStorageOpen),
		),
	)
	if !started {
		return d.inner.Open(ctx, key)
	}
	if d.resourceName != "" && !safeSetAttributes(span, AttrResourceName.String(d.resourceName)) {
		safeEnd(span)
		return d.inner.Open(ctx, key)
	}

	returned := false
	defer func() {
		if !returned {
			safeSetStatus(span, codes.Error, "")
			safeSetAttributes(span,
				AttrOperationOutcome.String(OutcomeError),
				AttrErrorType.String(ErrorTypePanic),
			)
			safeEnd(span)
		}
	}()

	body, info, err := d.inner.Open(c, key)
	returned = true

	if err == nil {
		safeSetAttributes(span, AttrOperationOutcome.String(OutcomeOk))
		safeEnd(span)
		return body, info, nil
	}

	outcome, errType := classifyStorageError(err)
	safeSetStatus(span, codes.Error, "")
	safeSetAttributes(span,
		AttrOperationOutcome.String(outcome),
		AttrErrorType.String(errType),
	)
	safeEnd(span)
	return body, info, err
}

func (d *storeDecorator) Head(ctx context.Context, key storage.Key) (storage.Info, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStorageHead, func(c context.Context) (storage.Info, error) {
		return d.inner.Head(c, key)
	})
}

func (d *storeDecorator) Delete(ctx context.Context, key storage.Key) error {
	_, err := executeStorage(ctx, d.tel, d.resourceName, OpStorageDelete, func(c context.Context) (struct{}, error) {
		return struct{}{}, d.inner.Delete(c, key)
	})
	return err
}

func (d *storeDecorator) Stage(ctx context.Context, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStorageStage, func(c context.Context) (storage.Staged, error) {
		return d.inner.Stage(c, source, options)
	})
}

func (d *storeDecorator) Promote(ctx context.Context, stageID storage.StageID, key storage.Key, options storage.PromoteOptions) (storage.Info, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStoragePromote, func(c context.Context) (storage.Info, error) {
		return d.inner.Promote(c, stageID, key, options)
	})
}

func (d *storeDecorator) Abort(ctx context.Context, stageID storage.StageID) error {
	_, err := executeStorage(ctx, d.tel, d.resourceName, OpStorageAbort, func(c context.Context) (struct{}, error) {
		return struct{}{}, d.inner.Abort(c, stageID)
	})
	return err
}

func (d *storeDecorator) CleanupExpired(ctx context.Context, options storage.CleanupOptions) (storage.CleanupResult, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStorageCleanupExpired, func(c context.Context) (storage.CleanupResult, error) {
		return d.inner.CleanupExpired(c, options)
	})
}

func (d *storeDecorator) TemporaryURL(ctx context.Context, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStorageTemporaryUrl, func(c context.Context) (storage.Link, error) {
		return d.inner.TemporaryURL(c, key, options)
	})
}

func (d *storeDecorator) Capabilities() storage.Capabilities {
	return d.inner.Capabilities()
}

func executeStorage[T any](
	ctx context.Context,
	t *Telemetry,
	resourceName string,
	op string,
	fn func(context.Context) (T, error),
) (res T, err error) {
	if t == nil || t.traceDisabled(true) || nilInterface(t.tracer) {
		return fn(ctx)
	}

	c, span, started := safeStart(
		t.tracer,
		ctx,
		StorageSpanName(op),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			AttrComponent.String(ComponentStorage),
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

	returned := false
	defer func() {
		if !returned {
			safeSetStatus(span, codes.Error, "")
			safeSetAttributes(span,
				AttrOperationOutcome.String(OutcomeError),
				AttrErrorType.String(ErrorTypePanic),
			)
			safeEnd(span)
		}
	}()

	res, err = fn(c)
	returned = true

	if err == nil {
		safeSetAttributes(span, AttrOperationOutcome.String(OutcomeOk))
		safeEnd(span)
		return res, nil
	}

	outcome, errType := classifyStorageError(err)
	safeSetStatus(span, codes.Error, "")
	safeSetAttributes(span,
		AttrOperationOutcome.String(outcome),
		AttrErrorType.String(errType),
	)
	safeEnd(span)
	return res, err
}

func classifyStorageError(err error) (string, string) {
	if errors.Is(err, context.Canceled) {
		return OutcomeCanceled, ErrorTypeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout, ErrorTypeTimeout
	}
	k := storage.KindOf(err)
	switch k {
	case storage.KindNotFound:
		return OutcomeError, ErrorTypeNotFound
	case storage.KindForbidden:
		return OutcomeError, ErrorTypeForbidden
	case storage.KindConflict, storage.KindAlreadyExists:
		return OutcomeError, ErrorTypeConflict
	case storage.KindInvalid, storage.KindPreconditionFailed, storage.KindUnsupported:
		return OutcomeError, ErrorTypeInvalid
	case storage.KindCancelled:
		return OutcomeCanceled, ErrorTypeCanceled
	case storage.KindExpired:
		return OutcomeError, ErrorTypeStaleVersion
	default:
		return OutcomeError, ErrorTypeInternal
	}
}
