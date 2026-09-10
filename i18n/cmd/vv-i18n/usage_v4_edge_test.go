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
			name: "returned callable producing snapshot",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func leak(head i18n.Head, snapshot *i18n.Snapshot) func() *i18n.Snapshot {
  _, _ = snapshot.Bind("app.visible")
  return head.Snapshot
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
			name: "returned reassigned erased binder",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func leak(snapshot *i18n.Snapshot) any {
  _, _ = snapshot.Bind("app.visible")
  var escaped any = "safe"
  escaped = snapshot.Bind
  escaped = snapshot.Bind
  return escaped
}
`,
		},
		{
			name: "bare named return",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func leak(snapshot *i18n.Snapshot) (escaped *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  escaped = snapshot
  return
}
`,
		},
		{
			name: "bare named erased return",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
func leak(snapshot *i18n.Snapshot) (escaped any) {
  _, _ = snapshot.Bind("app.visible")
  escaped = snapshot
  return
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
		{
			name: "selected unconstrained generic escape",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
var escaped any
func keep[T any](value T) { escaped = value }
func leak(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  keep(snapshot)
}
`,
		},
		{
			name: "generic type set erased return",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Snapshotish interface { *i18n.Snapshot }
func leak[T Snapshotish](value T, snapshot *i18n.Snapshot) any {
  _, _ = snapshot.Bind("app.visible")
  return value
}
`,
		},
		{
			name: "returned unknown definition bind",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func leak(snapshot *i18n.Snapshot, definition i18n.Definition[Payload]) any {
  _, _ = snapshot.Bind("app.visible")
  return definition.Bind
}
`,
		},
		{
			name: "returned erased unknown definition",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func leak(snapshot *i18n.Snapshot, definition i18n.Definition[Payload]) any {
  _, _ = snapshot.Bind("app.visible")
  return any(definition)
}
`,
		},
		{
			name: "stored unknown definition bind",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type holder struct { Bind func(Payload) (i18n.Message, error) }
func leak(snapshot *i18n.Snapshot, definition i18n.Definition[Payload]) {
  _, _ = snapshot.Bind("app.visible")
  value := holder{}
  value.Bind = definition.Bind
}
`,
		},
		{
			name: "unknown definition external call",
			source: `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func leak(snapshot *i18n.Snapshot, definition i18n.Definition[Payload], keep func(any)) {
  _, _ = snapshot.Bind("app.visible")
  keep(definition)
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

func TestExtractUsageKeepsCompleteForUnrelatedNamedReturn(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/named-return-control")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
func use(snapshot *i18n.Snapshot) (value any, err error) {
  _, _ = snapshot.Bind("app.visible")
  value = "safe"
  return
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || !usage.Complete {
		t.Fatalf("unrelated named return usage = %+v", usage)
	}
}

func TestExtractUsageRejectsIndirectCallableMutation(t *testing.T) {
	tests := map[string]string{
		"definition bind address": `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func use(snapshot *i18n.Snapshot, replacement func(Payload) (i18n.Message, error)) {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.address.definition")
  bind := definition.Bind
  pointer := &bind
  *pointer = replacement
  _, _ = bind(Payload{})
}
`,
		"qualify address": `package sample
import "github.com/frostgrove/vv/i18n"
func use(snapshot *i18n.Snapshot, replacement func(string, string) i18n.Key) {
  qualify := i18n.Qualify
  pointer := &qualify
  *pointer = replacement
  _, _ = snapshot.Bind(qualify("app", "decoy"))
}
`,
		"constructor address": `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func use(snapshot *i18n.Snapshot, replacement func(*i18n.Snapshot, i18n.Key) (i18n.Definition[Payload], error)) {
  create := i18n.NewStructDefinition[Payload]
  pointer := &create
  *pointer = replacement
  _, _ = create(snapshot, "app.address.constructor")
}
`,
		"selected function range": `package sample
import "github.com/frostgrove/vv/i18n"
func Render(snapshot *i18n.Snapshot) { _, _ = snapshot.Bind("app.range.decoy") }
func use(snapshot *i18n.Snapshot, replacements []func(*i18n.Snapshot)) {
  render := Render
  for _, render = range replacements {}
  render(snapshot)
}
`,
		"qualify tuple reassignment": `package sample
import "github.com/frostgrove/vv/i18n"
func pair(replacement func(string, string) i18n.Key) (int, func(string, string) i18n.Key) {
  return 0, replacement
}
func use(snapshot *i18n.Snapshot, replacement func(string, string) i18n.Key) {
  qualify := i18n.Qualify
  _, qualify = pair(replacement)
  _, _ = snapshot.Bind(qualify("app", "decoy"))
}
`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeNamedTestModule(t, directory, "example.test/callable-mutation")
			writeTestFile(t, filepath.Join(directory, "use.go"), source)
			usage, err := extractUsage(context.Background(), []string{directory}, true)
			if err == nil && usage.Complete {
				t.Fatalf("mutated callable was treated as complete: %+v", usage)
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
func genericCallback[B Binder](binder B) { _, _ = binder.Bind("app.generic_callback") }
func use(snapshot *i18n.Snapshot, binder Binder) {
  snapshotCallback(snapshot)
  bindCallback(snapshot.Bind)
  methodCallback((*i18n.Snapshot).Bind, snapshot)
  func(bind Bind) { _, _ = bind("app.literal_callback") }(snapshot.Bind)
  _, _ = binder.Bind("app.interface")
  genericCallback(snapshot)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []i18n.Key{"app.bind_callback", "app.generic_callback", "app.interface", "app.literal_callback", "app.method_callback", "app.snapshot_callback"}
	if !slices.Equal(usage.Keys, want) || !usage.Complete {
		t.Fatalf("typed local callback usage = %+v, want %v", usage, want)
	}
}

func TestExtractUsageKeepsCompleteAcrossSelectedTypedCapabilityGraph(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/selected-graph")
	writeTestFile(t, filepath.Join(directory, "messages", "messages.go"), `package messages
import "github.com/frostgrove/vv/i18n"
type Bind = func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
func Render(snapshot *i18n.Snapshot) { _, _ = snapshot.Bind("app.sibling") }
func RenderWith(bind Bind) { _, _ = bind("app.callback") }
`)
	writeTestFile(t, filepath.Join(directory, "bridge", "bridge.go"), `package bridge
import (
  "example.test/selected-graph/messages"
  "github.com/frostgrove/vv/i18n"
)
type Bind = func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
func Render(snapshot *i18n.Snapshot) { messages.Render(snapshot) }
func RenderWith(bind Bind) { messages.RenderWith(bind) }
`)
	writeTestFile(t, filepath.Join(directory, "application", "use.go"), `package application
import (
  "example.test/selected-graph/bridge"
  "example.test/selected-graph/messages"
  "github.com/frostgrove/vv/i18n"
)
type Render func(*i18n.Snapshot)
type Bind func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
func use(snapshot *i18n.Snapshot) {
  render := bridge.Render
  render(snapshot)
  renderWith := bridge.RenderWith
  renderWith(snapshot.Bind)
  var lateRender func(*i18n.Snapshot)
  lateRender = bridge.Render
  lateRender(snapshot)
  var lateRenderWith func(bridge.Bind)
  lateRenderWith = bridge.RenderWith
  lateRenderWith(snapshot.Bind)
  convertedRender := Render(bridge.Render)
  convertedRender(snapshot)
  convertedBind := Bind(snapshot.Bind)
  bridge.RenderWith(convertedBind)
  messages.Render(snapshot)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []i18n.Key{"app.callback", "app.sibling"}
	if !slices.Equal(usage.Keys, want) || !usage.Complete {
		t.Fatalf("selected capability graph usage = %+v, want %v", usage, want)
	}
}

func TestExtractUsageRejectsCapabilityCallsOutsideSelectedSourceGraph(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/partial-graph")
	writeTestFile(t, filepath.Join(directory, "messages", "messages.go"), `package messages
import "github.com/frostgrove/vv/i18n"
const Ready = true
var Render func(*i18n.Snapshot)
`)
	usePath := filepath.Join(directory, "application", "use.go")
	writeTestFile(t, usePath, `package application
import (
  "example.test/partial-graph/messages"
  "github.com/frostgrove/vv/i18n"
)
func use(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  _ = messages.Ready
}
`)
	baseline, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(baseline.Keys, []i18n.Key{"app.visible"}) || !baseline.Complete {
		t.Fatalf("unselected graph baseline usage = %+v", baseline)
	}
	writeTestFile(t, usePath, `package application
import (
  "example.test/partial-graph/messages"
  "github.com/frostgrove/vv/i18n"
)
func use(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  messages.Render(snapshot)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
		t.Fatalf("unselected capability graph usage = %+v", usage)
	}
}

func TestExtractUsageSelectedSiblingCannotHideExternalCapabilityEscape(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/selected-escape")
	writeTestFile(t, filepath.Join(directory, "messages", "messages.go"), `package messages
import (
  "fmt"
  "github.com/frostgrove/vv/i18n"
)
func Render(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.selected_escape")
  _ = fmt.Sprint(snapshot)
}
`)
	writeTestFile(t, filepath.Join(directory, "application", "use.go"), `package application
import (
  "example.test/selected-escape/messages"
  "github.com/frostgrove/vv/i18n"
)
func use(snapshot *i18n.Snapshot) { messages.Render(snapshot) }
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.selected_escape"}) || usage.Complete {
		t.Fatalf("selected sibling external escape usage = %+v", usage)
	}
}

func TestExtractUsageRejectsMutatedSelectedFunctionAliases(t *testing.T) {
	tests := []struct {
		name   string
		bridge string
		use    string
	}{
		{
			name: "indirect local replacement",
			use: `package application
import (
  "example.test/alias-mutation/messages"
  "github.com/frostgrove/vv/i18n"
)
func use(snapshot *i18n.Snapshot, replacement func(*i18n.Snapshot)) {
  render := messages.Render
  pointer := &render
  *pointer = replacement
  render(snapshot)
}
`,
		},
		{
			name: "cross package variable replacement",
			bridge: `package bridge
import (
  "example.test/alias-mutation/messages"
  "github.com/frostgrove/vv/i18n"
)
var Render = messages.Render
func Use(snapshot *i18n.Snapshot) { Render(snapshot) }
`,
			use: `package application
import (
  "example.test/alias-mutation/bridge"
  "github.com/frostgrove/vv/i18n"
)
func use(snapshot *i18n.Snapshot, replacement func(*i18n.Snapshot)) {
  bridge.Render = replacement
  bridge.Use(snapshot)
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeNamedTestModule(t, directory, "example.test/alias-mutation")
			writeTestFile(t, filepath.Join(directory, "messages", "messages.go"), `package messages
import "github.com/frostgrove/vv/i18n"
func Render(snapshot *i18n.Snapshot) { _, _ = snapshot.Bind("app.alias_mutation") }
`)
			if test.bridge != "" {
				writeTestFile(t, filepath.Join(directory, "bridge", "bridge.go"), test.bridge)
			}
			writeTestFile(t, filepath.Join(directory, "application", "use.go"), test.use)
			usage, err := extractUsage(context.Background(), []string{directory}, true)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(usage.Keys, []i18n.Key{"app.alias_mutation"}) || usage.Complete {
				t.Fatalf("mutated selected alias usage = %+v", usage)
			}
		})
	}
}

func TestExtractUsageRejectsUnknownConvertedBindCapability(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/unknown-bind")
	writeTestFile(t, filepath.Join(directory, "bridge", "bridge.go"), `package bridge
import "github.com/frostgrove/vv/i18n"
var Unknown func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
`)
	writeTestFile(t, filepath.Join(directory, "application", "use.go"), `package application
import (
  "example.test/unknown-bind/bridge"
  "github.com/frostgrove/vv/i18n"
)
type Bind func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
func use() {
  bind := Bind(bridge.Unknown)
  _, _ = bind("app.unproved")
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage.Keys) != 0 || usage.Complete {
		t.Fatalf("unknown converted bind usage = %+v", usage)
	}
}

func TestExtractUsageRejectsUnknownConvertedBindForwarding(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/unknown-bind-forward")
	writeTestFile(t, filepath.Join(directory, "bridge", "bridge.go"), `package bridge
import "github.com/frostgrove/vv/i18n"
type Bind = func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
var Unknown Bind
func Consume(bind Bind) { _, _ = bind("app.claimed") }
`)
	writeTestFile(t, filepath.Join(directory, "application", "use.go"), `package application
import (
  "example.test/unknown-bind-forward/bridge"
  "github.com/frostgrove/vv/i18n"
)
type Bind func(i18n.Key, ...i18n.Argument) (i18n.Message, error)
func use() { bridge.Consume(Bind(bridge.Unknown)) }
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.claimed"}) || usage.Complete {
		t.Fatalf("unknown converted bind forwarding usage = %+v", usage)
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
			name: "range replacement is not trusted",
			build: `func Build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.range")
  result := Bindings{Known: definition}
  replacements := []i18n.Definition[Payload]{replacement}
  for _, result.Known = range replacements {}
  return result
}`,
			useField: "Known", wantKeys: []i18n.Key{"app.factory.range"},
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
			name: "value escape is not trusted",
			build: `func keep[T any](value T) { _ = value }
func Build(snapshot *i18n.Snapshot) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.escape")
  result := Bindings{Known: definition}
  keep(result)
  return result
}`,
			useField: "Known", wantKeys: []i18n.Key{"app.factory.escape"},
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
			name: "recursive factory with known base is trusted",
			build: `func Build(snapshot *i18n.Snapshot, depth int) Bindings {
  if depth > 0 { return Build(snapshot, depth-1) }
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.recursive")
  return Bindings{Known: definition}
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.recursive"},
		},
		{
			name: "single result factory wrapper is trusted",
			build: `func Pair(snapshot *i18n.Snapshot) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.single_wrapper")
  return Bindings{Known: definition}
}
func Build(snapshot *i18n.Snapshot) Bindings {
  bindings := Pair(snapshot)
  return bindings
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.single_wrapper"},
		},
		{
			name: "tuple result factory wrapper is trusted",
			build: `func Pair(snapshot *i18n.Snapshot) (error, Bindings) {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.tuple_wrapper")
  return nil, Bindings{Known: definition}
}
func Build(snapshot *i18n.Snapshot) Bindings {
  _, bindings := Pair(snapshot)
  return bindings
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.tuple_wrapper"},
		},
		{
			name: "unknown tuple result factory wrapper is not trusted",
			build: `func Pair(replacement i18n.Definition[Payload]) (error, Bindings) {
  return nil, Bindings{Known: replacement}
}
func Build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) Bindings {
  _, bindings := Pair(replacement)
  return bindings
}`,
			useField: "Known",
		},
		{
			name: "pointer dereference factory wrapper is trusted",
			build: `func Pointer(snapshot *i18n.Snapshot) *Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.pointer_wrapper")
  return &Bindings{Known: definition}
}
func Build(snapshot *i18n.Snapshot) Bindings {
  return *Pointer(snapshot)
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.pointer_wrapper"},
		},
		{
			name: "local pointer result factory is trusted",
			build: `func Build(snapshot *i18n.Snapshot) *Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.local_pointer")
  bindings := Bindings{Known: definition}
  return &bindings
}`,
			useField: "Known", complete: true, wantKeys: []i18n.Key{"app.factory.local_pointer"},
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
			case "generated replacement is not trusted", "range replacement is not trusted", "trust is independent per result field", "unknown tuple result factory wrapper is not trusted":
				arguments += ", replacement"
			case "mixed return paths are not trusted":
				arguments += ", replacement, choose"
			case "address escape is not trusted":
				arguments += ", escape"
			case "recursive factory with known base is trusted":
				arguments += ", 1"
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

func TestExtractUsageTrustsGenericDefinitionFactoryFields(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/generic-factory")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Bindings[T any] struct { Known i18n.Definition[T] }
func Build[T any](snapshot *i18n.Snapshot) Bindings[T] {
  definition, _ := i18n.NewStructDefinition[T](snapshot, "app.factory.generic")
  return Bindings[T]{Known: definition}
}
func use(snapshot *i18n.Snapshot) {
  _, _ = Build[Payload](snapshot).Known.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.factory.generic"}) || !usage.Complete {
		t.Fatalf("generic factory usage = %+v", usage)
	}
}

func TestExtractUsageTrustsSelectedCrossPackageDefinitionFactories(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/cross-package-factory")
	writeTestFile(t, filepath.Join(directory, "messages", "messages.go"), `package messages
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Bindings struct { Known i18n.Definition[Payload] }
func Build(snapshot *i18n.Snapshot) *Bindings {
  _ = func() int { return 1 }
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.cross_package")
  return &Bindings{Known: definition}
}
func Contract(snapshot *i18n.Snapshot) i18n.ContractRef {
  _ = func() string { return "unrelated" }
  contract, _ := snapshot.ContractRef("app.contract.cross_package")
  return contract
}
func BuildSecond(snapshot *i18n.Snapshot) (error, Bindings) {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.second_result")
  return nil, Bindings{Known: definition}
}
func ContractSecond(snapshot *i18n.Snapshot) (error, i18n.ContractRef) {
  contract, _ := snapshot.ContractRef("app.contract.second_result")
  return nil, contract
}
func ContractPointer(snapshot *i18n.Snapshot) *i18n.ContractRef {
  contract, _ := snapshot.ContractRef("app.contract.pointer")
  return &contract
}
`)
	writeTestFile(t, filepath.Join(directory, "bridge", "bridge.go"), `package bridge
import (
  "example.test/cross-package-factory/messages"
  "github.com/frostgrove/vv/i18n"
)
func Build(snapshot *i18n.Snapshot) *messages.Bindings { return messages.Build(snapshot) }
func Contract(snapshot *i18n.Snapshot) i18n.ContractRef { return messages.Contract(snapshot) }
func BuildSecond(snapshot *i18n.Snapshot) (error, messages.Bindings) { return messages.BuildSecond(snapshot) }
func ContractSecond(snapshot *i18n.Snapshot) (error, i18n.ContractRef) { return messages.ContractSecond(snapshot) }
`)
	writeTestFile(t, filepath.Join(directory, "application", "use.go"), `package application
import (
  "example.test/cross-package-factory/bridge"
  "example.test/cross-package-factory/messages"
  "github.com/frostgrove/vv/i18n"
)
func use(snapshot *i18n.Snapshot) {
  _, _ = bridge.Build(snapshot).Known.Bind(messages.Payload{})
  _, _ = i18n.DefineStruct[messages.Payload](snapshot, bridge.Contract(snapshot))
  _, bindings := bridge.BuildSecond(snapshot)
  _, _ = bindings.Known.Bind(messages.Payload{})
  _, contract := bridge.ContractSecond(snapshot)
  _, _ = i18n.DefineStruct[messages.Payload](snapshot, contract)
  _, _ = i18n.DefineStruct[messages.Payload](snapshot, *messages.ContractPointer(snapshot))
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []i18n.Key{"app.contract.cross_package", "app.contract.pointer", "app.contract.second_result", "app.factory.cross_package", "app.factory.second_result"}
	if !slices.Equal(usage.Keys, want) || !usage.Complete {
		t.Fatalf("selected cross-package factory usage = %+v, want %v", usage, want)
	}
}

func TestExtractUsageDowngradesEscapedFactoryContainerPointer(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/factory-container-escape")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Bindings struct { Known i18n.Definition[Payload] }
func Build(snapshot *i18n.Snapshot) *Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.factory.container_escape")
  return &Bindings{Known: definition}
}
func use(snapshot *i18n.Snapshot, mutate func(*Bindings)) {
  bindings := Build(snapshot)
  mutate(bindings)
  _, _ = bindings.Known.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.factory.container_escape"}) || usage.Complete {
		t.Fatalf("escaped factory container usage = %+v", usage)
	}
}

func TestUsageCodecCanonicalizesUniqueTagsAndRejectsNonCanonicalWireTags(t *testing.T) {
	manifest := testUsageDocumentManifest()
	manifest.GoScope.BuildTags = []string{"z", "a"}
	raw, err := encodeUsage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded.GoScope.BuildTags, []string{"a", "z"}) {
		t.Fatalf("canonical build tags = %v", decoded.GoScope.BuildTags)
	}

	duplicate := testUsageDocumentManifest()
	duplicate.GoScope.BuildTags = []string{"edge", "edge"}
	if _, err := encodeUsage(duplicate); err == nil {
		t.Fatal("duplicate build tags were encoded")
	}

	for _, tags := range [][]string{{"z", "a"}, {"edge", "edge"}} {
		forged := testUsageDocumentManifest()
		forged.GoScope.BuildTags = tags
		forged.GoScope.SourceDigest = i18n.ExpectedUsageSourceDigest(*forged.GoScope)
		forged.ManifestDigest = i18n.ExpectedUsageManifestDigest(forged)
		raw, err := encodeCommandJSON("forged usage manifest", usageDocument{
			Schema: usageSchema, Keys: forged.Keys, Dynamic: forged.Dynamic, Occurrences: forged.Occurrences,
			GoScope: forged.GoScope, ManifestDigest: forged.ManifestDigest, Complete: forged.Complete,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeUsage(raw); err == nil {
			t.Fatalf("non-canonical wire tags were decoded: %v", tags)
		}
	}
}

func TestUsageCodecRejectsNonCanonicalUnscopedV4Content(t *testing.T) {
	duplicate := i18n.UsageManifest{Keys: []i18n.Key{"app.z", "app.z", "app.a"}}
	if _, err := encodeUsage(duplicate); err == nil {
		t.Fatal("duplicate unscoped keys were encoded")
	}

	for _, keys := range [][]i18n.Key{{"app.z", "app.a"}, {"app.a", "app.a"}} {
		manifest := i18n.UsageManifest{Keys: keys}
		manifest.ManifestDigest = i18n.ExpectedUsageManifestDigest(manifest)
		raw, err := encodeCommandJSON("forged usage manifest", usageDocument{
			Schema: usageSchema, Keys: manifest.Keys, Dynamic: manifest.Dynamic, Occurrences: manifest.Occurrences,
			ManifestDigest: manifest.ManifestDigest,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeUsage(raw); err == nil {
			t.Fatalf("non-canonical unscoped keys were decoded: %v", keys)
		}
	}
}

func TestUsageCodecRejectsControlAndBidiInLedgerPaths(t *testing.T) {
	tests := map[string]func(*i18n.UsageManifest){
		"root NUL": func(manifest *i18n.UsageManifest) {
			manifest.GoScope.Roots[0].Path = "example.test/\x00app"
			manifest.GoScope.Files[0].Root = manifest.GoScope.Roots[0].Path
			manifest.GoScope.Files[0].LogicalPath = manifest.GoScope.Files[0].Root + "/" + manifest.GoScope.Files[0].Path
			for index := range manifest.Occurrences {
				manifest.Occurrences[index].Path = manifest.GoScope.Files[0].LogicalPath
			}
		},
		"file control": func(manifest *i18n.UsageManifest) {
			manifest.GoScope.Files[0].Path = "\x1fuse.go"
			manifest.GoScope.Files[0].LogicalPath = manifest.GoScope.Files[0].Root + "/" + manifest.GoScope.Files[0].Path
			for index := range manifest.Occurrences {
				manifest.Occurrences[index].Path = manifest.GoScope.Files[0].LogicalPath
			}
		},
		"occurrence bidi": func(manifest *i18n.UsageManifest) {
			manifest.Occurrences[0].Path = "example.test/app/\u202espoof.go"
		},
		"metadata control": func(manifest *i18n.UsageManifest) {
			manifest.GoScope.Metadata[0].Path = "go.\x7fmod"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := testUsageDocumentManifest()
			mutate(&manifest)
			if _, err := encodeUsage(manifest); err == nil {
				t.Fatal("unsafe ledger path was encoded")
			}
			manifest.GoScope.SourceDigest = i18n.ExpectedUsageSourceDigest(*manifest.GoScope)
			manifest.ManifestDigest = i18n.ExpectedUsageManifestDigest(manifest)
			raw, err := encodeCommandJSON("forged usage manifest", usageDocument{
				Schema: usageSchema, Keys: manifest.Keys, Dynamic: manifest.Dynamic, Occurrences: manifest.Occurrences,
				GoScope: manifest.GoScope, ManifestDigest: manifest.ManifestDigest, Complete: manifest.Complete,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeUsage(raw); err == nil {
				t.Fatal("unsafe ledger path was decoded")
			}
		})
	}
}

func TestUsageCodecPreservesLegacyPositiveEvidenceWithoutV4Authority(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		occurrences int
	}{
		{
			name: "v1",
			raw:  `{"schema":"frostgrove.i18n.usage/v1","keys":["app.one"],"dynamic":[],"complete":true}`,
		},
		{
			name:        "v2",
			raw:         `{"schema":"frostgrove.i18n.usage/v2","keys":["app.one"],"dynamic":[],"occurrences":[{"key":"app.one","path":"legacy/use.go","line":1,"column":1}],"complete":true}`,
			occurrences: 1,
		},
		{
			name:        "v3",
			raw:         `{"schema":"frostgrove.i18n.usage/v3","keys":["app.one"],"dynamic":[],"occurrences":[{"key":"app.one","path":"legacy/use.go","line":1,"column":1}],"go_scope":{"analyzer":"frostgrove.vv-i18n/go-ast/v1","goos":"linux","goarch":"amd64","compiler":"gc","cgo_enabled":false,"build_tags":[],"tool_tags":[],"release_tags":[],"roots":[{"path":"legacy","kind":"directory"}],"source_digest":"0000000000000000000000000000000000000000000000000000000000000000","selected_files":1,"excluded_files":0},"complete":true}`,
			occurrences: 1,
		},
	}
	spec := i18n.CatalogSpec{
		Revision: "legacy/v1", SourceLocale: "en", DefaultLocale: "en", Supported: []string{"en"},
		Modules: []i18n.Module{{Name: "app", Messages: []i18n.MessageSpec{{
			ID: "one", Revision: "one/v1", Source: "One", Description: "Legacy usage fixture.", Output: i18n.OutputPlain,
		}}}},
	}
	if _, err := i18n.New(spec); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage, err := decodeUsage([]byte(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(usage.Keys, []i18n.Key{"app.one"}) || len(usage.Occurrences) != test.occurrences || usage.GoScope != nil || usage.Complete {
				t.Fatalf("legacy migration = %+v", usage)
			}
			report := i18n.Check(spec, i18n.CheckPolicy{Usage: usage})
			if !report.OK() {
				t.Fatalf("legacy usage check = %+v", report.Findings)
			}
			for _, finding := range report.Findings {
				if strings.HasPrefix(finding.Path, "usage.") || finding.Status == i18n.CheckUnused {
					t.Fatalf("legacy evidence triggered v4 authority: %+v", report.Findings)
				}
			}
		})
	}
}

func TestUsageCodecRejectsPublicStringLimitBeforeCanonicalAllocation(t *testing.T) {
	limits := i18n.DefaultUsageLimits()
	manifest := i18n.UsageManifest{Keys: []i18n.Key{i18n.Key(strings.Repeat("x", limits.MaxStringBytes+1))}}
	if _, err := encodeUsage(manifest); err == nil || !strings.Contains(err.Error(), "string material") {
		t.Fatalf("oversized usage string error = %v", err)
	}
}

func FuzzUsageCodecNeverPanicsAndCanonicalizesSuccessfulInput(f *testing.F) {
	f.Add([]byte(`{"schema":"frostgrove.i18n.usage/v1","keys":["app.one"],"dynamic":[],"complete":true}`))
	f.Add([]byte(`{"schema":"frostgrove.i18n.usage/v4","keys":[],"dynamic":[],"occurrences":[],"manifest_digest":"invalid","complete":false}`))
	if raw, err := encodeUsage(i18n.UsageManifest{}); err == nil {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			return
		}
		manifest, err := decodeUsage(raw)
		if err != nil {
			return
		}
		first, err := encodeUsage(manifest)
		if err != nil {
			t.Fatalf("decoded manifest did not re-encode: %v", err)
		}
		roundTrip, err := decodeUsage(first)
		if err != nil {
			t.Fatalf("canonical manifest did not decode: %v", err)
		}
		second, err := encodeUsage(roundTrip)
		if err != nil || string(first) != string(second) {
			t.Fatalf("canonical encoding is unstable: %v\nfirst: %s\nsecond: %s", err, first, second)
		}
	})
}
