package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func publicSchemaSurface(t *testing.T, data []byte) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "schema.go", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	surface := map[string]string{}
	render := func(node ast.Node) string {
		var out bytes.Buffer
		if err := format.Node(&out, token.NewFileSet(), node); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	for _, declaration := range file.Decls {
		switch d := declaration.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && ast.IsExported(d.Name.Name) {
				surface[d.Name.Name] = render(d.Type)
			}
		case *ast.GenDecl:
			for _, raw := range d.Specs {
				switch spec := raw.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(spec.Name.Name) {
						surface[spec.Name.Name] = render(spec.Type)
					}
				case *ast.ValueSpec:
					for index, name := range spec.Names {
						if !ast.IsExported(name.Name) {
							continue
						}
						if d.Tok == token.CONST {
							surface[name.Name] = render(spec.Values[index])
						} else if spec.Type != nil {
							surface[name.Name] = render(spec.Type)
						} else if composite, ok := spec.Values[index].(*ast.CompositeLit); ok {
							surface[name.Name] = render(composite.Type)
						}
					}
				}
			}
		}
	}
	return surface
}

func TestEveryV1ExportAndLegacyLayoutRemainsCompatible(t *testing.T) {
	legacy, err := os.ReadFile("testdata/v1_api.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	code, err := render(validRegistryFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	before, after := publicSchemaSurface(t, legacy), publicSchemaSurface(t, code)
	if len(before) != 109 {
		t.Fatalf("legacy surface control = %d, want 109 symbols", len(before))
	}
	for name, shape := range before {
		actual, ok := after[name]
		if !ok {
			t.Errorf("v1 export removed: %s", name)
			continue
		}
		if name == "ContractVersion" || name == "MigrationPolicy" {
			continue
		}
		if name == "ScopeVersion" {
			continue
		}
		if actual != shape {
			t.Errorf("v1 %s changed: %s -> %s", name, shape, actual)
		}
	}
}

func TestRuntimeResourceNameNormalizationDelegatesToGeneratedAuthority(t *testing.T) {
	data, err := os.ReadFile("../../otel/telemetry.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "telemetry.go", data, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "normalizeResourceName" {
			continue
		}
		if len(function.Body.List) != 2 {
			t.Fatal("resource normalization has logic outside the generated authority")
		}
		condition, ok := function.Body.List[0].(*ast.IfStmt)
		if !ok {
			t.Fatal("resource normalization does not reject through the generated authority")
		}
		negation, ok := condition.Cond.(*ast.UnaryExpr)
		if !ok || negation.Op != token.NOT {
			t.Fatal("resource normalization does not negate generated admission")
		}
		call, ok := negation.X.(*ast.CallExpr)
		if !ok {
			t.Fatal("resource normalization does not call generated admission")
		}
		name, named := call.Fun.(*ast.Ident)
		if !named || name.Name != "ValidResourceName" || len(call.Args) != 1 {
			t.Fatal("resource normalization bypasses ValidResourceName")
		}
		argument, named := call.Args[0].(*ast.Ident)
		if !named || argument.Name != "name" || condition.Else != nil || len(condition.Body.List) != 1 {
			t.Fatal("resource normalization does not delegate the input directly")
		}
		rejection, ok := condition.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(rejection.Results) != 1 {
			t.Fatal("resource normalization does not reject invalid input")
		}
		empty, literal := rejection.Results[0].(*ast.BasicLit)
		if !literal || empty.Kind != token.STRING || empty.Value != `""` {
			t.Fatal("resource normalization does not erase invalid input")
		}
		returned, ok := function.Body.List[1].(*ast.ReturnStmt)
		if !ok || len(returned.Results) != 1 {
			t.Fatal("resource normalization does not return exactly one admitted name")
		}
		identifier, named := returned.Results[0].(*ast.Ident)
		if !named || identifier.Name != "name" {
			t.Fatal("resource normalization changes an admitted name")
		}
		return
	}
	t.Fatal("normalizeResourceName is absent")
}

func TestMakeGenerateExplicitlyInvokesOTelSatellite(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$PWD\" \"$*\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "generate", "GO="+fake)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make generate routing: %v\n%s", err, output)
	}
	line := filepath.Join(root, "otel") + "|generate ./..."
	if strings.Count(string(output), line) != 1 {
		t.Fatalf("OTel satellite generation did not run exactly once:\n%s", output)
	}
	directive, err := os.ReadFile("../../otel/doc.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(directive), "-out schema_gen.go -manifest wire_manifest.json") {
		t.Fatal("OTel directive does not select both local outputs")
	}
}

func TestScopeVersionGenerationUpdatesBothArtifacts(t *testing.T) {
	r := validRegistryFixture(t)
	r.Scope.Version = "v9.8.7"
	code, err := render(r)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := renderManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "v9.8.7") || !strings.Contains(string(manifest), "v9.8.7") {
		t.Fatal("version did not reach both generated artifacts")
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := writeRegistry(path, r); err != nil {
		t.Fatal(err)
	}
	written, err := readRegistry(path)
	if err != nil || written.Scope.Version != "v9.8.7" {
		t.Fatalf("version persistence = %s/%v", written.Scope.Version, err)
	}
}

func TestCLIWriteVersionAndStaleChecksHandleBothArtifacts(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := t.TempDir()
	input := filepath.Join(fixture, "internal", "otelreg", "registry.json")
	if err := os.MkdirAll(filepath.Dir(input), 0755); err != nil {
		t.Fatal(err)
	}
	r := validRegistryFixture(t)
	if err := writeRegistry(input, r); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, c := range r.Components {
		for _, v := range vocabularies(c) {
			paths := []string{v.Source.File}
			for _, ref := range v.Source.Interfaces {
				paths = append(paths, ref.File)
			}
			for _, path := range paths {
				if path != "" {
					dirs[strings.Split(path, "/")[0]] = true
				}
			}
		}
	}
	for _, shape := range r.SourceShapes {
		if shape.File != "" {
			dirs[strings.Split(shape.File, "/")[0]] = true
		}
	}
	dirs["docs"] = true
	for dir := range dirs {
		if err := os.Symlink(filepath.Join(root, dir), filepath.Join(fixture, dir)); err != nil {
			t.Fatal(err)
		}
	}
	codePath, wirePath := filepath.Join(fixture, "schema_gen.go"), filepath.Join(fixture, "wire_manifest.json")
	binary := filepath.Join(t.TempDir(), "vv-otel-gen")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generator: %v\n%s", err, out)
	}
	args := []string{"-registry", input, "-out", codePath, "-manifest", wirePath}
	run := func(extra ...string) ([]byte, error) {
		return exec.Command(binary, append(append([]string{}, args...), extra...)...).CombinedOutput()
	}
	if out, err := run("-write-scope-version", "v8.7.6"); err != nil {
		t.Fatalf("write version: %v\n%s", err, out)
	}
	written, err := readRegistry(input)
	if err != nil || written.Scope.Version != "v8.7.6" {
		t.Fatalf("registry version persistence: %s/%v", written.Scope.Version, err)
	}
	for _, path := range []string{codePath, wirePath} {
		original, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(original), "v8.7.6") {
			t.Fatalf("output version did not update: %s/%v", path, err)
		}
		if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
			t.Fatal(err)
		}
		before := map[string][]byte{}
		for _, target := range []string{input, codePath, wirePath} {
			before[target], err = os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
		}
		if out, err := run("-check"); err == nil || !strings.Contains(string(out), "stale") {
			t.Fatalf("stale %s was not rejected: %s/%v", path, out, err)
		}
		for target, want := range before {
			after, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(after, want) {
				t.Fatalf("check rewrote %s", target)
			}
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := run("-check"); err != nil {
		t.Fatalf("fresh check: %v\n%s", err, out)
	}
}
