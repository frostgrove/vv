package audit_test

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/audit"
)

func TestRetentionRulesRequireCanonicalPositivePeriods(t *testing.T) {
	forever := audit.KeepForever("security")
	period := audit.KeepFor("operations", audit.CalendarPeriod{Years: 7, Months: 6})
	rules := audit.RetentionRules(forever, period)
	if len(rules) != 2 {
		t.Fatalf("rules = %d", len(rules))
	}
	rules[0] = period
	again := audit.RetentionRules(forever)
	if len(again) != 1 {
		t.Fatalf("fresh rules = %d", len(again))
	}
	for _, invalid := range []audit.CalendarPeriod{
		{},
		{Months: 12},
		{Years: 100, Days: 1},
		{Years: 101},
	} {
		if _, err := audit.TryKeepFor("operations", invalid); !errors.Is(err, audit.ErrDeclaration) {
			t.Fatalf("period %+v = %v", invalid, err)
		}
	}
}

func TestSignatureDescriptionAcceptsTheBuiltInProtocolSpelling(t *testing.T) {
	policy := audit.RequireSignature(audit.SignatureDescription{
		Algorithm: "hmac-sha256",
		Profile:   "frostgrove.audit.signature.v1",
		KeyID:     "audit-key-2026",
	})
	_ = policy
}
