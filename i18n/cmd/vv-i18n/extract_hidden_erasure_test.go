package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestExtractUsageRejectsHiddenKeysErasedByCompositeElements(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "slice", body: `return []any{definition}`},
		{name: "map value", body: `return map[string]any{"definition": definition}`},
		{name: "struct field", body: `return struct{ Value any }{Value: definition}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeNamedTestModule(t, directory, "example.test/composite-hidden-key")
			writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func leak(definition i18n.Definition[Payload]) any {
  `+test.body+`
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) {
  _, _ = snapshot.Bind("app.visible")
  _ = leak(replacement)
}
`)
			usage, err := extractUsage(context.Background(), []string{directory}, true)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
				t.Fatalf("composite hidden-key erasure usage = %+v", usage)
			}
		})
	}
}
