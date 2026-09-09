package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNumericBoundsUseStrictJSONNumbers(t *testing.T) {
	var decoded struct {
		Bound NumericBound `json:"bound"`
	}
	for _, input := range []string{`{"bound":"1"}`, `{"bound":true}`, `{"bound":[]}`, `{"bound":null}`} {
		if err := json.Unmarshal([]byte(input), &decoded); err == nil {
			t.Fatalf("accepted non-numeric bound %s", input)
		}
	}
	for _, input := range []string{`{"bound":9007199254740993}`, `{"bound":1e3}`, `{"bound":0.125}`} {
		if err := json.Unmarshal([]byte(input), &decoded); err != nil {
			t.Fatalf("rejected numeric bound %s: %v", input, err)
		}
	}
}

func TestInt64BoundsMustBeIntegralAndInRange(t *testing.T) {
	base := Signal{Kind: "metric", Name: "vv.test", Instrument: "counter", NumberType: "int64", Unit: "{item}", RecordWhen: "non_negative"}
	for name, change := range map[string]func(*Signal){
		"fractional_minimum": func(signal *Signal) { signal.Min = numericBound("0.5") },
		"fractional_maximum": func(signal *Signal) { signal.Max = numericBound("1e-1") },
		"minimum_underflow":  func(signal *Signal) { signal.Min = numericBound("-9223372036854775809") },
		"maximum_overflow":   func(signal *Signal) { signal.Max = numericBound("9223372036854775808") },
	} {
		t.Run(name, func(t *testing.T) {
			signal := base
			change(&signal)
			if err := validateSignalShape(signal); err == nil {
				t.Fatal("invalid int64 bound accepted")
			}
		})
	}
	base.Min = numericBound("9.007199254740992e15")
	base.Max = numericBound("9007199254740993")
	if err := validateSignalShape(base); err != nil {
		t.Fatalf("exact integral JSON bounds rejected: %v", err)
	}
}

func TestHistogramBoundariesStayWithinBothAuthoredBounds(t *testing.T) {
	signal := validRegistryFixture(t).Signals["command_duration"]
	signal.Max = numericBound("10")
	if err := validateSignalShape(signal); err != nil {
		t.Fatalf("boundary equal to authored maximum rejected: %v", err)
	}
	signal.Max = numericBound("9")
	if err := validateSignalShape(signal); err == nil {
		t.Fatal("boundary above authored maximum accepted")
	}
}

func TestRenderedBoundsPreserveTypedExactValues(t *testing.T) {
	r := validRegistryFixture(t)
	modifySignal(&r, "cache_operations", func(signal *Signal) {
		signal.Min = numericBound("9007199254740992")
		signal.Max = numericBound("9007199254740993")
	})
	if err := validate(r); err != nil {
		t.Fatalf("exact int64 fixture is invalid: %v", err)
	}
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := renderedDescriptor(code, "cache_operations")
	for _, field := range []string{"Minimum: 9.007199254740992e+15", "Maximum: 9.007199254740992e+15", "MinimumInt64: 9007199254740992", "MaximumInt64: 9007199254740993", "HasMinimum: true", "HasMaximum: true"} {
		if !strings.Contains(descriptor, field) {
			t.Fatalf("rendered descriptor lost %s", field)
		}
	}
	manifest, err := renderManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"min": 9007199254740992`) || !strings.Contains(string(manifest), `"max": 9007199254740993`) {
		t.Fatal("wire manifest rounded exact int64 bounds")
	}
}

func TestRenderedBoundPresenceMatchesAuthoredPresence(t *testing.T) {
	r := validRegistryFixture(t)
	modifySignal(&r, "cache_operations", func(signal *Signal) {
		signal.Min = ""
		signal.Max = numericBound("5")
	})
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := renderedDescriptor(code, "cache_operations")
	if !strings.Contains(descriptor, "HasMinimum: false") || !strings.Contains(descriptor, "HasMaximum: true") {
		t.Fatal("rendered bound presence differs from authored min/max presence")
	}
	floatDescriptor := renderedDescriptor(code, "command_duration")
	if !strings.Contains(floatDescriptor, "Minimum: 0") || !strings.Contains(floatDescriptor, "HasMinimum: true") || !strings.Contains(floatDescriptor, "MinimumInt64: 0") {
		t.Fatal("float descriptor bounds changed representation")
	}
}

func renderedDescriptor(code []byte, key string) string {
	start := strings.Index(string(code), `{Key: "`+key+`"`)
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(string(code[start:]), '\n')
	if end < 0 {
		return string(code[start:])
	}
	return string(code[start : start+end])
}

func TestGeneratedInt64AdmissionAtFloatPrecisionBoundary(t *testing.T) {
	r := validRegistryFixture(t)
	modifySignal(&r, "cache_encoded_bytes", func(signal *Signal) {
		signal.Max = numericBound("9007199254740992")
	})
	if err := validate(r); err != nil {
		t.Fatalf("bounded fixture is invalid: %v", err)
	}
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(".", ".numeric-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.WriteFile(filepath.Join(directory, "schema_gen.go"), code, 0600); err != nil {
		t.Fatal(err)
	}
	contract := []byte(`package vvotel

import "testing"

func TestInt64Boundary(t *testing.T) {
	var target SignalDescriptor
	for _, descriptor := range SignalDescriptors() {
		if descriptor.Key == "cache_encoded_bytes" {
			target = descriptor
			break
		}
	}
	const maximum int64 = 1 << 53
	if !target.AcceptsInt64(maximum) {
		t.Fatal("typed maximum rejected")
	}
	if target.AcceptsInt64(maximum + 1) {
		t.Fatal("typed value above maximum accepted")
	}
	if target.AcceptsValue(float64(maximum)) {
		t.Fatal("compatibility float path accepted an ambiguous int64")
	}
}
`)
	if err := os.WriteFile(filepath.Join(directory, "schema_gen_test.go"), contract, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-count=1", ".")
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated numeric contract: %v\n%s", err, output)
	}
}

func TestGeneratedResourceNameAdmissionTracksRegistryBounds(t *testing.T) {
	r := validRegistryFixture(t)
	resource := r.Attributes[r.Exports.ResourceAttribute]
	resource.MaxBytes = 3
	r.Attributes[r.Exports.ResourceAttribute] = resource
	if err := validate(r); err != nil {
		t.Fatalf("bounded resource fixture is invalid: %v", err)
	}
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	if publicSchemaSurface(t, code)["MaxResourceNameBytes"] != "3" {
		t.Fatal("generated resource byte authority ignored the registry")
	}
	directory, err := os.MkdirTemp(".", ".resource-contract-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	if err := os.WriteFile(filepath.Join(directory, "schema_gen.go"), code, 0600); err != nil {
		t.Fatal(err)
	}
	contract := []byte(`package vvotel

import (
	"go.opentelemetry.io/otel/attribute"
	"testing"
)

func TestResourceNameBoundary(t *testing.T) {
	if !ValidResourceName("abc") || ValidResourceName("abcd") {
		t.Fatal("generated public admission ignored the three-byte boundary")
	}
	var declaration SignalAttributeDescriptor
	found := false
	for _, descriptor := range SignalDescriptors() {
		for _, variant := range descriptor.Variants {
			for _, field := range variant.Attributes {
				if field.Key == AttrResourceName && field.Declared {
					declaration = field
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("resource declaration descriptor is absent")
	}
	for _, value := range []string{"", "a", "abc", "abcd", "a-b", "bad space", "é", "éé"} {
		if got, want := ValidResourceName(value), signalAttributeAccepts(declaration, attribute.StringValue(value)); got != want {
			t.Fatalf("runtime/schema admission differs for %q: %t/%t", value, got, want)
		}
	}
}
`)
	if err := os.WriteFile(filepath.Join(directory, "schema_gen_test.go"), contract, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-count=1", ".")
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated resource contract: %v\n%s", err, output)
	}
}
