package storageminio

import (
	"context"
	"errors"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
)

type bucketAdmin interface {
	BucketExists(context.Context, string) (bool, error)
	MakeBucket(context.Context, string, minio.MakeBucketOptions) error
}

// EnsureBucket makes the configured bucket exist, and is the only call in this
// package that contacts MinIO outside an object operation.
//
// An application calls it once at start-up. Every other method assumes the
// bucket is there and answers a missing one as a failed write, which arrives
// as a document somebody could not save rather than as a deployment that is not
// finished.
//
// Creating one it did not find is not the same bucket an operator would have
// provisioned: it has no versioning, no retention and no replication, and it
// looks identical to one that has. That difference belongs to whoever runs the
// deployment, so it is worth being deliberate about calling this in production.
//
// A bucket that appeared between the check and the create is success: two
// replicas starting together both wanted it to exist, and it does.
func (this *Backend) EnsureBucket(ctx context.Context) error {
	if this.admin == nil {
		return storage.NewError("ensure bucket", storage.KindInternal,
			errors.New("this backend was built without a MinIO client"))
	}
	exists, err := this.admin.BucketExists(ctx, this.bucket)
	if err != nil {
		return mapError("ensure bucket", err, storage.CreateOnly, nil)
	}
	if exists {
		return nil
	}
	if err := this.admin.MakeBucket(ctx, this.bucket, minio.MakeBucketOptions{}); err != nil {
		if exists, checkErr := this.admin.BucketExists(ctx, this.bucket); checkErr == nil && exists {
			return nil
		}
		return mapError("ensure bucket", err, storage.CreateOnly, nil)
	}
	return nil
}
