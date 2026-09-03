package storageminiofx_test

import (
	"context"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"go.uber.org/fx"

	vvstorage "github.com/frostgrove/vv/storage"
	"github.com/frostgrove/vv/storage/storageminio"
	"github.com/frostgrove/vv/storage/storageminio/storageminiofx"
)

func settings() storageminiofx.Settings {
	return storageminiofx.Settings{
		Endpoint:  "localhost:9000",
		AccessKey: "key",
		SecretKey: "secret",
		Bucket:    "documents",
	}
}

func TestBothTheContractAndTheBackendAreResolvable(t *testing.T) {
	err := fx.ValidateApp(
		storageminiofx.Module(settings()),
		fx.Invoke(func(vvstorage.Backend, *storageminio.Backend, *minio.Client) {}),
	)
	if err != nil {
		t.Fatalf("the storage graph is incomplete: %v", err)
	}
}

func TestSomethingThisModuleDoesNotProvideIsRefused(t *testing.T) {
	err := fx.ValidateApp(
		storageminiofx.Module(settings()),
		fx.Invoke(func(*minio.Core) {}),
	)
	if err == nil {
		t.Fatal("a graph resolved a *minio.Core nobody provides, so the check above proves nothing")
	}
}

func TestBuildingTheClientTouchesNothing(t *testing.T) {
	unreachable := settings()
	unreachable.Endpoint = "127.0.0.1:1"

	if _, err := storageminiofx.NewClient(unreachable); err != nil {
		t.Fatalf("constructing the client reached for the server: %v", err)
	}

	malformed := settings()
	malformed.Endpoint = "http://host:9000/path"
	if _, err := storageminiofx.NewClient(malformed); err == nil {
		t.Fatal("an endpoint MinIO cannot use was accepted, so nothing is validated at construction at all")
	}
}

func TestSettingsNobodyFilledInReachTheStoreOverTLS(t *testing.T) {
	client, err := storageminiofx.NewClient(settings())
	if err != nil {
		t.Fatalf("settings carrying only the required fields were refused: %v", err)
	}
	if scheme := client.EndpointURL().Scheme; scheme != "https" {
		t.Fatalf("a Settings whose transport nobody set reaches the object store over %s, so its credentials do", scheme)
	}
}

func TestPlaintextHasToBeAskedForByName(t *testing.T) {
	asked := settings()
	asked.Transport = storageminiofx.TransportPlaintext

	client, err := storageminiofx.NewClient(asked)
	if err != nil {
		t.Fatalf("a deployment that asked for plaintext was refused: %v", err)
	}
	if scheme := client.EndpointURL().Scheme; scheme != "http" {
		t.Fatalf("plaintext was asked for and the client speaks %s, so the setting does nothing", scheme)
	}
}

func TestATransportNobodyDefinedIsRefusedRatherThanGuessed(t *testing.T) {
	wrong := settings()
	wrong.Transport = "ssl"

	if _, err := storageminiofx.NewClient(wrong); err == nil {
		t.Fatal("a transport this module has no meaning for was accepted, and a typo therefore chooses one of the two answers silently")
	}
}

func unreachable() storageminiofx.Settings {
	// Nothing listens here, so whether the start touches the object store at all
	// is the difference between the two policies and not a matter of timing.
	asked := settings()
	asked.Endpoint = "127.0.0.1:1"
	asked.Transport = storageminiofx.TransportPlaintext
	return asked
}

func started(t *testing.T, asked storageminiofx.Settings) error {
	t.Helper()
	application := fx.New(storageminiofx.Module(asked), fx.NopLogger)
	if err := application.Err(); err != nil {
		t.Fatalf("the graph did not build: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := application.Start(ctx)
	if err == nil {
		_ = application.Stop(ctx)
	}
	return err
}

func TestSettingsNobodyFilledInWriteNothingToTheObjectStore(t *testing.T) {
	if err := started(t, unreachable()); err != nil {
		t.Fatalf("a module nobody asked to create a bucket still went to the object store at start: %v", err)
	}
}

func TestCreatingABucketHasToBeAskedForByName(t *testing.T) {
	asked := unreachable()
	asked.Bucketing = storageminiofx.BucketOnDemand

	if err := started(t, asked); err == nil {
		t.Fatal("a module asked to create the bucket started against an object store that is not there, so the setting does nothing")
	}
}

func TestABucketPolicyNobodyDefinedIsRefusedRatherThanGuessed(t *testing.T) {
	wrong := settings()
	wrong.Bucketing = "ensure"

	if _, err := storageminiofx.NewClient(wrong); err == nil {
		t.Fatal("a bucket policy this module has no meaning for was accepted, so a typo silently means 'do not create'")
	}
}
