package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestExtractUsageDowngradesEveryEscapedI18nCapability(t *testing.T) {
	tests := []struct {
		name   string
		extra  map[string]string
		source string
	}{
		{
			name: "returned bound method",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Bind = func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
func leak(snapshot *i18n.Snapshot) Bind {
  _, _ = snapshot.Bind("app.visible")
  return snapshot.Bind
}
`,
		},
		{
			name: "returned method expression",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Bind = func(*i18n.Snapshot, i18n.Key, ...i18n.Argument) (i18n.Message, error)
func leak(snapshot *i18n.Snapshot) Bind {
  _, _ = snapshot.Bind("app.visible")
  return (*i18n.Snapshot).Bind
}
`,
		},
		{
			name: "returned binder interface",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Binder interface { Bind(i18n.Key, ...i18n.Argument) (i18n.Message, error) }
func leak(snapshot *i18n.Snapshot) Binder {
  _, _ = snapshot.Bind("app.visible")
  return snapshot
}
`,
		},
		{
			name: "returned erased snapshot",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func leak(snapshot *i18n.Snapshot) any {
  _, _ = snapshot.Bind("app.visible")
  return any(snapshot)
}
`,
		},
		{
			name: "field storage",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type holder struct { Snapshot *i18n.Snapshot }
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  value := holder{}
  value.Snapshot = snapshot
  _ = value
}
`,
		},
		{
			name: "map storage",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  values := map[string]*i18n.Snapshot{}
  values["snapshot"] = snapshot
}
`,
		},
		{
			name: "reflection",
			source: `package sample
import (
  "reflect"
  "github.com/frostgrove/vv/i18n"
)
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  _ = reflect.ValueOf(snapshot.Bind)
}
`,
		},
		{
			name: "unsafe",
			source: `package sample
import (
  "unsafe"
  "github.com/frostgrove/vv/i18n"
)
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  _ = unsafe.Pointer(snapshot)
}
`,
		},
		{
			name: "external generic helper",
			extra: map[string]string{
				"helper/helper.go": `package helper
func Keep[T any](value T) { _ = value }
`,
			},
			source: `package sample
import (
  "example.test/usage-edge/helper"
  "github.com/frostgrove/vv/i18n"
)
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  helper.Keep[any](any(snapshot.Bind))
}
`,
		},
		{
			name: "local erased helper",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func keep(any) {}
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  keep(snapshot)
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeNamedTestModule(t, directory, "example.test/usage-edge")
			writeTestFile(t, filepath.Join(directory, "use.go"), test.source)
			for path, content := range test.extra {
				writeTestFile(t, filepath.Join(directory, filepath.FromSlash(path)), content)
			}
			usage, err := extractUsage(context.Background(), []string{directory}, true)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
				t.Fatalf("escaped capability usage = %+v", usage)
			}
		})
	}
}

func TestExtractUsageKeepsCompleteForLocalTypedCapabilityCallbacks(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/usage-control")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Binder interface { Bind(i18n.Key, ...i18n.Argument) (i18n.Message, error) }
type Bind = func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
type BindMethod = func(*i18n.Snapshot, i18n.Key, ...i18n.Argument) (i18n.Message, error)
func snapshotCallback(snapshot *i18n.Snapshot) { _, _ = snapshot.Bind("app.snapshot_callback") }
func bindCallback(bind Bind) { _, _ = bind("app.bind_callback") }
func methodCallback(bind BindMethod, snapshot *i18n.Snapshot) { _, _ = bind(snapshot, "app.method_callback") }
func use(snapshot *i18n.Snapshot, binder Binder) {
  snapshotCallback(snapshot)
  bindCallback(snapshot.Bind)
  methodCallback((*i18n.Snapshot).Bind, snapshot)
  func(bind Bind) { _, _ = bind("app.literal_callback") }(snapshot.Bind)
  _, _ = binder.Bind("app.interface")
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []i18n.Key{"app.bind_callback", "app.interface", "app.literal_callback", "app.method_callback", "app.snapshot_callback"}
	if !slices.Equal(usage.Keys, want) || !usage.Complete {
		t.Fatalf("typed local callback usage = %+v, want %v", usage, want)
	}
}

func TestExtractUsageTrustsDefinitionFactoryFieldsOnlyWhenEveryPathIsKnown(t *testing.T) {
	tests := []struct {
		name      string
		generated bool
		build     string
		useField  string
		complete  bool
		wantKeys  []i18n.Key
	}{
		{
			name: "generated replacement is not trusted", generated: true,
			build: `func Build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.decoy")
  result := Bindings{Known: definition}
  result.Known = replacement
  return result
}`,
			useField: "Known", wantKeys: []i18n.Key{"app.factory.decoy"},
		},
		{
			name: "mixed return paths are not trusted",
			build: `func Build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload], choose bool) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.mixed")
  if choose { return Bindings{Known: definition} }
  return Bindings{Known: replacement}
}`,
			useField: "Known", wantKeys: []i18n.Key{"app.factory.mixed"},
		},
		{
			name: "address escape is not trusted",
			build: `func Build(snapshot *i18n.Snapshot, escape func(*Bindings)) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.address")
  result := Bindings{Known: definition}
  escape(&result)
  return result
}`,
			useField: "Known", wantKeys: []i18n.Key{"app.factory.address"},
		},
		{
			name: "non generated all path factory is trusted",
			build: `func Build(snapshot *i18n.Snapshot) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.safe")
  return Bindings{Known: definition}
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.safe"},
		},
		{
			name: "trust is independent per result field",
			build: `func Build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.field")
  return Bindings{Known: definition, Unknown: replacement}
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.field"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeNamedTestModule(t, directory, "example.test/factory-edge")
			marker := ""
			if test.generated {
				marker = "// Code generated by vv-i18n test. DO NOT EDIT.\n"
			}
			source := marker + `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Bindings struct {
  Known i18n.Definition[Payload]
  Unknown i18n.Definition[Payload]
}
` + test.build + `
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload], choose bool, escape func(*Bindings)) {
  _, _ = Build(`
			arguments := "snapshot"
			switch test.name {
			case "generated replacement is not trusted", "trust is independent per result field":
				arguments += ", replacement"
			case "mixed return paths are not trusted":
				arguments += ", replacement, choose"
			case "address escape is not trusted":
				arguments += ", escape"
			}
			source += arguments + `).` + test.useField + `.Bind(Payload{})
}
`
			writeTestFile(t, filepath.Join(directory, "use.go"), source)
			usage, err := extractUsage(context.Background(), []string{directory}, true)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(usage.Keys, test.wantKeys) || usage.Complete != test.complete {
				t.Fatalf("factory usage = %+v, want keys %v and complete %t\nsource:\n%s", usage, test.wantKeys, test.complete, strings.TrimSpace(source))
			}
		})
	}
}
