// Package storageminiofx puts a MinIO-backed object store in an fx graph.
//
//	fx.Options(storageminiofx.Module(storageminiofx.Settings{
//		Endpoint:  configuration.Storage.Endpoint,
//		AccessKey: configuration.Storage.AccessKey,
//		SecretKey: configuration.Storage.SecretKey,
//		Bucket:    configuration.Storage.Bucket,
//	}))
//
// It provides a [storage.Backend] and not a Store, deliberately. A Store is
// scoped to one namespace, and a namespace belongs to whichever bounded context
// owns those objects — so the context constructs its own Store over this backend
// and nothing here has to know what the keys mean. Credentials, the bucket and
// the connection are infrastructure; what is kept in them is not.
//
// # Why this is a module and not a package of the library
//
// The framework holds no container ([[D-037]]). What it holds here is an adapter
// to one the consumer chose ([[D-074]]).
package storageminiofx

import (
	"context"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/fx"

	vvstorage "github.com/frostgrove/vv/storage"
	"github.com/frostgrove/vv/storage/storageminio"
)

// Settings is what a deployment configures the object store with.
//
// It is passed rather than resolved from the graph, because a deployment keeps
// these values inside a configuration struct of its own shape and this package
// must not have an opinion about that shape.
type Settings struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool

	Bucket string
	Prefix string
	// LinkTTL caps how long a temporary URL this backend signs may live.
	LinkTTL time.Duration

	// SkipEnsureBucket leaves the bucket alone at start-up.
	//
	// The default is to create it if it is missing, because the alternative —
	// carry on and find out on the first upload — turns a deployment that is not
	// finished into a failed write an hour later with a worse error. A
	// deployment whose credentials may not administer buckets sets this and
	// takes that trade.
	SkipEnsureBucket bool
}

// Module wires the backend.
func Module(settings Settings) fx.Option {
	return fx.Module("vv.storageminio",
		fx.Provide(
			func() (*minio.Client, error) { return NewClient(settings) },
			func(client *minio.Client) (*storageminio.Backend, error) { return NewBackend(settings, client) },
			// What every consumer depends on, so a bounded context holds the
			// contract rather than the MinIO adapter.
			func(backend *storageminio.Backend) vvstorage.Backend { return backend },
		),
		fx.Invoke(func(lifecycle fx.Lifecycle, backend *storageminio.Backend) {
			if settings.SkipEnsureBucket {
				return
			}
			ensureOnStart(lifecycle, settings, backend)
		}),
	)
}

// NewClient builds the client.
//
// It performs no network call: MinIO's constructor only validates, so a wrong
// endpoint surfaces in the start-up hook with everything else that is wrong
// rather than during dependency construction, where it would read as a wiring
// problem.
func NewClient(settings Settings) (*minio.Client, error) {
	client, err := minio.New(settings.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(settings.AccessKey, settings.SecretKey, ""),
		Secure: settings.UseSSL,
		Region: settings.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storageminiofx: configuring object storage: %w", err)
	}
	return client, nil
}

// NewBackend returns the concrete backend, because EnsureBucket is not part of
// [vvstorage.Backend]: administering a bucket is not an object operation.
func NewBackend(settings Settings, client *minio.Client) (*storageminio.Backend, error) {
	backend, err := storageminio.New(&storageminio.Config{
		Client:     client,
		Bucket:     settings.Bucket,
		Prefix:     settings.Prefix,
		MaxLinkTTL: settings.LinkTTL,
	})
	if err != nil {
		return nil, fmt.Errorf("storageminiofx: opening object storage: %w", err)
	}
	return backend, nil
}

// ensureOnStart makes the bucket exist, or the process does not start. It also
// proves the endpoint and the credentials, which is the other half of what could
// be wrong and the half that is otherwise found on the first upload.
func ensureOnStart(lifecycle fx.Lifecycle, settings Settings, backend *storageminio.Backend) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := backend.EnsureBucket(ctx); err != nil {
				return fmt.Errorf("storageminiofx: object storage at %s: %w", settings.Endpoint, err)
			}
			return nil
		},
	})
}
