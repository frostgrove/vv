//go:build integration

package auditpg

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("FROSTGROVE_AUDITPG_TEST_DSN") == "" {
		_, _ = fmt.Fprintln(os.Stderr, "FROSTGROVE_AUDITPG_TEST_DSN is required for the live auditpg suite")
		os.Exit(2)
	}
	os.Exit(m.Run())
}
