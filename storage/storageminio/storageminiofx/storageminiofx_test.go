package storageminiofx_test

import (
	"testing"

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
