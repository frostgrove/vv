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
	// MaxCreateOnlySize is the largest payload the adapter can place with an
	// atomic CreateOnly precondition. minio-go/v7 does not preserve custom
	// conditional headers when it completes a multipart upload, so CreateOnly
	// deliberately uses one conditional PUT, whose SDK limit is 5 GiB.
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

// Clock supplies wall time for staging expiry, cleanup and returned link
// expiry. A nil Clock uses time.Now.
type Clock func() time.Time

// Config contains only adapter-owned choices. Client construction and lifetime
// remain with the application.
type Config struct {
	Client     *minio.Client
	Bucket     string
	Prefix     string
	MaxLinkTTL time.Duration
	Clock      Clock
}

// Backend stores objects in one caller-selected MinIO bucket.
type Backend struct {
	client     clientAPI
	core       coreAPI
	bucket     string
	prefix     string
	maxLinkTTL time.Duration
	clock      Clock
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

// New validates configuration without contacting MinIO.
func New(config Config) (*Backend, error) {
	if config.Client == nil {
		return nil, storage.NewError("construct", storage.KindInvalid, errors.New("client is nil"))
	}
	return newBackend(config, config.Client, minio.Core{Client: config.Client})
}

func newBackend(config Config, client clientAPI, core coreAPI) (*Backend, error) {
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
		prefixBytes++ // separator after the configured prefix
	}
	// Namespace is at most 63 bytes. Check the longest logical key now so a
	// successfully constructed backend can represent every portable Key.
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

func (b *Backend) Put(ctx context.Context, namespace storage.Namespace, key storage.Key, source io.Reader, opts storage.PutOptions) (storage.Info, error) {
	object, err := b.objectName(namespace, key)
	if err != nil {
		return storage.Info{}, storage.NewError("put", storage.KindInvalid, err)
	}
	if opts.Mode == storage.CreateOnly && opts.Size == nil {
		return b.putUnknownCreateOnly(ctx, namespace, object, source, opts)
	}
	return b.put(ctx, "put", object, source, callerSource, opts.Mode, opts.Size, opts.ContentType, opts.Metadata, nil)
}

func (b *Backend) putUnknownCreateOnly(ctx context.Context, namespace storage.Namespace, finalObject string, source io.Reader, opts storage.PutOptions) (storage.Info, error) {
	id, err := storage.NewStageID()
	if err != nil {
		return storage.Info{}, err
	}
	stageObject, err := b.stageName(namespace, id)
	if err != nil {
		return storage.Info{}, storage.NewError("put", storage.KindInvalid, err)
	}
	expiresAt := b.now().Add(storage.DefaultStageTTL)
	internal := map[string]string{
		stageMarkerKey: stageMarkerValue,
		stageExpiryKey: expiresAt.Format(time.RFC3339Nano),
	}
	// The cryptographically random private identity does not need overwrite
	// semantics from the user-facing contract. In particular, minio-go drops
	// custom conditional headers when completing an unknown-size multipart
	// upload, so advertising CreateOnly here would be false.
	if _, err := b.put(ctx, "put", stageObject, source, callerSource, storage.Replace, nil, opts.ContentType, opts.Metadata, internal); err != nil {
		_ = b.client.RemoveObject(ctx, b.bucket, stageObject, minio.RemoveObjectOptions{})
		return storage.Info{}, err
	}
	// The caller never receives this private StageID, so every later return path
	// must attempt cleanup; an unsuccessful attempt remains bounded by its TTL.
	defer func() {
		_ = b.client.RemoveObject(ctx, b.bucket, stageObject, minio.RemoveObjectOptions{})
	}()

	body, objectInfo, _, err := b.core.GetObject(ctx, b.bucket, stageObject, minio.GetObjectOptions{})
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
	info, err := b.put(ctx, "put", finalObject, body, backendBody, storage.CreateOnly, &size, objectInfo.ContentType, opts.Metadata, nil)
	if err != nil {
		return storage.Info{}, err
	}
	return info, nil
}

func (b *Backend) Open(ctx context.Context, namespace storage.Namespace, key storage.Key) (io.ReadCloser, storage.Info, error) {
	object, err := b.objectName(namespace, key)
	if err != nil {
		return nil, storage.Info{}, storage.NewError("open", storage.KindInvalid, err)
	}
	body, objectInfo, _, err := b.core.GetObject(ctx, b.bucket, object, minio.GetObjectOptions{})
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

func (b *Backend) Head(ctx context.Context, namespace storage.Namespace, key storage.Key) (storage.Info, error) {
	object, err := b.objectName(namespace, key)
	if err != nil {
		return storage.Info{}, storage.NewError("head", storage.KindInvalid, err)
	}
	objectInfo, err := b.client.StatObject(ctx, b.bucket, object, minio.StatObjectOptions{})
	if err != nil {
		return storage.Info{}, mapError("head", err, 0, nil)
	}
	info, err := infoFromObject(objectInfo)
	if err != nil {
		return storage.Info{}, storage.NewError("head", storage.KindInternal, err)
	}
	return info, nil
}

func (b *Backend) Delete(ctx context.Context, namespace storage.Namespace, key storage.Key) error {
	object, err := b.objectName(namespace, key)
	if err != nil {
		return storage.NewError("delete", storage.KindInvalid, err)
	}
	err = b.client.RemoveObject(ctx, b.bucket, object, minio.RemoveObjectOptions{})
	return mapError("delete", err, 0, nil)
}

func (b *Backend) Stage(ctx context.Context, namespace storage.Namespace, source io.Reader, opts storage.StageOptions) (storage.Staged, error) {
	id, err := storage.NewStageID()
	if err != nil {
		return storage.Staged{}, err
	}
	expiresAt := b.now().Add(opts.ExpiresIn).UTC()
	object, err := b.stageName(namespace, id)
	if err != nil {
		return storage.Staged{}, storage.NewError("stage", storage.KindInvalid, err)
	}
	internal := map[string]string{
		stageMarkerKey: stageMarkerValue,
		stageExpiryKey: expiresAt.Format(time.RFC3339Nano),
	}
	info, err := b.put(ctx, "stage", object, source, callerSource, storage.Replace, opts.Size, opts.ContentType, opts.Metadata, internal)
	if err != nil {
		return storage.Staged{}, err
	}
	return storage.Staged{ID: id, Info: info, ExpiresAt: expiresAt}, nil
}

func (b *Backend) Promote(ctx context.Context, namespace storage.Namespace, id storage.StageID, key storage.Key, opts storage.PromoteOptions) (result storage.Info, resultErr error) {
	stageObject, err := b.stageName(namespace, id)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInvalid, err)
	}
	finalObject, err := b.objectName(namespace, key)
	if err != nil {
		return storage.Info{}, storage.NewError("promote", storage.KindInvalid, err)
	}
	claim, err := b.acquireClaim(ctx, "promote", namespace, id)
	if err != nil {
		return storage.Info{}, err
	}
	releaseState := claimStateRetired
	release := true
	committed := false
	defer func() {
		if release {
			if _, err := b.releaseClaim(ctx, "promote", claim, releaseState); err != nil && !committed {
				// A deterministic final failure is retryable only after its active
				// generation was retired. Surface a transition failure instead of
				// promising a retry that would immediately conflict.
				result = storage.Info{}
				resultErr = err
			}
		}
	}()

	body, objectInfo, _, err := b.core.GetObject(ctx, b.bucket, stageObject, minio.GetObjectOptions{})
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
	if !b.now().Before(expiresAt) {
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
	info, err := b.put(ctx, "promote", finalObject, body, backendBody, opts.Mode, &size, objectInfo.ContentType, metadata, nil)
	if err != nil {
		if uncertain(err) {
			release = false
		}
		// In particular, a destination collision keeps the staged upload so the
		// caller can choose another final key or abort it explicitly.
		return storage.Info{}, err
	}
	committed = true

	// The final object is already committed. Staging cleanup is deliberately
	// best effort because this interface has no state for "promoted, cleanup
	// pending" and returning failure would invite an unsafe duplicate retry.
	if err := b.client.RemoveObject(ctx, b.bucket, stageObject, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
		// A committed final object plus a possibly live stage must retain the
		// election claim; otherwise a retry could promote the same StageID to a
		// second key.
		release = false
	} else {
		releaseState = claimStateTerminal
	}
	return info, nil
}

func (b *Backend) Abort(ctx context.Context, namespace storage.Namespace, id storage.StageID) (resultErr error) {
	object, err := b.stageName(namespace, id)
	if err != nil {
		return storage.NewError("abort", storage.KindInvalid, err)
	}
	claim, err := b.acquireClaim(ctx, "abort", namespace, id)
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
			if _, err := b.releaseClaim(ctx, "abort", claim, releaseState); err != nil {
				resultErr = err
			}
		}
	}()
	_, statErr := b.client.StatObject(ctx, b.bucket, object, minio.StatObjectOptions{})
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
	resultErr = mapError("abort", b.client.RemoveObject(ctx, b.bucket, object, minio.RemoveObjectOptions{}), 0, nil)
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

func (b *Backend) CleanupExpired(ctx context.Context, namespace storage.Namespace, opts storage.CleanupOptions) (storage.CleanupResult, error) {
	prefix, err := b.stagePrefix(namespace)
	if err != nil {
		return storage.CleanupResult{}, storage.NewError("cleanup", storage.KindInvalid, err)
	}
	listCtx, cancel := context.WithCancel(ctx)
	objects := b.client.ListObjects(listCtx, b.bucket, minio.ListObjectsOptions{
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
		objectInfo, err := b.client.StatObject(ctx, b.bucket, object.Key, minio.StatObjectOptions{})
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return result, mapError("cleanup", err, 0, nil)
		}
		expiresAt, ok, err := stageExpiry(objectInfo)
		if err != nil || !ok {
			// Never delete an object merely because it happens to be below the
			// private prefix. Only an intact marker authorizes cleanup.
			continue
		}
		if b.now().Before(expiresAt) {
			continue
		}
		claim, err := b.acquireClaim(ctx, "cleanup", namespace, id)
		if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}
		// Acquisition has a preflight check; this second Stat closes the window
		// before deletion and prevents a stale claimant from acting on an absent
		// stage after a terminal claim is reclaimed.
		objectInfo, err = b.client.StatObject(ctx, b.bucket, object.Key, minio.StatObjectOptions{})
		if err != nil {
			mapped := mapError("cleanup", err, 0, nil)
			if errors.Is(mapped, storage.ErrNotFound) {
				deleted, releaseErr := b.releaseClaim(ctx, "cleanup", claim, claimStateTerminal)
				if releaseErr != nil {
					return result, releaseErr
				}
				if deleted {
					result.Removed++
					if result.Removed == opts.Limit {
						result.More = true
						return result, nil
					}
				}
				continue
			}
			if uncertain(mapped) {
				return result, mapped
			}
			if _, releaseErr := b.releaseClaim(ctx, "cleanup", claim, claimStateRetired); releaseErr != nil {
				return result, releaseErr
			}
			return result, mapped
		}
		expiresAt, ok, err = stageExpiry(objectInfo)
		if err != nil || !ok || b.now().Before(expiresAt) {
			if _, releaseErr := b.releaseClaim(ctx, "cleanup", claim, claimStateRetired); releaseErr != nil {
				return result, releaseErr
			}
			continue
		}

		removeErr := mapError("cleanup", b.client.RemoveObject(ctx, b.bucket, object.Key, minio.RemoveObjectOptions{}), 0, nil)
		if errors.Is(removeErr, storage.ErrNotFound) {
			deleted, releaseErr := b.releaseClaim(ctx, "cleanup", claim, claimStateTerminal)
			if releaseErr != nil {
				return result, releaseErr
			}
			if deleted {
				result.Removed++
				if result.Removed == opts.Limit {
					result.More = true
					return result, nil
				}
			}
			continue
		}
		if removeErr != nil {
			if !uncertain(removeErr) {
				if _, releaseErr := b.releaseClaim(ctx, "cleanup", claim, claimStateRetired); releaseErr != nil {
					return result, releaseErr
				}
			}
			return result, removeErr
		}
		if _, err := b.releaseClaim(ctx, "cleanup", claim, claimStateTerminal); err != nil {
			return result, err
		}
		result.Removed++
		if result.Removed == opts.Limit {
			result.More = true
			return result, nil
		}
	}
	if err := b.cleanupExpiredClaims(ctx, namespace, opts.Limit, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (b *Backend) TemporaryURL(ctx context.Context, namespace storage.Namespace, key storage.Key, opts storage.TemporaryURLOptions) (storage.Link, error) {
	if opts.ExpiresIn < time.Second || opts.ExpiresIn > b.maxLinkTTL || opts.ExpiresIn%time.Second != 0 {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInvalid, errors.New("link TTL exceeds backend policy"))
	}
	object, err := b.objectName(namespace, key)
	if err != nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInvalid, err)
	}
	issuedAt := b.now()
	u, err := b.client.PresignedGetObject(ctx, b.bucket, object, opts.ExpiresIn, nil)
	if err != nil {
		return storage.Link{}, mapError("temporary URL", err, 0, nil)
	}
	if u == nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInternal, errors.New("presigned URL is absent"))
	}
	// SigV4 encodes the signing timestamp and expiration in whole seconds.
	// Truncating is conservative and never reports a later expiry than the URL.
	link, err := storage.NewLink(u.String(), issuedAt.Truncate(time.Second).Add(opts.ExpiresIn))
	if err != nil {
		return storage.Link{}, storage.NewError("temporary URL", storage.KindInternal, err)
	}
	return link, nil
}

func (b *Backend) Capabilities() storage.Capabilities {
	return storage.Capabilities{
		CreateOnly:   true,
		Replace:      true,
		Staging:      true,
		TemporaryURL: true,
	}
}

func (b *Backend) put(ctx context.Context, operation, object string, source io.Reader, provenance readProvenance, mode storage.WriteMode, size *int64, contentType string, metadata storage.Metadata, internal map[string]string) (storage.Info, error) {
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
	// exactSizeReader holds back the declared final byte until it has observed
	// EOF. This lets minio-go retain the known size without accepting either
	// early EOF or an unobserved byte N+1.
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
			// Public unknown-size writes are materialized in a private stage first;
			// reaching this branch would otherwise select an unsafe conditional
			// multipart path in minio-go.
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
			// minio-go may issue a Content-Length: 0 request without reading the
			// source. Probe here so a non-empty source cannot become a successful,
			// visibly empty object.
			var probe [1]byte
			if _, err := exact.Read(probe[:]); !errors.Is(err, io.EOF) {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return storage.Info{}, storage.NewError(operation, storage.KindCancelled, ctxErr)
				}
				return storage.Info{}, mapReaderFailure(operation, provenance, err)
			}
		}
	}
	upload, err := b.client.PutObject(ctx, b.bucket, object, uploadSource, objectSize, putOptions)
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
		modifiedAt = b.now()
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

func (r *exactSizeReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		return r.verifyEmpty()
	}
	if r.remaining == 1 {
		return r.readVerifiedFinalByte(p)
	}

	read := p
	if int64(len(read)) >= r.remaining {
		read = read[:r.remaining-1]
	}
	n, err := r.reader.Read(read)
	r.remaining -= int64(n)
	if errors.Is(err, io.EOF) {
		r.err = errSizeMismatch
		return n, r.err
	}
	return n, err
}

func (r *exactSizeReader) verifyEmpty() (int, error) {
	zeroReads := 0
	for {
		n, err := r.reader.Read(r.byte[:])
		switch {
		case n > 0:
			r.err = errSizeMismatch
			return 0, r.err
		case errors.Is(err, io.EOF):
			r.done = true
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

func (r *exactSizeReader) readVerifiedFinalByte(p []byte) (int, error) {
	n, err := r.reader.Read(r.byte[:])
	if n == 0 {
		if errors.Is(err, io.EOF) {
			r.err = errSizeMismatch
			return 0, r.err
		}
		return 0, err
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	last := r.byte[0]
	if !errors.Is(err, io.EOF) {
		if _, err := r.verifyEmpty(); !errors.Is(err, io.EOF) {
			return 0, err
		}
	}
	p[0] = last
	r.remaining = 0
	r.done = true
	return 1, nil
}

func (r *sourceReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.reader.Read(p)
	if r.ctx != nil {
		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return n, ctxErr
		}
	}
	if n > 0 {
		r.consecutiveNoProgress = 0
	} else if err == nil && len(p) > 0 {
		r.consecutiveNoProgress++
		if r.consecutiveNoProgress >= maxConsecutiveNoProgressReads {
			err = io.ErrNoProgress
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

func (b *Backend) objectName(namespace storage.Namespace, key storage.Key) (string, error) {
	return checkedJoin(b.prefix, namespace.Value(), key.Value())
}

func (b *Backend) stageName(namespace storage.Namespace, id storage.StageID) (string, error) {
	return checkedJoin(b.prefix, stageDirectory, namespace.Value(), id.Value())
}

func (b *Backend) stagePrefix(namespace storage.Namespace) (string, error) {
	name, err := checkedJoin(b.prefix, stageDirectory, namespace.Value())
	if err != nil {
		return "", err
	}
	return name + "/", nil
}

func (b *Backend) claimName(namespace storage.Namespace, id storage.StageID) (string, error) {
	return checkedJoin(b.prefix, claimDirectory, namespace.Value(), id.Value())
}

func (b *Backend) claimPrefix(namespace storage.Namespace) (string, error) {
	name, err := checkedJoin(b.prefix, claimDirectory, namespace.Value())
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

func (b *Backend) now() time.Time { return b.clock().UTC() }
