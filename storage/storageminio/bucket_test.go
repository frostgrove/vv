package storageminio

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/storage"
	"github.com/minio/minio-go/v7"
)

type fakeBucketAdmin struct {
	exists  []bool
	existsN int
	made    int
	makeErr error
	statErr error
}

func (this *fakeBucketAdmin) BucketExists(context.Context, string) (bool, error) {
	if this.statErr != nil {
		return false, this.statErr
	}
	answer := false
	if this.existsN < len(this.exists) {
		answer = this.exists[this.existsN]
	} else if len(this.exists) > 0 {
		answer = this.exists[len(this.exists)-1]
	}
	this.existsN++
	return answer, nil
}

func (this *fakeBucketAdmin) MakeBucket(context.Context, string, minio.MakeBucketOptions) error {
	this.made++
	return this.makeErr
}

func newAdminBackend(t *testing.T, admin bucketAdmin) *Backend {
	t.Helper()
	backend, err := newBackend(&Config{Bucket: "test-bucket"}, &fakeClient{}, &fakeCore{})
	if err != nil {
		t.Fatalf("building the backend failed: %v", err)
	}
	backend.admin = admin
	return backend
}

func TestEnsureBucketCreatesAMissingBucket(t *testing.T) {
	admin := &fakeBucketAdmin{exists: []bool{false}}
	if err := newAdminBackend(t, admin).EnsureBucket(context.Background()); err != nil {
		t.Fatalf("ensuring a missing bucket failed: %v", err)
	}
	if admin.made != 1 {
		t.Fatalf("the bucket was not created: MakeBucket ran %d times", admin.made)
	}
}

func TestEnsureBucketLeavesAnExistingBucketAlone(t *testing.T) {
	admin := &fakeBucketAdmin{exists: []bool{true}}
	if err := newAdminBackend(t, admin).EnsureBucket(context.Background()); err != nil {
		t.Fatalf("ensuring an existing bucket failed: %v", err)
	}
	if admin.made != 0 {
		t.Fatal("an existing bucket was created again")
	}
}

// Two replicas starting together both want the bucket to exist. The one that
// loses the create must not fail start-up over a bucket that is now there.
func TestEnsureBucketAcceptsABucketAnotherReplicaCreated(t *testing.T) {
	admin := &fakeBucketAdmin{
		exists:  []bool{false, true},
		makeErr: errors.New("bucket already owned by you"),
	}
	if err := newAdminBackend(t, admin).EnsureBucket(context.Background()); err != nil {
		t.Fatalf("a bucket created by another replica was reported as a failure: %v", err)
	}
}

func TestEnsureBucketReportsACreateThatDidNotHappen(t *testing.T) {
	admin := &fakeBucketAdmin{
		exists:  []bool{false, false},
		makeErr: errors.New("access denied"),
	}
	err := newAdminBackend(t, admin).EnsureBucket(context.Background())
	if err == nil {
		t.Fatal("a bucket that was neither found nor created was reported as success")
	}
}

func TestEnsureBucketRefusesABackendWithNoClient(t *testing.T) {
	backend, err := newBackend(&Config{Bucket: "test-bucket"}, &fakeClient{}, &fakeCore{})
	if err != nil {
		t.Fatalf("building the backend failed: %v", err)
	}
	if err := backend.EnsureBucket(context.Background()); !errors.Is(err, storage.ErrInternal) {
		t.Fatalf("a backend built without a client answered %v", err)
	}
}
