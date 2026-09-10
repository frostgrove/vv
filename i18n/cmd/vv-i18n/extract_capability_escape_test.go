package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestExtractUsageRejectsDefinitionsEscapedThroughTypedContainers(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/typed-container-escape")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Box[T any] struct { Definition i18n.Definition[T] }
type Nested[T any] struct { Box *Box[T] }
func direct(replacement i18n.Definition[Payload]) Box[Payload] {
  return Box[Payload]{Definition: replacement}
}
func nested(replacement i18n.Definition[Payload]) Nested[Payload] {
  box := &Box[Payload]{Definition: replacement}
  return Nested[Payload]{Box: box}
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) {
  _, _ = snapshot.Bind("app.visible")
  _, _ = direct(replacement), nested(replacement)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
		t.Fatalf("typed container escape usage = %+v", usage)
	}
}

func TestExtractUsageRejectsConstructorsStoredInTypedContainers(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/typed-constructor-container")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Factory[A any] func(*i18n.Snapshot, i18n.Key, i18n.ArgumentEncoder[A]) (i18n.Definition[A], error)
type FactoryBox[A any] struct { Factory Factory[A] }
func leak[A any]() FactoryBox[A] {
  return FactoryBox[A]{Factory: i18n.NewDefinition[A]}
}
func use(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  _ = leak[string]()
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
		t.Fatalf("typed constructor container usage = %+v", usage)
	}
}

func TestExtractUsageRejectsKeyBearingSnapshotMethodValues(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/snapshot-method-value-escape")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
func contract(snapshot *i18n.Snapshot) func(i18n.Key) (i18n.ContractRef, bool) {
  return snapshot.ContractRef
}
func errors(snapshot *i18n.Snapshot) func(i18n.ErrorSpec) (*i18n.ErrorSource, error) {
  return snapshot.ErrorMessages
}
func use(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.visible")
  _, _ = contract(snapshot), errors(snapshot)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
		t.Fatalf("snapshot method value escape usage = %+v", usage)
	}
}

func TestExtractUsageTrustsKnownDefinitionsInNestedTypedContainers(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/known-nested-container")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Box[T any] struct { Definition i18n.Definition[T] }
type Nested[T any] struct { Box *Box[T] }
func known(snapshot *i18n.Snapshot) Nested[Payload] {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.known.nested")
  return Nested[Payload]{Box: &Box[Payload]{Definition: definition}}
}
func use(snapshot *i18n.Snapshot) {
  nested := known(snapshot)
  _, _ = nested.Box.Definition.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.known.nested"}) || !usage.Complete {
		t.Fatalf("known nested container usage = %+v", usage)
	}
}

func TestExtractUsageRejectsMixedDefinitionContainerErasedAsWhole(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/mixed-container-whole-escape")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Pair struct {
  Known i18n.Definition[Payload]
  Hidden i18n.Definition[Payload]
}
func leak(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) any {
  known, _ := i18n.NewStructDefinition[Payload](snapshot, "app.mixed.known")
  return Pair{Known: known, Hidden: replacement}
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) { _ = leak(snapshot, replacement) }
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.mixed.known"}) || usage.Complete {
		t.Fatalf("mixed whole-container escape usage = %+v", usage)
	}
}

func TestExtractUsageRejectsUnknownContractInMapKey(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/contract-map-key-escape")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
func leak(replacement i18n.ContractRef) any {
  return map[i18n.ContractRef]string{replacement: "hidden"}
}
func use(snapshot *i18n.Snapshot, replacement i18n.ContractRef) {
  _, _ = snapshot.Bind("app.visible")
  _ = leak(replacement)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.visible"}) || usage.Complete {
		t.Fatalf("contract map-key escape usage = %+v", usage)
	}
}

func TestExtractUsageTrustsKnownContractInMapKey(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/known-contract-map-key")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
func known(snapshot *i18n.Snapshot) any {
  contract, _ := snapshot.ContractRef("app.map.known")
  return map[i18n.ContractRef]string{contract: "known"}
}
func use(snapshot *i18n.Snapshot) { _ = known(snapshot) }
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.map.known"}) || !usage.Complete {
		t.Fatalf("known contract map-key usage = %+v", usage)
	}
}

func TestExtractUsageRejectsMutatedNestedDefinitionPath(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/mutated-nested-definition")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Box struct { Definition i18n.Definition[Payload] }
type Nested struct { Box *Box }
func build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) Nested {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.nested.initial")
  nested := Nested{Box: &Box{Definition: definition}}
  nested.Box.Definition = replacement
  return nested
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) {
  nested := build(snapshot, replacement)
  _, _ = nested.Box.Definition.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.nested.initial"}) || usage.Complete {
		t.Fatalf("mutated nested definition usage = %+v", usage)
	}
}

func TestExtractUsageTrustsImmutableNamedZeroDefinition(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/named-zero-definition")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func definition(snapshot *i18n.Snapshot) (i18n.Definition[Payload], error) {
  value, err := i18n.NewStructDefinition[Payload](snapshot, "app.named.zero")
  if err != nil {
    var zero i18n.Definition[Payload]
    return zero, err
  }
  return value, nil
}
func use(snapshot *i18n.Snapshot) {
  value, _ := definition(snapshot)
  _, _ = value.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.named.zero"}) || !usage.Complete {
		t.Fatalf("immutable named zero usage = %+v", usage)
	}
}

func TestExtractUsageRejectsMutatedNamedZeroDefinition(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/mutated-named-zero-definition")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func definition(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) (i18n.Definition[Payload], error) {
  value, err := i18n.NewStructDefinition[Payload](snapshot, "app.named.mutated")
  if err != nil {
    var zero i18n.Definition[Payload]
    zero = replacement
    return zero, err
  }
  return value, nil
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) {
  value, _ := definition(snapshot, replacement)
  _, _ = value.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.named.mutated"}) || usage.Complete {
		t.Fatalf("mutated named zero usage = %+v", usage)
	}
}
