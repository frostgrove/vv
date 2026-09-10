package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestExtractUsageTrustsSelectedDirectResultFactories(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/direct-result-factories")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func Definition(snapshot *i18n.Snapshot) (i18n.Definition[Payload], error) {
  return i18n.NewStructDefinition[Payload](snapshot, "app.direct.definition")
}
func ForwardDefinition(snapshot *i18n.Snapshot) (i18n.Definition[Payload], error) {
  return Definition(snapshot)
}
func RepackDefinition(snapshot *i18n.Snapshot) (i18n.Definition[Payload], error) {
  definition, err := ForwardDefinition(snapshot)
  return definition, err
}
func DefinitionPointer(snapshot *i18n.Snapshot) *i18n.Definition[Payload] {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.direct.pointer")
  return &definition
}
func Specification(snapshot *i18n.Snapshot) i18n.DefinitionSpec[Payload] {
  contract, _ := snapshot.ContractRef("app.direct.specification")
  return i18n.DefinitionSpec[Payload]{Contract: contract, Encode: func(Payload) ([]i18n.Argument, error) { return nil, nil }}
}
func SpecificationPointer(snapshot *i18n.Snapshot) *i18n.DefinitionSpec[Payload] {
  contract, _ := snapshot.ContractRef("app.direct.specification_pointer")
  specification := i18n.DefinitionSpec[Payload]{Contract: contract, Encode: func(Payload) ([]i18n.Argument, error) { return nil, nil }}
  return &specification
}
func Descriptor(snapshot *i18n.Snapshot) i18n.Descriptor {
  descriptor, _ := snapshot.Descriptor("app.direct.descriptor")
  return descriptor
}
func DescriptorPointer(snapshot *i18n.Snapshot) *i18n.Descriptor {
  descriptor, _ := snapshot.Descriptor("app.direct.descriptor_pointer")
  return &descriptor
}
func Mapping() i18n.ErrorMapping {
  return i18n.ErrorMapping{Ladder: "required", Key: "errors.direct.mapping"}
}
func Label() i18n.FieldLabel {
  return i18n.FieldLabel{Field: "name", Key: "fields.direct.name"}
}
func MappingPointer() *i18n.ErrorMapping {
  mapping := i18n.ErrorMapping{Ladder: "required", Key: "errors.direct.mapping_pointer"}
  return &mapping
}
func ErrorConfiguration() i18n.ErrorSpec {
  return i18n.ErrorSpec{Mappings: []i18n.ErrorMapping{Mapping()}, FieldLabels: []i18n.FieldLabel{Label()}}
}
func PlanConfiguration() i18n.ErrorPlanSpec {
  return i18n.ErrorPlanSpec{Mappings: []i18n.ErrorMapping{Mapping()}, FieldLabels: []i18n.FieldLabel{Label()}}
}
func ErrorConfigurationPointer() *i18n.ErrorSpec {
  specification := i18n.ErrorSpec{Mappings: []i18n.ErrorMapping{*MappingPointer()}}
  return &specification
}
func use(snapshot *i18n.Snapshot) {
  definition, _ := RepackDefinition(snapshot)
  _, _ = definition.Bind(Payload{})
  _, _ = (*DefinitionPointer(snapshot)).Bind(Payload{})
  _, _ = i18n.Define(snapshot, Specification(snapshot))
  _, _ = i18n.Define(snapshot, *SpecificationPointer(snapshot))
  _, _ = i18n.DefineStruct[Payload](snapshot, Descriptor(snapshot).ContractRef())
  _, _ = i18n.DefineStruct[Payload](snapshot, (*DescriptorPointer(snapshot)).ContractRef())
  _, _ = snapshot.ErrorMessages(ErrorConfiguration())
  _, _ = snapshot.ErrorMessages(*ErrorConfigurationPointer())
  _, _ = snapshot.ErrorPlan(PlanConfiguration())
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []i18n.Key{
		"app.direct.definition",
		"app.direct.descriptor",
		"app.direct.descriptor_pointer",
		"app.direct.pointer",
		"app.direct.specification",
		"app.direct.specification_pointer",
		"errors.direct.mapping",
		"errors.direct.mapping_pointer",
		"fields.direct.name",
	}
	if !slices.Equal(usage.Keys, want) || !usage.Complete {
		t.Fatalf("direct result factory usage = %+v, want keys %v and complete", usage, want)
	}
}

func TestExtractUsageRejectsUnprovenDirectResultFactoryAndErasedControllerSpec(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/direct-result-controls")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
var escaped any
func keep(value any) { escaped = value }
func Definition(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload], known bool) (i18n.Definition[Payload], error) {
  if known { return i18n.NewStructDefinition[Payload](snapshot, "app.direct.decoy") }
  return replacement, nil
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload], spec i18n.ControllerSpec) {
  definition, _ := Definition(snapshot, replacement, false)
  _, _ = definition.Bind(Payload{})
  keep(spec)
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.direct.decoy"}) || usage.Complete {
		t.Fatalf("unproven direct result usage = %+v", usage)
	}
}

func TestExtractUsageRejectsWholeContainerPointerMutation(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/container-pointer-mutation")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
type Bindings struct { Known i18n.Definition[Payload] }
func (bindings *Bindings) Replace(replacement i18n.Definition[Payload]) {
  *bindings = Bindings{Known: replacement}
}
func Build(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) Bindings {
  definition, _ := i18n.NewStructDefinition[Payload](snapshot, "app.container.decoy")
  bindings := Bindings{Known: definition}
  bindings.Replace(replacement)
  return bindings
}
func use(snapshot *i18n.Snapshot, replacement i18n.Definition[Payload]) {
  _, _ = Build(snapshot, replacement).Known.Bind(Payload{})
}
`)
	usage, err := extractUsage(context.Background(), []string{directory}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(usage.Keys, []i18n.Key{"app.container.decoy"}) || usage.Complete {
		t.Fatalf("whole-container pointer mutation usage = %+v", usage)
	}
}

func TestExtractUsageRejectsHiddenKeyErasureThroughLocalAlias(t *testing.T) {
	directory := t.TempDir()
	writeNamedTestModule(t, directory, "example.test/hidden-key-erasure")
	writeTestFile(t, filepath.Join(directory, "use.go"), `package sample
import "github.com/frostgrove/vv/i18n"
type Payload struct { Value string }
func leak(definition i18n.Definition[Payload]) any {
  erased := any(definition)
  return erased
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
		t.Fatalf("hidden-key erasure usage = %+v", usage)
	}
}
