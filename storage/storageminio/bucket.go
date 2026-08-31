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
