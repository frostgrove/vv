package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestExtractUsageRejectsSelectedErasureOfUnknownHiddenKeyValues(t *testing.T) {
	tests := []struct {
		name      string
		valueType string
	}{
		{name: "definition", valueType: "i18n.Definition[Payload]"},
		{name: "descriptor", valueType: "i18n.Descriptor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeNamedTestModule(t, directory, "example.test/selected-hidden-erasure")
			writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
var escaped any
func keep(value any) { escaped = value }
func use(snapshot *i18n.Snapshot, value `+test.valueType+`) {
  _, _ = snapshot.Bind("app.visible")
  keep(value)
}
`)
			usage, err := extractUsage(context.Background(), []string{directory}, true)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
				t.Fatalf("selected hidden-key erasure usage = %+v", usage)
			}
		})
	}
}
