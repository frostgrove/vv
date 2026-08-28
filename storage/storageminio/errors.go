package storageminio

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
)

func mapReaderFailure(operation string, provenance readProvenance, err error) error {
	if provenance == callerSource {
		return storage.NewError(operation, storage.KindSource, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return storage.NewError(operation, storage.KindCancelled, err)
	}
	if errors.Is(err, errSizeMismatch) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrNoProgress) {
		return storage.NewError(operation, storage.KindTemporary, err)
	}
	return mapError(operation, err, 0, nil)
}

func mapError(operation string, err error, mode storage.WriteMode, sourceErr error) error {
	if err == nil {
		return nil
	}
	if sourceErr != nil {
		// Provenance wins over the error's concrete value. A live call whose
		// reader itself returns context.Canceled still has a source failure; only
		// cancellation reported by the operation/SDK is call cancellation.
		return storage.NewError(operation, storage.KindSource, sourceErr)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return storage.NewError(operation, storage.KindCancelled, err)
	}
	if kind := storage.KindOf(err); kind != "" {
		// Re-project the bounded kind so a provider seam or nested adapter cannot
		// leak its own operation label through this backend's public error.
		return storage.NewError(operation, kind, err)
	}

	var response minio.ErrorResponse
	if errors.As(err, &response) {
		kind := kindFromResponse(response, mode)
		return storage.NewError(operation, kind, err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return storage.NewError(operation, storage.KindTemporary, err)
	}
	return storage.NewError(operation, storage.KindInternal, err)
}

func kindFromResponse(response minio.ErrorResponse, mode storage.WriteMode) storage.Kind {
	switch response.Code {
	case minio.NoSuchKey, minio.NoSuchVersion:
		return storage.KindNotFound
	case minio.NoSuchBucket:
		return storage.KindUnavailable
	case minio.PreconditionFailed:
		if mode == storage.CreateOnly {
			return storage.KindAlreadyExists
		}
		return storage.KindPreconditionFailed
	case minio.Conflict, "ConditionalRequestConflict":
		return storage.KindConflict
	case minio.AccessDenied, minio.InvalidAccessKeyID, minio.SignatureDoesNotMatch, minio.AllAccessDisabled:
		return storage.KindForbidden
	case minio.InvalidArgument, minio.InvalidBucketName, minio.XMinioInvalidObjectName:
		return storage.KindInvalid
	case minio.APINotSupported, minio.NotImplemented:
		return storage.KindUnsupported
	case minio.EntityTooLarge, minio.EntityTooSmall, minio.IncompleteBody, minio.UnexpectedEOF, minio.BadDigest:
		return storage.KindSource
	case minio.InternalError, "RequestTimeout", "SlowDown", "ServiceUnavailable":
		return storage.KindTemporary
	}

	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return storage.KindForbidden
	case http.StatusNotFound:
		// A real missing object carries NoSuchKey/NoSuchVersion above. A bare
		// 404 is commonly an endpoint/proxy/bucket routing failure and must not
		// masquerade as logical object absence.
		return storage.KindUnavailable
	case http.StatusConflict:
		return storage.KindConflict
	case http.StatusPreconditionFailed:
		if mode == storage.CreateOnly {
			return storage.KindAlreadyExists
		}
		return storage.KindPreconditionFailed
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return storage.KindTemporary
	default:
		return storage.KindInternal
	}
}

func isNotFound(err error) bool {
	var response minio.ErrorResponse
	if !errors.As(err, &response) {
		return false
	}
	return response.Code == minio.NoSuchKey || response.Code == minio.NoSuchVersion
}

func uncertain(err error) bool {
	switch storage.KindOf(err) {
	case storage.KindCancelled, storage.KindTemporary, storage.KindUnavailable, storage.KindInternal:
		return true
	default:
		return false
	}
}
