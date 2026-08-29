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

// A bounded context depends on the contract and not on the adapter, so what this
// graph has to resolve is [vvstorage.Backend]. The concrete backend is offered
// too, because administering a bucket is not an object operation and is
// therefore not on the interface.
func TestBothTheContractAndTheBackendAreResolvable(t *testing.T) {
	err := fx.ValidateApp(
		storageminiofx.Module(settings()),
		fx.Invoke(func(vvstorage.Backend, *storageminio.Backend, *minio.Client) {}),
	)
	if err != nil {
		t.Fatalf("the storage graph is incomplete: %v", err)
	}
}

// The control on the test above: a component this module does not provide has to
// fail validation, or the check proves nothing.
func TestSomethingThisModuleDoesNotProvideIsRefused(t *testing.T) {
	err := fx.ValidateApp(
		storageminiofx.Module(settings()),
		fx.Invoke(func(*minio.Core) {}),
	)
	if err == nil {
		t.Fatal("a graph resolved a *minio.Core nobody provides, so the check above proves nothing")
	}
}

// A client is built without touching the network, so a wrong endpoint surfaces
// in the start-up hook with everything else that is wrong rather than during
// dependency construction, where it would read as a wiring problem.
func TestBuildingTheClientTouchesNothing(t *testing.T) {
	unreachable := settings()
	unreachable.Endpoint = "127.0.0.1:1"

	if _, err := storageminiofx.NewClient(unreachable); err != nil {
		t.Fatalf("constructing the client reached for the server: %v", err)
	}

	// The control. An endpoint that cannot be parsed at all is still refused
	// here, so the case above is the constructor not connecting rather than the
	// constructor not checking anything.
	malformed := settings()
	malformed.Endpoint = "http://host:9000/path"
	if _, err := storageminiofx.NewClient(malformed); err == nil {
		t.Fatal("an endpoint MinIO cannot use was accepted, so nothing is validated at construction at all")
	}
}
