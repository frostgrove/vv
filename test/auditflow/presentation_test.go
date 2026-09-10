package auditflow_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/i18n"
	"github.com/frostgrove/vv/port"
)

func TestAuditAlphaI18nPresentsSafeErrorWithoutChangingMachineContract(t *testing.T) {
	snapshot, err := i18n.New(i18n.CatalogSpec{
		Revision:        "audit-alpha-errors/v1",
		SourceLocale:    "en",
		DefaultLocale:   "en",
		Supported:       []string{"en"},
		Required:        []string{"en"},
		DefaultTimeZone: "UTC",
		Modules: []i18n.Module{{Name: "audit", Messages: []i18n.MessageSpec{{
			ID: "unsupported", Revision: "unsupported/v1",
			Description: "Safe public message for an unsupported audit operation.",
			Source:      "audit operation is unavailable", Output: i18n.OutputPlain, Public: true,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := snapshot.ErrorMessages(i18n.ErrorSpec{
		Mappings: []i18n.ErrorMapping{{
			Ladder: string(errs.CodeMethodNotAllowed), Key: i18n.Qualify("audit", "unsupported"),
		}},
		Presentation: i18n.PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}

	machineError := fmt.Errorf("application audit boundary: %w", audit.ErrUnsupported)
	if !errors.Is(machineError, audit.ErrUnsupported) {
		t.Fatal("wrapped audit machine error lost its identity")
	}
	fault := port.FaultOf(machineError)
	if fault.Code != errs.CodeMethodNotAllowed || fault.Message != "" {
		t.Fatalf("audit machine fault = %+v", fault)
	}
	violations := port.Violations(port.WithLocale(context.Background(), "en"), fault, &port.ViolationOptions{Messages: messages})
	if len(violations) != 1 || violations[0].Code != errs.CodeMethodNotAllowed ||
		violations[0].Message != "audit operation is unavailable" || violations[0].MessageLocale != "en" {
		t.Fatalf("localized audit violation = %+v", violations)
	}
	if fault.Message != "" || !errors.Is(machineError, audit.ErrUnsupported) {
		t.Fatalf("presentation mutated audit contract: fault=%+v error=%v", fault, machineError)
	}
}
