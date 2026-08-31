package storageminio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
)

const (
	claimMarkerKey     = "vv-stage-claim"
	claimMarkerValue   = "1"
	claimStateKey      = "vv-stage-claim-state"
	claimTokenKey      = "vv-stage-claim-token"
	claimStateActive   = "active"
	claimStateRetired  = "retired"
	claimStateTerminal = "terminal"

	claimAcquireAttempts = 3
)

var errInvalidClaim = errors.New("invalid claim record")

type claimLease struct {
	object      string
	stageObject string
	token       string
	etag        string
}

type claimRecord struct {
	claimLease
	state     string
	expiresAt time.Time
}

func (this *Backend) acquireClaim(ctx context.Context, operation string, namespace storage.Namespace, id storage.StageID) (claimLease, error) {
	object, err := this.claimName(namespace, id)
	if err != nil {
		return claimLease{}, storage.NewError(operation, storage.KindInvalid, err)
	}
	stageObject, err := this.stageName(namespace, id)
	if err != nil {
		return claimLease{}, storage.NewError(operation, storage.KindInvalid, err)
	}
	token, err := newClaimToken(operation)
	if err != nil {
		return claimLease{}, err
	}
	expiresAt := this.now().Add(storage.MaxStageTTL)

	for attempt := 0; attempt < claimAcquireAttempts; attempt++ {
		if err := this.requireStage(ctx, operation, stageObject); err != nil {
			return claimLease{}, err
		}
		lease, err := this.writeClaim(ctx, operation, object, stageObject, token, claimStateActive, expiresAt, "")
		if err == nil {
			return this.confirmClaim(ctx, operation, lease)
		}
		if !claimCreateCollision(err) {
			return claimLease{}, err
		}

		current, readErr := this.readClaim(ctx, operation, object, stageObject)
		if errors.Is(readErr, storage.ErrNotFound) {
			continue
		}
		if readErr != nil {
			return claimLease{}, readErr
		}
		if current.state == claimStateActive && current.token == token {
			return this.confirmClaim(ctx, operation, current.claimLease)
		}
		if current.state == claimStateTerminal {
			return claimLease{}, storage.NewError(operation, storage.KindNotFound, errors.New("stage is terminal"))
		}
		if current.state == claimStateActive && this.now().Before(current.expiresAt) {
			return claimLease{}, storage.NewError(operation, storage.KindConflict, errors.New("stage operation is already active"))
		}

		if err := this.requireStage(ctx, operation, stageObject); err != nil {
			return claimLease{}, err
		}
		lease, err = this.writeClaim(ctx, operation, object, stageObject, token, claimStateActive, expiresAt, current.etag)
		if err == nil {
			return this.confirmClaim(ctx, operation, lease)
		}
		if !claimCASLost(err) {
			return claimLease{}, err
		}

		observed, readErr := this.readClaim(ctx, operation, object, stageObject)
		if readErr == nil && observed.state == claimStateActive && observed.token == token {
			return this.confirmClaim(ctx, operation, observed.claimLease)
		}
		if errors.Is(readErr, storage.ErrNotFound) {
			continue
		}
		if readErr != nil {
			return claimLease{}, readErr
		}
		if observed.state == claimStateTerminal {
			return claimLease{}, storage.NewError(operation, storage.KindNotFound, errors.New("stage is terminal"))
		}
		return claimLease{}, storage.NewError(operation, storage.KindConflict, err)
	}

	return claimLease{}, storage.NewError(operation, storage.KindConflict, errors.New("stage claim changed concurrently"))
}

func (this *Backend) requireStage(ctx context.Context, operation, stageObject string) error {
	if ctx == nil {
		return storage.NewError(operation, storage.KindInvalid, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return storage.NewError(operation, storage.KindCancelled, err)
	}
	_, err := this.client.StatObject(ctx, this.bucket, stageObject, minio.StatObjectOptions{})
	return mapError(operation, err, 0, nil)
}

func (this *Backend) confirmClaim(ctx context.Context, operation string, lease claimLease) (claimLease, error) {
	err := this.requireStage(ctx, operation, lease.stageObject)
	if err == nil {
		return lease, nil
	}
	if errors.Is(err, storage.ErrNotFound) {
		if _, releaseErr := this.releaseClaim(ctx, operation, lease, claimStateTerminal); releaseErr != nil {
			return claimLease{}, releaseErr
		}
	}

	return claimLease{}, err
}

func (this *Backend) releaseClaim(ctx context.Context, operation string, lease claimLease, targetState string) (bool, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimReleaseTimeout)
	defer cancel()

	transitioned, owned, err := this.transitionClaim(cleanupCtx, operation, lease, targetState)
	if err != nil {
		if errors.Is(cleanupCtx.Err(), context.DeadlineExceeded) {
			return false, storage.NewError(operation, storage.KindTemporary, cleanupCtx.Err())
		}
		return false, err
	}
	if targetState != claimStateTerminal || !owned {
		return false, nil
	}
	err = this.client.RemoveObject(cleanupCtx, this.bucket, transitioned.object, minio.RemoveObjectOptions{})
	if err != nil && errors.Is(cleanupCtx.Err(), context.DeadlineExceeded) {
		return false, storage.NewError(operation, storage.KindTemporary, cleanupCtx.Err())
	}
	if err := mapError(operation, err, 0, nil); err != nil {
		return false, err
	}
	return true, nil
}

func (this *Backend) transitionClaim(ctx context.Context, operation string, lease claimLease, targetState string) (claimLease, bool, error) {
	if targetState != claimStateRetired && targetState != claimStateTerminal {
		return claimLease{}, false, storage.NewError(operation, storage.KindInternal, errors.New("claim transition is invalid"))
	}
	token, err := newClaimToken(operation)
	if err != nil {
		return claimLease{}, false, err
	}
	transitioned, writeErr := this.writeClaim(ctx, operation, lease.object, lease.stageObject, token, targetState, this.now(), lease.etag)
	if writeErr == nil {
		return transitioned, true, nil
	}

	current, readErr := this.readClaim(ctx, operation, lease.object, lease.stageObject)
	if errors.Is(readErr, storage.ErrNotFound) {
		return claimLease{}, false, nil
	}
	if readErr == nil {
		if current.token == token && current.state == targetState {
			return current.claimLease, true, nil
		}
		if current.token != lease.token || current.etag != lease.etag {
			return claimLease{}, false, nil
		}
	}
	return claimLease{}, false, writeErr
}

func (this *Backend) cleanupExpiredClaims(ctx context.Context, namespace storage.Namespace, limit int, result *storage.CleanupResult) error {
	prefix, err := this.claimPrefix(namespace)
	if err != nil {
		return storage.NewError("cleanup", storage.KindInvalid, err)
	}
	listCtx, cancel := context.WithCancel(ctx)
	objects := this.client.ListObjects(listCtx, this.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	defer func() {
		cancel()
		for range objects {
		}
	}()
	for object := range objects {
		if object.Err != nil {
			return mapError("cleanup", object.Err, 0, nil)
		}
		id, ok := stageIDFromName(prefix, object.Key)
		if !ok {
			continue
		}
		stageObject, err := this.stageName(namespace, id)
		if err != nil {
			return storage.NewError("cleanup", storage.KindInvalid, err)
		}
		claim, err := this.readClaim(ctx, "cleanup", object.Key, stageObject)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if errors.Is(err, errInvalidClaim) {
			continue
		}
		if err != nil {
			return err
		}

		_, stageErr := this.client.StatObject(ctx, this.bucket, stageObject, minio.StatObjectOptions{})
		stageErr = mapError("cleanup", stageErr, 0, nil)
		stageMissing := errors.Is(stageErr, storage.ErrNotFound)
		if stageErr != nil && !stageMissing {
			return stageErr
		}
		if !stageMissing {
			if claim.state == claimStateActive && !this.now().Before(claim.expiresAt) {
				if _, err := this.releaseClaim(ctx, "cleanup", claim.claimLease, claimStateRetired); err != nil {
					return err
				}
			}
			continue
		}

		deleted := false
		if claim.state == claimStateTerminal {
			err := this.client.RemoveObject(ctx, this.bucket, claim.object, minio.RemoveObjectOptions{})
			if err := mapError("cleanup", err, 0, nil); err != nil {
				return err
			}
			deleted = true
		} else {
			deleted, err = this.releaseClaim(ctx, "cleanup", claim.claimLease, claimStateTerminal)
			if err != nil {
				return err
			}
		}
		if !deleted {
			continue
		}
		result.Removed++
		if result.Removed == limit {
			result.More = true
			return nil
		}
	}
	return nil
}

func newClaimToken(operation string) (string, error) {
	id, err := storage.NewStageID()
	if err != nil {
		return "", storage.NewError(operation, storage.KindInternal, err)
	}
	return id.Value(), nil
}

func (this *Backend) writeClaim(ctx context.Context, operation, object, stageObject, token, state string, expiresAt time.Time, matchETag string) (claimLease, error) {
	if ctx == nil {
		return claimLease{}, storage.NewError(operation, storage.KindInvalid, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return claimLease{}, storage.NewError(operation, storage.KindCancelled, err)
	}
	metadata, err := mergeMetadata(nil, map[string]string{
		claimMarkerKey: claimMarkerValue,
		claimStateKey:  state,
		claimTokenKey:  token,
		stageExpiryKey: expiresAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return claimLease{}, storage.NewError(operation, storage.KindInternal, err)
	}
	options := minio.PutObjectOptions{
		ContentType:      "application/octet-stream",
		UserMetadata:     metadata,
		DisableMultipart: true,
		SendContentMd5:   true,
	}
	mode := storage.CreateOnly
	if matchETag == "" {
		options.SetMatchETagExcept("*")
	} else {
		options.SetMatchETag(matchETag)
		mode = storage.Replace
	}
	upload, err := this.client.PutObject(ctx, this.bucket, object, strings.NewReader(token), int64(len(token)), options)
	if err != nil {
		return claimLease{}, mapError(operation, err, mode, nil)
	}
	lease := claimLease{object: object, stageObject: stageObject, token: token, etag: strings.Trim(upload.ETag, `"`)}
	if lease.etag != "" {
		return lease, nil
	}
	current, err := this.readClaim(ctx, operation, object, stageObject)
	if err != nil {
		return claimLease{}, err
	}
	if current.token != token || current.state != state {
		return claimLease{}, storage.NewError(operation, storage.KindConflict, errors.New("claim generation changed before validation"))
	}
	return current.claimLease, nil
}

func (this *Backend) readClaim(ctx context.Context, operation, object, stageObject string) (claimRecord, error) {
	info, err := this.client.StatObject(ctx, this.bucket, object, minio.StatObjectOptions{})
	if err != nil {
		return claimRecord{}, mapError(operation, err, 0, nil)
	}
	metadata, err := rawUserMetadata(info)
	if err != nil {
		return claimRecord{}, invalidClaimError(operation, storage.KindInternal, err.Error())
	}
	if metadata[claimMarkerKey] != claimMarkerValue {
		return claimRecord{}, invalidClaimError(operation, storage.KindConflict, "claim marker is absent")
	}
	state := metadata[claimStateKey]
	token := metadata[claimTokenKey]
	if state == "" && token == "" {
		state = claimStateActive
	} else if (state != claimStateActive && state != claimStateRetired && state != claimStateTerminal) || token == "" {
		return claimRecord{}, invalidClaimError(operation, storage.KindInternal, "claim state is invalid")
	}
	rawExpiry := metadata[stageExpiryKey]
	expiresAt, err := time.Parse(time.RFC3339Nano, rawExpiry)
	if err != nil || expiresAt.IsZero() {
		return claimRecord{}, invalidClaimError(operation, storage.KindInternal, "claim expiry is invalid")
	}
	etag := strings.Trim(info.ETag, `"`)
	if etag == "" {
		return claimRecord{}, storage.NewError(operation, storage.KindInternal, errors.New("claim ETag is absent"))
	}
	return claimRecord{
		claimLease: claimLease{object: object, stageObject: stageObject, token: token, etag: etag},
		state:      state,
		expiresAt:  expiresAt.UTC(),
	}, nil
}

func invalidClaimError(operation string, kind storage.Kind, message string) error {
	return storage.NewError(operation, kind, fmt.Errorf("%w: %s", errInvalidClaim, message))
}

func claimCreateCollision(err error) bool {
	return errors.Is(err, storage.ErrAlreadyExists) || errors.Is(err, storage.ErrConflict)
}

func claimCASLost(err error) bool {
	return errors.Is(err, storage.ErrPreconditionFailed) ||
		errors.Is(err, storage.ErrAlreadyExists) ||
		errors.Is(err, storage.ErrConflict) ||
		errors.Is(err, storage.ErrNotFound)
}
