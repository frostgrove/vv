package auditmemory_test

import (
	"testing"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
)

func TestMemoryStoreAdvertisesOnlyImplementedAlphaCapabilities(t *testing.T) {
	log, err := auditmemory.NewLog(auditmemory.LogSpec{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := auditmemory.New(auditmemory.Spec{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	view := store.Capabilities().View()
	if view.Transactions != audit.SupportSupported || view.Idempotency != audit.SupportSupported || view.Reconciliation != audit.SupportSupported || view.ExactInspection != audit.SupportSupported {
		t.Fatalf("base capabilities = %+v", view)
	}
	if view.CrossSystemAtomic != audit.SupportUnsupported || view.Persistence != audit.SupportUnsupported ||
		view.StableSearch != audit.SupportUnsupported ||
		view.AttemptLifecycle != audit.SupportUnsupported || view.Holds != audit.SupportUnsupported || view.PurgePlanning != audit.SupportUnsupported {
		t.Fatalf("unimplemented capability advertised: %+v", view)
	}
}
