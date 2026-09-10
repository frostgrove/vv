package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestUsageCodecRejectsUnsafeScopeStrings(t *testing.T) {
	tests := []struct {
		name   string
		field  string
		mutate func(*i18n.GoUsageScope)
	}{
		{name: "GOOS control", field: "goos", mutate: func(scope *i18n.GoUsageScope) {
			scope.GOOS = "lin\x00ux"
			setUsageScopeEnvironment(scope, "GOOS", scope.GOOS)
		}},
		{name: "GOARCH bidi", field: "goarch", mutate: func(scope *i18n.GoUsageScope) {
			scope.GOARCH = "amd\u202e64"
			setUsageScopeEnvironment(scope, "GOARCH", scope.GOARCH)
		}},
		{name: "compiler control", field: "compiler", mutate: func(scope *i18n.GoUsageScope) {
			scope.Compiler = "g\x7fc"
		}},
		{name: "Go version control", field: "go_version", mutate: func(scope *i18n.GoUsageScope) {
			scope.GoVersion = "go1.26\rspoof"
			setUsageScopeEnvironment(scope, "GOVERSION", scope.GoVersion)
		}},
		{name: "toolchain bidi", field: "toolchain", mutate: func(scope *i18n.GoUsageScope) {
			scope.Toolchain = "go1.26\u202espoof"
		}},
		{name: "Go experiment control", field: "go_experiment", mutate: func(scope *i18n.GoUsageScope) {
			scope.GoExperiment = "loopvar\x00regabi"
			setUsageScopeEnvironment(scope, "GOEXPERIMENT", scope.GoExperiment)
		}},
		{name: "Go flags control", field: "go_flags", mutate: func(scope *i18n.GoUsageScope) {
			scope.GoFlags = "-mod=vendor\n-mod=mod"
			setUsageScopeEnvironment(scope, "GOFLAGS", scope.GoFlags)
		}},
		{name: "Go work control", field: "go_work", mutate: func(scope *i18n.GoUsageScope) {
			scope.GoWork = "off\x00active"
			setUsageScopeEnvironment(scope, "GOWORK", scope.GoWork)
		}},
		{name: "Go env bidi", field: "go_env", mutate: func(scope *i18n.GoUsageScope) {
			scope.GoEnv = "off\u202espoof"
			setUsageScopeEnvironment(scope, "GOENV", scope.GoEnv)
		}},
		{name: "build tag control", field: "build_tags[0]", mutate: func(scope *i18n.GoUsageScope) {
			scope.BuildTags = []string{"edge\x00hidden"}
		}},
		{name: "tool tag bidi", field: "tool_tags[0]", mutate: func(scope *i18n.GoUsageScope) {
			scope.ToolTags = []string{"tool\u202espoof"}
		}},
		{name: "release tag control", field: "release_tags[0]", mutate: func(scope *i18n.GoUsageScope) {
			scope.ReleaseTags = []string{"go1.26\x7f"}
		}},
		{name: "oversized tag atom", field: "build_tags[0]", mutate: func(scope *i18n.GoUsageScope) {
			scope.BuildTags = []string{strings.Repeat("x", maximumUsageScopeAtomBytes+1)}
		}},
		{name: "environment name control", field: "environment[8].name", mutate: func(scope *i18n.GoUsageScope) {
			setUsageScopeEnvironment(scope, "HIDDEN\x00NAME", "value")
		}},
		{name: "environment value bidi", field: "environment[8].value", mutate: func(scope *i18n.GoUsageScope) {
			setUsageScopeEnvironment(scope, "SAFE_NAME", "value\u202espoof")
		}},
		{name: "metadata kind control", field: "metadata[0].kind", mutate: func(scope *i18n.GoUsageScope) {
			scope.Metadata[0].Kind = "module\x00hidden"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := testUsageDocumentManifest()
			test.mutate(manifest.GoScope)
			if _, err := encodeUsage(manifest); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("unsafe scope encoding error = %v", err)
			}
			raw := forgeUsageDocument(t, manifest)
			if _, err := decodeUsage(raw); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("unsafe scope decoding error = %v", err)
			}
		})
	}
}

func TestUsageScopeValidationRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{'v', 0xff})
	tests := []struct {
		name   string
		field  string
		mutate func(*i18n.GoUsageScope)
	}{
		{name: "indexed field", field: "go_flags", mutate: func(scope *i18n.GoUsageScope) {
			scope.GoFlags = invalid
			setUsageScopeEnvironment(scope, "GOFLAGS", invalid)
		}},
		{name: "tag", field: "build_tags[0]", mutate: func(scope *i18n.GoUsageScope) {
			scope.BuildTags = []string{invalid}
		}},
		{name: "environment name", field: "environment[8].name", mutate: func(scope *i18n.GoUsageScope) {
			setUsageScopeEnvironment(scope, invalid, "value")
		}},
		{name: "environment value", field: "environment[8].value", mutate: func(scope *i18n.GoUsageScope) {
			setUsageScopeEnvironment(scope, "SAFE_NAME", invalid)
		}},
		{name: "metadata kind", field: "metadata[0].kind", mutate: func(scope *i18n.GoUsageScope) {
			scope.Metadata[0].Kind = invalid
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := testUsageDocumentManifest()
			test.mutate(manifest.GoScope)
			if _, err := encodeUsage(manifest); err == nil {
				t.Fatal("invalid UTF-8 scope was encoded")
			}
			manifest.GoScope.SourceDigest = i18n.ExpectedUsageSourceDigest(*manifest.GoScope)
			if err := validateUsageDocumentScope(context.Background(), manifest.GoScope, true, i18n.DefaultUsageLimits()); err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("invalid UTF-8 scope validation error = %v", err)
			}
		})
	}

	manifest := testUsageDocumentManifest()
	manifest.GoScope.GoFlags = "vv-invalid-marker"
	setUsageScopeEnvironment(manifest.GoScope, "GOFLAGS", manifest.GoScope.GoFlags)
	raw := forgeUsageDocument(t, manifest)
	invalidMarker := append([]byte(nil), []byte("vv-invalid-marker")...)
	invalidMarker[2] = 0xff
	raw = bytes.ReplaceAll(raw, []byte("vv-invalid-marker"), invalidMarker)
	if _, err := decodeUsage(raw); err == nil {
		t.Fatal("invalid UTF-8 usage JSON was decoded")
	}
}

func TestUsageCodecPreservesSafeFreeFormScopeStrings(t *testing.T) {
	manifest := testUsageDocumentManifest()
	scope := manifest.GoScope
	scope.GoVersion = "devel go1.27-abcdef"
	scope.Toolchain = "devel go1.27-abcdef gc"
	scope.GoExperiment = "loopvar,regabiargs"
	scope.GoFlags = "-tags=alpha,βeta\t-mod=vendor"
	setUsageScopeEnvironment(scope, "GOVERSION", scope.GoVersion)
	setUsageScopeEnvironment(scope, "GOEXPERIMENT", scope.GoExperiment)
	setUsageScopeEnvironment(scope, "GOFLAGS", scope.GoFlags)
	setUsageScopeEnvironment(scope, "CC", `C:\Program Files\LLVM\bin\clang.exe`)
	setUsageScopeEnvironment(scope, "GO111MODULE", "")
	setUsageScopeEnvironment(scope, "GOPROXY", "https://proxy.example.test,direct|off")
	scope.BuildTags = []string{"alpha", "βeta"}
	scope.ToolTags = []string{"amd64.v1", "goexperiment.regabiargs"}
	scope.ReleaseTags = []string{"go1.26", "go1.27"}

	raw, err := encodeUsage(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.GoScope.GoVersion != scope.GoVersion || decoded.GoScope.Toolchain != scope.Toolchain || decoded.GoScope.GoExperiment != scope.GoExperiment || decoded.GoScope.GoFlags != scope.GoFlags {
		t.Fatalf("decoded indexed scope = %+v", decoded.GoScope)
	}
	for _, setting := range []i18n.UsageSetting{
		{Name: "CC", Value: `C:\Program Files\LLVM\bin\clang.exe`},
		{Name: "GO111MODULE", Value: ""},
		{Name: "GOPROXY", Value: "https://proxy.example.test,direct|off"},
	} {
		if value, ok := usageScopeEnvironmentValue(decoded.GoScope, setting.Name); !ok || value != setting.Value {
			t.Fatalf("decoded environment %s = %q, %v", setting.Name, value, ok)
		}
	}
}

func forgeUsageDocument(t *testing.T, manifest i18n.UsageManifest) []byte {
	t.Helper()
	manifest.GoScope.SourceDigest = i18n.ExpectedUsageSourceDigest(*manifest.GoScope)
	manifest.ManifestDigest = i18n.ExpectedUsageManifestDigest(manifest)
	raw, err := encodeCommandJSON("forged usage manifest", usageDocument{
		Schema: usageSchema, Keys: manifest.Keys, Dynamic: manifest.Dynamic, Occurrences: manifest.Occurrences,
		GoScope: manifest.GoScope, ManifestDigest: manifest.ManifestDigest, Complete: manifest.Complete,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func setUsageScopeEnvironment(scope *i18n.GoUsageScope, name, value string) {
	for index := range scope.Environment {
		if scope.Environment[index].Name == name {
			scope.Environment[index].Value = value
			return
		}
	}
	scope.Environment = append(scope.Environment, i18n.UsageSetting{Name: name, Value: value})
	slices.SortFunc(scope.Environment, func(left, right i18n.UsageSetting) int {
		return strings.Compare(left.Name, right.Name)
	})
}

func usageScopeEnvironmentValue(scope *i18n.GoUsageScope, name string) (string, bool) {
	for _, setting := range scope.Environment {
		if setting.Name == name {
			return setting.Value, true
		}
	}
	return "", false
}
