package vvotel

import (
	"context"
	"errors"
	"io"

	"github.com/frostgrove/vv/storage"
	"go.opentelemetry.io/otel/metric"
)

type StorageOption func(*storageSettings)

type storageSettings struct {
	resourceName ApprovedName
	streams      bool
}

func WithStorageResource(name ApprovedName) StorageOption {
	return func(s *storageSettings) {
		s.resourceName = name
	}
}

func WithStorageStreams() StorageOption {
	return func(s *storageSettings) {
		s.streams = true
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
	if t != nil && t.signalEnabled(SignalStorageSpan) && !nilInterface(t.tracer) {
		s.resourceName = ApprovedName(t.boundResourceName(s.resourceName))
	} else {
		s.resourceName = ApprovedName(normalizeResourceName(s.resourceName.Value()))
	}
	return func(next storage.Store) storage.Store {
		if next == nil {
			return nil
		}
		return &storeDecorator{
			inner:          next,
			tel:            t,
			resourceName:   string(s.resourceName),
			observeStreams: s.streams && storageStreamSignalsEnabled(t),
		}
	}
}

type storeDecorator struct {
	inner          storage.Store
	tel            *Telemetry
	resourceName   string
	observeStreams bool
}

func (d *storeDecorator) Put(ctx context.Context, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	operationContext := ctx
	result, err := executeStorage(ctx, d.tel, d.resourceName, OpStoragePut, func(c context.Context) (storage.Info, error) {
		operationContext = c
		return d.inner.Put(c, key, source, options)
	})
	if err == nil {
		recordStorageOperationBytes(operationContext, d.tel, OpStoragePut, result.Size)
	}
	return result, err
}

func (d *storeDecorator) Open(ctx context.Context, key storage.Key, options storage.ReadOptions) (io.ReadCloser, storage.Info, error) {
	type openResult struct {
		body io.ReadCloser
		info storage.Info
	}
	incomingSpanContext := safeSpanContextFromContext(ctx)
	operationContext := ctx
	result, err := executeStorage(ctx, d.tel, d.resourceName, OpStorageOpen, func(c context.Context) (openResult, error) {
		operationContext = c
		body, info, openErr := d.inner.Open(c, key, options)
		return openResult{body: body, info: info}, openErr
	})
	if err != nil || !d.observeStreams || nilInterface(result.body) {
		return result.body, result.info, err
	}
	parent := safeSpanContextFromContext(operationContext)
	if !parent.IsValid() {
		parent = incomingSpanContext
	}
	result.body = newStorageStream(result.body, d.tel, parent)
	return result.body, result.info, err
}

func (d *storeDecorator) Head(ctx context.Context, key storage.Key) (storage.Info, error) {
	return executeStorage(ctx, d.tel, d.resourceName, OpStorageHead, func(c context.Context) (storage.Info, error) {
		return d.inner.Head(c, key)
	})
}

func (d *storeDecorator) Delete(ctx context.Context, key storage.Key, options storage.DeleteOptions) error {
	_, err := executeStorage(ctx, d.tel, d.resourceName, OpStorageDelete, func(c context.Context) (struct{}, error) {
		return struct{}{}, d.inner.Delete(c, key, options)
	})
	return err
}

func (d *storeDecorator) Stage(ctx context.Context, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
	operationContext := ctx
	result, err := executeStorage(ctx, d.tel, d.resourceName, OpStorageStage, func(c context.Context) (storage.Staged, error) {
		operationContext = c
		return d.inner.Stage(c, source, options)
	})
	if err == nil {
		recordStorageOperationBytes(operationContext, d.tel, OpStorageStage, result.Info.Size)
	}
	return result, err
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
	operationContext := ctx
	result, err := executeStorage(ctx, d.tel, d.resourceName, OpStorageCleanupExpired, func(c context.Context) (storage.CleanupResult, error) {
		operationContext = c
		return d.inner.CleanupExpired(c, options)
	})
	if err == nil {
		recordStorageCleanupRemoved(operationContext, d.tel, result)
	}
	return result, err
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
	if t == nil {
		return fn(ctx)
	}
	var tracer = t.tracer
	if !t.signalEnabled(SignalStorageSpan) {
		tracer = nil
	}
	var histogram = t.float64Histogram(SignalStorageDuration)
	if !t.signalEnabled(SignalStorageDuration) {
		histogram = nil
	}
	return executeOperation(ctx, operationSpec{
		tracer:           tracer,
		histogram:        histogram,
		spanName:         StorageSpanName(op),
		operation:        op,
		resourceName:     resourceName,
		classifyError:    classifyStorageError,
		spanAttributes:   storageSpanAttributes,
		metricAttributes: storageMetricAttributes,
	}, fn)
}

func recordStorageOperationBytes(ctx context.Context, t *Telemetry, operation string, size int64) {
	if t == nil || size < 0 || !t.signalEnabled(SignalStorageOperationBytes) {
		return
	}
	histogram := t.int64Histogram(SignalStorageOperationBytes)
	if nilInterface(histogram) {
		return
	}
	attributes, admitted := safeMetricAttributes(storageOperationBytesAttributes, operation, OutcomeOk, "")
	if !admitted {
		return
	}
	safeRecordInt64(histogram, ctx, size, metric.WithAttributes(attributes...))
}

func recordStorageCleanupRemoved(ctx context.Context, t *Telemetry, result storage.CleanupResult) {
	if t == nil || result.Removed < 0 || result.Removed > storage.MaxCleanupLimit || !t.signalEnabled(SignalStorageCleanupRemoved) {
		return
	}
	histogram := t.int64Histogram(SignalStorageCleanupRemoved)
	if nilInterface(histogram) {
		return
	}
	attributes, admitted := storageCleanupRemovedAttributes(result.More)
	if !admitted {
		return
	}
	safeRecordInt64(histogram, ctx, int64(result.Removed), metric.WithAttributes(attributes...))
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
