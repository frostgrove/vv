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

type Settings struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool

	Bucket string
	Prefix string

	LinkTTL time.Duration

	SkipEnsureBucket bool
}

func Module(settings Settings) fx.Option {
	return fx.Module("vv.storageminio",
		fx.Provide(
			func() (*minio.Client, error) { return NewClient(settings) },
			func(client *minio.Client) (*storageminio.Backend, error) { return NewBackend(settings, client) },

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
