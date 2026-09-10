package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestExtractUsageDowngradesSelectedPackageWithOpaqueBuildInputs(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/opaque-build-input")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
func hidden()
func use(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  hidden()
}
`)
	writeTestFile(t, filepath.Join(directory, "hidden.s"), `#include "textflag.h"
TEXT ·hidden(SB),NOSPLIT,$0-0
  RET
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
		t.Fatalf("opaque build input usage = %+v", usage)
	}
}
