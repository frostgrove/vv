package storageminio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/s3utils"
)

const (
	MaxCreateOnlySize int64 = 5 * 1024 * 1024 * 1024

	maxObjectNameBytes  = 1024
	maxPrefixBytes      = 128
	stageDirectory      = ".vv-stage"
	claimDirectory      = ".vv-stage-claim"
	stageMarkerKey      = "vv-stage"
	stageExpiryKey      = "vv-stage-expires"
	stageMarkerValue    = "1"
	claimReleaseTimeout = 5 * time.Second
)

type readProvenance uint8

const (
	callerSource readProvenance = iota + 1
	backendBody
)

type Clock func() time.Time

type Config struct {
	Client     *minio.Client
	Bucket     string
	Prefix     string
	MaxLinkTTL time.Duration
	Clock      Clock
}

type Backend struct {
	client     clientAPI
	core       coreAPI
	bucket     string
	prefix     string
	maxLinkTTL time.Duration
	clock      Clock

	admin bucketAdmin
}

type clientAPI interface {
	PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) (minio.UploadInfo, error)
	StatObject(context.Context, string, string, minio.StatObjectOptions) (minio.ObjectInfo, error)
	RemoveObject(context.Context, string, string, minio.RemoveObjectOptions) error
	ListObjects(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
	PresignedGetObject(context.Context, string, string, time.Duration, url.Values) (*url.URL, error)
}

type coreAPI interface {
	GetObject(context.Context, string, string, minio.GetObjectOptions) (io.ReadCloser, minio.ObjectInfo, http.Header, error)
}

var _ storage.Backend = (*Backend)(nil)

func New(config *Config) (*Backend, error) {
	if config == nil {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("config is nil"))
	}
	if config.Client == nil {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("client is nil"))
	}
	backend, err := newBackend(config, config.Client, minio.Core{Client: config.Client})
	if err != nil {
		return nil, err
	}
	backend.admin = config.Client
	return backend, nil
}

func newBackend(config *Config, client clientAPI, core coreAPI) (*Backend, error) {
	if client == nil || core == nil {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("client is nil"))
	}
	if err := s3utils.CheckValidBucketNameStrict(config.Bucket); err != nil {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("bucket is invalid"))
	}
	if err := validatePrefix(config.Prefix); err != nil {
		return nil, storage.NewError("construct", storage.KindInvalid, err)
	}

	maxLinkTTL := config.MaxLinkTTL
	if maxLinkTTL == 0 {
		maxLinkTTL = storage.MaxTemporaryURLTTL
	}
	if maxLinkTTL < time.Second || maxLinkTTL > storage.MaxTemporaryURLTTL || maxLinkTTL%time.Second != 0 {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("maximum link TTL is invalid"))
	}

	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}

	prefixBytes := len(config.Prefix)
	if prefixBytes != 0 {
		prefixBytes++
	}

	if prefixBytes+63+1+storage.MaxKeyBytes > maxObjectNameBytes {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("prefix leaves insufficient object-name space"))
	}
	if prefixBytes+len(stageDirectory)+1+63+1+43 > maxObjectNameBytes {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("prefix leaves insufficient staging space"))
	}
	if prefixBytes+len(claimDirectory)+1+63+1+43 > maxObjectNameBytes {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("prefix leaves insufficient claim space"))
	}

	return &Backend{
		client:     client,
		core:       core,
		bucket:     config.Bucket,
		prefix:     config.Prefix,
		maxLinkTTL: maxLinkTTL,
		clock:      clock,
	}, nil
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if len(prefix) > maxPrefixBytes {
		return errors.New("prefix is too long")
	}
	if _, err := storage.ParseKey(prefix); err != nil {
		return errors.New("prefix is invalid")
	}
	return nil
}

func (this *Backend) Put(ctx context.Context, namespace storage.Namespace, key storage.Key, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	object, err := this.objectName(namespace, key)
	if err != nil {
		return storage.Info{}, storage.NewError("put", storage.KindInvalid, err)
	}
	if options.Mode == storage.CreateOnly && options.Size == nil {
		return this.putUnknownCreateOnly(ctx, namespace, object, source, options)
	}
	return this.put(ctx, "put", object, source, callerSource, options.Mode, options.Size, options.ContentType, options.Metadata, nil)
}

func (this *Backend) putUnknownCreateOnly(ctx context.Context, namespace storage.Namespace, finalObject string, source io.Reader, options storage.PutOptions) (storage.Info, error) {
	id, err := storage.NewStageID()
	if err != nil {
		return storage.Info{}, err
	}
	stageObject, err := this.stageName(namespace, id)
	if err != nil {
		return storage.Info{}, storage.NewError("put", storage.KindInvalid, err)
	}
	expiresAt := this.now().Add(storage.DefaultStageTTL)
	internal := map[string]string{
		stageMarkerKey: stageMarkerValue,
		stageExpiryKey: expiresAt.Format(time.RFC3339Nano),
	}

	if _, err := this.put(ctx, "put", stageObject, source, callerSource, storage.Replace, nil, options.ContentType, options.Metadata, internal); err != nil {
		_ = this.client.RemoveObject(ctx, this.bucket, stageObject, minio.RemoveObjectOptions{})
		return storage.Info{}, err
	}

	defer func() {
		_ = this.client.RemoveObject(ctx, this.bucket, stageObject, minio.RemoveObjectOptions{})
	}()

	body, objectInfo, _, err := this.core.GetObject(ctx, this.bucket, stageObject, minio.GetObjectOptions{})
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return storage.Info{}, mapError("put", err, 0, nil)
	}
	if body == nil {
		return storage.Info{}, storage.NewError("put", storage.KindInternal, errors.New("stage body is absent"))
	}
	defer body.Close()
	if objectInfo.Size < 0 {
		return storage.Info{}, storage.NewError("put", storage.KindInternal, errors.New("stage size is invalid"))
	}
	size := objectInfo.Size
	info, err := this.put(ctx, "put", finalObject, body, backendBody, storage.CreateOnly, &size, objectInfo.ContentType, options.Metadata, nil)
	if err != nil {
		return storage.Info{}, err
	}
	return info, nil
}

func (this *Backend) Open(ctx context.Context, namespace storage.Namespace, key storage.Key) (io.ReadCloser, storage.Info, error) {
	object, err := this.objectName(namespace, key)
	if err != nil {
		return nil, storage.Info{}, storage.NewError("open", storage.KindInvalid, err)
	}
	body, objectInfo, _, err := this.core.GetObject(ctx, this.bucket, object, minio.GetObjectOptions{})
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		return nil, storage.Info{}, mapError("open", err, 0, nil)
	}
	if body == nil {
		return nil, storage.Info{}, storage.NewError("open", storage.KindInternal, errors.New("object body is absent"))
	}
	info, err := infoFromObject(objectInfo)
	if err != nil {
		_ = body.Close()
		return nil, storage.Info{}, storage.NewError("open", storage.KindInternal, err)
	}
	return &openBody{body: body}, info, nil
}

func (this *Backend) Head(ctx context.Context, namespace storage.Namespace, key storage.Key) (storage.Info, error) {
	object, err := this.objectName(namespace, key)
	if err != nil {
		return storage.Info{}, storage.NewError("head", storage.KindInvalid, err)
	}
	objectInfo, err := this.client.StatObject(ctx, this.bucket, object, minio.StatObjectOptions{})
	if err != nil {
		return storage.Info{}, mapError("head", err, 0, nil)
	}
	info, err := infoFromObject(objectInfo)
	if err != nil {
		return storage.Info{}, storage.NewError("head", storage.KindInternal, err)
	}
	return info, nil
}

func (this *Backend) Delete(ctx context.Context, namespace storage.Namespace, key storage.Key) error {
	object, err := this.objectName(namespace, key)
	if err != nil {
		return storage.NewError("delete", storage.KindInvalid, err)
	}
	err = this.client.RemoveObject(ctx, this.bucket, object, minio.RemoveObjectOptions{})
	return mapError("delete", err, 0, nil)
}

func (this *Backend) Stage(ctx context.Context, namespace storage.Namespace, source io.Reader, options storage.StageOptions) (storage.Staged, error) {
	id, err := storage.NewStageID()
	if err != nil {
		return storage.Staged{}, err
	}
	expiresAt := this.now().Add(options.ExpiresIn).UTC()
	object, err := this.stageName(namespace, id)
	if err != nil {
		return storage.Staged{}, storage.NewError("stage", storage.KindInvalid, err)
	}
	internal := map[string]string{
		stageMarkerKey: stageMarkerValue,
		stageExpiryKey: expiresAt.Format(time.RFC3339Nano),
	}
	info, err := this.put(ctx, "stage", object, source, callerSource, storage.Replace, options.Size, options.ContentType, options.Metadata, internal)
	if err != nil {
		return storage.Staged{}, err
	}
	return storage.Staged{ID: id, Info: info, ExpiresAt: expiresAt}, nil
}

func (this *Backend) Promote(ctx context.Context, namespace storage.Namespace, id storage.StageID, key storage.Key, options storage.PromoteOptions) (result storage.Info, resultErr error) {
	stageObject, err := this.stageName(namespace, id)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInvalid, err)
	}
	finalObject, err := this.objectName(namespace, key)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInvalid, err)
	}
	claim, err := this.acquireClaim(ctx, "promote", namespace, id)
	if err != nil {
		return storage.Info{}, err
	}
	releaseState := claimStateRetired
	release := true
	committed := false
	defer func() {
		if release {
			if _, err := this.releaseClaim(ctx, "promote", claim, releaseState); err != nil && !committed {
				result = storage.Info{}
				resultErr = err
			}
		}
	}()

	body, objectInfo, _, err := this.core.GetObject(ctx, this.bucket, stageObject, minio.GetObjectOptions{})
	if err != nil {
		if body != nil {
			_ = body.Close()
		}
		mapped := mapError("promote", err, 0, nil)
		if errors.Is(mapped, storage.ErrNotFound) {
			releaseState = claimStateTerminal
		}
		return storage.Info{}, mapped
	}
	if body == nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInternal, errors.New("stage body is absent"))
	}
	defer body.Close()

	expiresAt, ok, err := stageExpiry(objectInfo)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInternal, err)
	}
	if !ok {
		return storage.Info{}, storage.NewError("promote", storage.KindNotFound, errors.New("stage marker is absent"))
	}
	if !this.now().Before(expiresAt) {
		return storage.Info{}, storage.NewError("promote", storage.KindExpired, nil)
	}
	metadata, err := portableMetadata(objectInfo)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInternal, err)
	}
	if objectInfo.Size < 0 {
		return storage.Info{}, storage.NewError("promote", storage.KindInternal, errors.New("stage size is invalid"))
	}
	size := objectInfo.Size
	info, err := this.put(ctx, "promote", finalObject, body, backendBody, options.Mode, &size, objectInfo.ContentType, metadata, nil)
	if err != nil {
		if uncertain(err) {
			release = false
		}

		return storage.Info{}, err
	}
	committed = true

	if err := this.client.RemoveObject(ctx, this.bucket, stageObject, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
		release = false
	} else {
		releaseState = claimStateTerminal
	}
	return info, nil
}

func (this *Backend) Abort(ctx context.Context, namespace storage.Namespace, id storage.StageID) (resultErr error) {
	object, err := this.stageName(namespace, id)
	if err != nil {
		return storage.NewError("abort", storage.KindInvalid, err)
	}
	claim, err := this.acquireClaim(ctx, "abort", namespace, id)
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	releaseState := claimStateRetired
	release := true
	defer func() {
		if release {
			if _, err := this.releaseClaim(ctx, "abort", claim, releaseState); err != nil {
				resultErr = err
			}
		}
	}()
	_, statErr := this.client.StatObject(ctx, this.bucket, object, minio.StatObjectOptions{})
	if statErr != nil {
		mapped := mapError("abort", statErr, 0, nil)
		if errors.Is(mapped, storage.ErrNotFound) {
			releaseState = claimStateTerminal
			return nil
		}
		if uncertain(mapped) {
			release = false
		}
		return mapped
	}
	resultErr = mapError("abort", this.client.RemoveObject(ctx, this.bucket, object, minio.RemoveObjectOptions{}), 0, nil)
	if resultErr == nil {
		releaseState = claimStateTerminal
	} else if errors.Is(resultErr, storage.ErrNotFound) {
		releaseState = claimStateTerminal
		resultErr = nil
	} else if uncertain(resultErr) {
		release = false
	}
	return resultErr
}

func (this *Backend) CleanupExpired(ctx context.Context, namespace storage.Namespace, options storage.CleanupOptions) (storage.CleanupResult, error) {
	prefix, err := this.stagePrefix(namespace)
	if err != nil {
		return storage.CleanupResult{}, storage.NewError("cleanup", storage.KindInvalid, err)
	}
	listCtx, cancel := context.WithCancel(ctx)
	objects := this.client.ListObjects(listCtx, this.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})
	result := storage.CleanupResult{}
	stop := func() {
		cancel()
		for range objects {
		}
	}
	defer stop()

	for object := range objects {
		if object.Err != nil {
			return result, mapError("cleanup", object.Err, 0, nil)
		}
		id, ok := stageIDFromName(prefix, object.Key)
		if !ok {
			continue
		}
		objectInfo, err := this.client.StatObject(ctx, this.bucket, object.Key, minio.StatObjectOptions{})
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return result, mapError("cleanup", err, 0, nil)
		}
		expiresAt, ok, err := stageExpiry(objectInfo)
		if err != nil || !ok {
			continue
		}
		if this.now().Before(expiresAt) {
			continue
		}
		claim, err := this.acquireClaim(ctx, "cleanup", namespace, id)
		if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}

		objectInfo, err = this.client.StatObject(ctx, this.bucket, object.Key, minio.StatObjectOptions{})
		if err != nil {
			mapped := mapError("cleanup", err, 0, nil)
			if errors.Is(mapped, storage.ErrNotFound) {
				deleted, releaseErr := this.releaseClaim(ctx, "cleanup", claim, claimStateTerminal)
				if releaseErr != nil {
					return result, releaseErr
				}
				if deleted {
					result.Removed++
					if result.Removed == options.Limit {
						result.More = true
						return result, nil
					}
				}
				continue
			}
			if uncertain(mapped) {
				return result, mapped
			}
			if _, releaseErr := this.releaseClaim(ctx, "cleanup", claim, claimStateRetired); releaseErr != nil {
				return result, releaseErr
			}
			return result, mapped
		}
		expiresAt, ok, err = stageExpiry(objectInfo)
		if err != nil || !ok || this.now().Before(expiresAt) {
			if _, releaseErr := this.releaseClaim(ctx, "cleanup", claim, claimStateRetired); releaseErr != nil {
				return result, releaseErr
			}
			continue
		}

		removeErr := mapError("cleanup", this.client.RemoveObject(ctx, this.bucket, object.Key, minio.RemoveObjectOptions{}), 0, nil)
		if errors.Is(removeErr, storage.ErrNotFound) {
			deleted, releaseErr := this.releaseClaim(ctx, "cleanup", claim, claimStateTerminal)
			if releaseErr != nil {
				return result, releaseErr
			}
			if deleted {
				result.Removed++
				if result.Removed == options.Limit {
					result.More = true
					return result, nil
				}
			}
			continue
		}
		if removeErr != nil {
			if !uncertain(removeErr) {
				if _, releaseErr := this.releaseClaim(ctx, "cleanup", claim, claimStateRetired); releaseErr != nil {
					return result, releaseErr
				}
			}
			return result, removeErr
		}
		if _, err := this.releaseClaim(ctx, "cleanup", claim, claimStateTerminal); err != nil {
			return result, err
		}
		result.Removed++
		if result.Removed == options.Limit {
			result.More = true
			return result, nil
		}
	}
	if err := this.cleanupExpiredClaims(ctx, namespace, options.Limit, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (this *Backend) TemporaryURL(ctx context.Context, namespace storage.Namespace, key storage.Key, options storage.TemporaryURLOptions) (storage.Link, error) {
	if options.ExpiresIn < time.Second || options.ExpiresIn > this.maxLinkTTL || options.ExpiresIn%time.Second != 0 {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInvalid, errors.New("link TTL exceeds backend policy"))
	}
	object, err := this.objectName(namespace, key)
	if err != nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInvalid, err)
	}
	issuedAt := this.now()
	u, err := this.client.PresignedGetObject(ctx, this.bucket, object, options.ExpiresIn, nil)
	if err != nil {
		return storage.Link{}, mapError("temporary URL", err, 0, nil)
	}
	if u == nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInternal, errors.New("presigned URL is absent"))
	}

	link, err := storage.NewLink(u.String(), issuedAt.Truncate(time.Second).Add(options.ExpiresIn))
	if err != nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInternal, err)
	}
	return link, nil
}

func (this *Backend) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		CreateOnly:   true,
		Replace:      true,
		Staging:      true,
		TemporaryURL: true,
	}
}

func (this *Backend) put(ctx context.Context, operation, object string, source io.Reader, provenance readProvenance, mode storage.WriteMode, size *int64, contentType string, metadata storage.Metadata, internal map[string]string) (storage.Info, error) {
	if ctx == nil {
		return storage.Info{}, storage.NewError(operation, storage.KindInvalid, errors.New("context is nil"))
	}
	if err := ctx.Err(); err != nil {
		return storage.Info{}, storage.NewError(operation, storage.KindCancelled, err)
	}
	if err := validateObjectName(object); err != nil {
		return storage.Info{}, storage.NewError(operation, storage.KindInvalid, err)
	}
	userMetadata, err := mergeMetadata(metadata, internal)
	if err != nil {
		return storage.Info{}, storage.NewError(operation, storage.KindInvalid, err)
	}

	objectSize := int64(-1)
	if size != nil {
		objectSize = *size
	}
	putOptions := minio.PutObjectOptions{
		ContentType:  contentType,
		UserMetadata: userMetadata,
	}
	if mode == storage.CreateOnly {
		if size == nil {
			return storage.Info{}, storage.NewError(operation, storage.KindUnsupported, errors.New("create-only placement requires a known size"))
		}
		if *size > MaxCreateOnlySize {
			return storage.Info{}, storage.NewError(operation, storage.KindUnsupported, errors.New("create-only payload exceeds the single-put limit"))
		}
		putOptions.SetMatchETagExcept("*")
		putOptions.DisableMultipart = true
	} else if mode != storage.Replace {
		return storage.Info{}, storage.NewError(operation, storage.KindInvalid, errors.New("write mode is invalid"))
	}

	tracked := &sourceReader{ctx: ctx, reader: source}
	var uploadSource io.Reader = tracked
	var exact *exactSizeReader
	if size != nil {
		exact = &exactSizeReader{reader: tracked, remaining: *size}
		uploadSource = exact
		if *size == 0 {
			var probe [1]byte
			if _, err := exact.Read(probe[:]); !errors.Is(err, io.EOF) {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return storage.Info{}, storage.NewError(operation, storage.KindCancelled, ctxErr)
				}
				return storage.Info{}, mapReaderFailure(operation, provenance, err)
			}
		}
	}
	upload, err := this.client.PutObject(ctx, this.bucket, object, uploadSource, objectSize, putOptions)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return storage.Info{}, storage.NewError(operation, storage.KindCancelled, ctxErr)
		}
		sourceErr := tracked.err
		if exact != nil && exact.err != nil {
			sourceErr = exact.err
		}
		if sourceErr != nil {
			return storage.Info{}, mapReaderFailure(operation, provenance, sourceErr)
		}
		return storage.Info{}, mapError(operation, err, mode, nil)
	}
	modifiedAt := upload.LastModified
	if modifiedAt.IsZero() {
		modifiedAt = this.now()
	}
	actualSize := upload.Size
	if actualSize == 0 && size != nil {
		actualSize = *size
	}
	return storage.Info{
		Size:        actualSize,
		ContentType: contentType,
		Metadata:    cloneMetadata(metadata),
		ModifiedAt:  modifiedAt,
		ETag:        upload.ETag,
		Version:     upload.VersionID,
	}, nil
}

type sourceReader struct {
	ctx                   context.Context
	reader                io.Reader
	err                   error
	consecutiveNoProgress int
}

const maxConsecutiveNoProgressReads = 100

var errSizeMismatch = errors.New("declared size does not match source")

type exactSizeReader struct {
	reader    io.Reader
	remaining int64
	done      bool
	err       error
	byte      [1]byte
}

func (this *exactSizeReader) Read(p []byte) (int, error) {
	if this.err != nil {
		return 0, this.err
	}
	if this.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if this.remaining == 0 {
		return this.verifyEmpty()
	}
	if this.remaining == 1 {
		return this.readVerifiedFinalByte(p)
	}

	read := p
	if int64(len(read)) >= this.remaining {
		read = read[:this.remaining-1]
	}
	n, err := this.reader.Read(read)
	this.remaining -= int64(n)
	if errors.Is(err, io.EOF) {
		this.err = errSizeMismatch
		return n, this.err
	}
	return n, err
}

func (this *exactSizeReader) verifyEmpty() (int, error) {
	zeroReads := 0
	for {
		n, err := this.reader.Read(this.byte[:])
		switch {
		case n > 0:
			this.err = errSizeMismatch
			return 0, this.err
		case errors.Is(err, io.EOF):
			this.done = true
			return 0, io.EOF
		case err != nil:
			return 0, err
		default:
			zeroReads++
			if zeroReads == maxConsecutiveNoProgressReads {
				return 0, io.ErrNoProgress
			}
		}
	}
}

func (this *exactSizeReader) readVerifiedFinalByte(p []byte) (int, error) {
	n, err := this.reader.Read(this.byte[:])
	if n == 0 {
		if errors.Is(err, io.EOF) {
			this.err = errSizeMismatch
			return 0, this.err
		}
		return 0, err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	last := this.byte[0]
	if !errors.Is(err, io.EOF) {
		if _, err := this.verifyEmpty(); !errors.Is(err, io.EOF) {
			return 0, err
		}
	}
	p[0] = last
	this.remaining = 0
	this.done = true
	return 1, nil
}

func (this *sourceReader) Read(p []byte) (int, error) {
	if this.err != nil {
		return 0, this.err
	}
	n, err := this.reader.Read(p)
	if this.ctx != nil {
		if ctxErr := this.ctx.Err(); ctxErr != nil {
			return n, ctxErr
		}
	}
	if n > 0 {
		this.consecutiveNoProgress = 0
	} else if err == nil && len(p) > 0 {
		this.consecutiveNoProgress++
		if this.consecutiveNoProgress >= maxConsecutiveNoProgressReads {
			err = io.ErrNoProgress
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		this.err = err
	}
	return n, err
}

func (this *Backend) objectName(namespace storage.Namespace, key storage.Key) (string, error) {
	return checkedJoin(this.prefix, namespace.Value(), key.Value())
}

func (this *Backend) stageName(namespace storage.Namespace, id storage.StageID) (string, error) {
	return checkedJoin(this.prefix, stageDirectory, namespace.Value(), id.Value())
}

func (this *Backend) stagePrefix(namespace storage.Namespace) (string, error) {
	name, err := checkedJoin(this.prefix, stageDirectory, namespace.Value())
	if err != nil {
		return "", err
	}
	return name + "/", nil
}

func (this *Backend) claimName(namespace storage.Namespace, id storage.StageID) (string, error) {
	return checkedJoin(this.prefix, claimDirectory, namespace.Value(), id.Value())
}

func (this *Backend) claimPrefix(namespace storage.Namespace) (string, error) {
	name, err := checkedJoin(this.prefix, claimDirectory, namespace.Value())
	if err != nil {
		return "", err
	}
	return name + "/", nil
}

func checkedJoin(parts ...string) (string, error) {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	name := strings.Join(filtered, "/")
	if err := validateObjectName(name); err != nil {
		return "", err
	}
	return name, nil
}

func validateObjectName(name string) error {
	if len(name) > maxObjectNameBytes || s3utils.CheckValidObjectName(name) != nil {
		return errors.New("physical object name is invalid")
	}
	return nil
}

func stageIDFromName(prefix, name string) (storage.StageID, bool) {
	if !strings.HasPrefix(name, prefix) {
		return storage.StageID{}, false
	}
	raw := strings.TrimPrefix(name, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return storage.StageID{}, false
	}
	id, err := storage.ParseStageID(raw)
	return id, err == nil
}

func (this *Backend) now() time.Time { return this.clock().UTC() }
