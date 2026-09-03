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

// Transport says how this deployment reaches the object store. The zero value is
// TLS on purpose: a Settings whose author forgot the field gets the answer that is
// safe in production, and plaintext has to be asked for by name. The field it
// replaced was `UseSSL bool`, whose zero value shipped credentials over plain HTTP.
type Transport string

const (
	TransportTLS       Transport = ""
	TransportPlaintext Transport = "plaintext"
)

// BucketPolicy says what this module may do about a bucket that is not there. The
// zero value creates nothing: making a bucket is a write to somebody's object
// store, and a deployment that did not ask for it should fail loudly against the
// bucket it expected rather than quietly start against a new empty one. The field
// it replaced was `SkipEnsureBucket bool`, which made creation the default and the
// refusal the opt-out.
type BucketPolicy string

const (
	BucketMustExist BucketPolicy = ""
	BucketOnDemand  BucketPolicy = "create"
)

type Settings struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	Transport Transport

	Bucket string
	Prefix string

	LinkTTL time.Duration

	Bucketing BucketPolicy
}

func (this Settings) secure() bool { return this.Transport != TransportPlaintext }

func (this Settings) validate() error {
	switch this.Transport {
	case TransportTLS, TransportPlaintext:
	default:
		return fmt.Errorf("storageminiofx: transport %q is neither %q nor %q",
			this.Transport, TransportTLS, TransportPlaintext)
	}
	switch this.Bucketing {
	case BucketMustExist, BucketOnDemand:
	default:
		return fmt.Errorf("storageminiofx: bucket policy %q is neither %q nor %q",
			this.Bucketing, BucketMustExist, BucketOnDemand)
	}
	return nil
}

func Module(settings Settings) fx.Option {
	return fx.Module("vv.storageminio",
		fx.Provide(
			func() (*minio.Client, error) { return NewClient(settings) },
			func(client *minio.Client) (*storageminio.Backend, error) { return NewBackend(settings, client) },

			func(backend *storageminio.Backend) vvstorage.Backend { return backend },
		),
		fx.Invoke(func(lifecycle fx.Lifecycle, backend *storageminio.Backend) {
			if settings.Bucketing != BucketOnDemand {
				return
			}
			ensureOnStart(lifecycle, settings, backend)
		}),
	)
}

func NewClient(settings Settings) (*minio.Client, error) {
	if err := settings.validate(); err != nil {
		return nil, err
	}
	client, err := minio.New(settings.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(settings.AccessKey, settings.SecretKey, ""),
		Secure: settings.secure(),
		Region: settings.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storageminiofx: configuring object storage: %w", err)
	}
	return client, nil
}

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
