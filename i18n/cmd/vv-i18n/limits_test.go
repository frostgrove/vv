package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCommandLimitsCodecIsDeclarativeAndStrict(t *testing.T) {
	maxMessages := 8192
	maxTagBytes := 192
	sourceBytes := 192 << 20
	compiledBytes := 256 << 20
	outputBytes := 320 << 20
	document := commandLimitsDocument{
		Schema: commandLimitsSchema,
		Catalog: &commandCatalogLimits{
			Locale:      &commandLocaleLimits{MaxTagBytes: &maxTagBytes},
			MaxMessages: &maxMessages,
		},
		Source:   &commandArtifactLimits{MaxBytes: &sourceBytes},
		Compiled: &commandArtifactLimits{MaxBytes: &compiledBytes},
		Output:   &commandOutputLimitsDocument{MaxBytes: &outputBytes},
	}
	raw, err := encodeCommandJSON("limits document", document)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := decodeCommandLimits(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if limits.Catalog.MaxMessages != maxMessages || limits.Catalog.Locale.MaxTagBytes != maxTagBytes || limits.Source.MaxBytes != sourceBytes || limits.Compiled.MaxBytes != compiledBytes || limits.Output.MaxBytes != outputBytes {
		t.Fatalf("decoded limits = %+v", limits)
	}

	valid := strings.TrimSpace(string(raw))
	cases := map[string]string{
		"unknown":            strings.Replace(valid, `"schema":`, `"unknown":true,"schema":`, 1),
		"duplicate":          strings.Replace(valid, `"schema":`, `"schema":"`+commandLimitsSchema+`","\u0073chema":`, 1),
		"unsupported schema": strings.Replace(valid, commandLimitsSchema, "frostgrove.i18n.limits/v2", 1),
		"explicit zero":      strings.Replace(valid, `"max_messages": 8192`, `"max_messages": 0`, 1),
		"negative":           strings.Replace(valid, `"max_messages": 8192`, `"max_messages": -1`, 1),
		"too many messages":  strings.Replace(valid, `"max_messages": 8192`, `"max_messages": 100001`, 1),
		"too much output":    strings.Replace(valid, `"max_bytes": 335544320`, `"max_bytes": 1073741825`, 1),
		"trailing":           string(raw) + `{}`,
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCommandLimits(context.Background(), []byte(candidate)); err == nil {
				t.Fatal("invalid limits document accepted")
			}
		})
	}
	compact := strings.NewReplacer("\n", "", "  ", "").Replace(string(raw))
	if _, err := decodeCommandLimits(context.Background(), []byte(compact)); err != nil {
		t.Fatalf("equivalent operator formatting rejected: %v", err)
	}
}

func TestCommandLimitsRejectNullForEveryOptionalSectionAndLeaf(t *testing.T) {
	paths := nullableCommandLimitPaths(reflect.TypeFor[commandLimitsDocument](), nil)
	if len(paths) < 40 {
		t.Fatalf("null-path census = %d", len(paths))
	}
	for _, path := range paths {
		name := strings.Join(path, ".")
		t.Run(name, func(t *testing.T) {
			value := "null"
			for index := len(path) - 1; index > 0; index-- {
				value = fmt.Sprintf(`{"%s":%s}`, path[index], value)
			}
			raw := fmt.Appendf(nil, `{"schema":%q,"%s":%s}`, commandLimitsSchema, path[0], value)
			if _, err := decodeCommandLimits(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "must not be null") {
				t.Fatalf("null %s error = %v", name, err)
			}
		})
	}
}

func nullableCommandLimitPaths(value reflect.Type, prefix []string) [][]string {
	paths := make([][]string, 0)
	for index := range value.NumField() {
		field := value.Field(index)
		if field.Name == "Schema" || field.Type.Kind() != reflect.Pointer {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		path := append(append([]string(nil), prefix...), name)
		paths = append(paths, path)
		if field.Type.Elem().Kind() == reflect.Struct {
			paths = append(paths, nullableCommandLimitPaths(field.Type.Elem(), path)...)
		}
	}
	return paths
}

func TestCommandLimitsReadIsBoundedCancellationAwareAndOptional(t *testing.T) {
	limits, err := readCommandLimits(context.Background(), "")
	if err != nil || limits != (commandLimits{}) {
		t.Fatalf("default limits = %+v, %v", limits, err)
	}
	if _, err := decodeCommandLimits(nil, []byte("{}")); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := decodeCommandLimits(ctx, []byte("{}")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decode = %v", err)
	}
	path := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maximumCommandLimitsBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readCommandLimits(context.Background(), path); err == nil {
		t.Fatal("oversized limits document accepted")
	}
}

func TestCommandLimitsValidationDoesNotRequireAnArbitraryProbeCatalog(t *testing.T) {
	one := 1
	raw, err := encodeCommandJSON("limits document", commandLimitsDocument{
		Schema: commandLimitsSchema,
		Catalog: &commandCatalogLimits{
			MaxCatalogItems:     &one,
			MaxIdentifierBytes:  &one,
			MaxRevisionBytes:    &one,
			MaxDescriptionBytes: &one,
			MaxTemplateBytes:    &one,
			MaxCatalogBytes:     &one,
		},
		Source:   &commandArtifactLimits{MaxBytes: &one, MaxDepth: &one, MaxMembers: &one},
		Compiled: &commandArtifactLimits{MaxBytes: &one, MaxDepth: &one, MaxMembers: &one},
		Output:   &commandOutputLimitsDocument{MaxBytes: &one},
	})
	if err != nil {
		t.Fatal(err)
	}
	limits, err := decodeCommandLimits(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if limits.Catalog.MaxCatalogBytes != one || limits.Source.MaxBytes != one || limits.Compiled.MaxBytes != one || limits.Output.MaxBytes != one {
		t.Fatalf("minimum limits = %+v", limits)
	}
}

func FuzzCommandLimitsNeverPanic(f *testing.F) {
	f.Add([]byte("{\n  \"schema\": \"frostgrove.i18n.limits/v1\"\n}\n"))
	f.Add([]byte(`{"schema":"frostgrove.i18n.limits/v1","catalog":{"max_messages":8192}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = decodeCommandLimits(context.Background(), raw)
	})
}
