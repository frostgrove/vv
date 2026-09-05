package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderIsDeterministic(t *testing.T) {
	registry, err := readRegistry("../../internal/otelreg/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	first, err := render(registry)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, err := render(registry)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, first) {
			t.Fatalf("render %d differs from first render", i)
		}
	}
}

func TestValidateRejectsIncompleteRegistry(t *testing.T) {
	registry, err := readRegistry("../../internal/otelreg/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	mapping := registry.Mappings["cache"]
	mapping.Operations = map[string]string{}
	registry.Mappings["cache"] = mapping
	if err := validate(registry); err == nil {
		t.Fatal("expected incomplete mapping to be rejected")
	}
}

func TestValidateRejectsUnknownMappingValue(t *testing.T) {
	registry, err := readRegistry("../../internal/otelreg/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	mapping := registry.Mappings["cache"]
	mapping.Operations["lookup"] = "tenant-secret"
	registry.Mappings["cache"] = mapping
	if err := validate(registry); err == nil {
		t.Fatal("expected unknown mapping value to be rejected")
	}
}

func TestReadRegistryRejectsTrailingDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"contract_version":"x"} {}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegistry(path); err == nil {
		t.Fatal("expected trailing JSON document to be rejected")
	}
}

func TestCheckDetectsStaleOutputWithoutWriting(t *testing.T) {
	registry, err := readRegistry("../../internal/otelreg/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	output, err := render(registry)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "schema_gen.go")
	if err := os.WriteFile(path, append([]byte("stale\n"), output...), 0644); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkGeneratedOutput(path, output); err == nil {
		t.Fatal("stale output unexpectedly passed check")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, unchanged) {
		t.Fatal("check fixture was modified")
	}
}
