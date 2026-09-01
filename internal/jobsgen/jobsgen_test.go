package jobsgen

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationMaterializesAutomaticDeclarationsAndPreservesManifestChoices(t *testing.T) {
	directory := fixturePackage(t, `package sample

import (
	"context"

	queue "github.com/frostgrove/vv/jobs"
)

type Payload struct {
	ID string `+"`json:\"id\"`"+`
}

var Beta = queue.Declare[Payload](queue.Heavy)

var Alpha = queue.Auto(queue.Handler[Payload](func(context.Context, Payload) error {
	return nil
}))
`)
	var log bytes.Buffer
	if err := Run(&Options{Dir: directory, Log: &log}); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "jobs.manifest.yml")
	generatedPath := filepath.Join(directory, "vv_jobs_gen.go")
	document, err := readManifest(manifestPath, "example.com/sample")
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Jobs) != 2 || document.Jobs[0].Variable != "Alpha" || document.Jobs[0].Name != "sample.alpha" || document.Jobs[1].Variable != "Beta" || document.Jobs[1].Name != "sample.beta" {
		t.Fatalf("manifest jobs = %#v", document.Jobs)
	}
	for _, entry := range document.Jobs {
		if entry.Codec.Kind != "safe-json" || entry.Codec.Version != 1 || entry.Partition != "global" || entry.Payload.Fingerprint == "" {
			t.Fatalf("manifest defaults = %#v", entry)
		}
	}
	generated := readFile(t, generatedPath)
	for _, expected := range []string{"_vvjobs.MustMaterialize(Alpha", "_vvjobs.MustMaterialize(Beta", "var VVJobsCatalog", "_vvjobs.JSON[Payload]", "_vvjobs.PartitionGlobal"} {
		if !bytes.Contains(generated, []byte(expected)) {
			t.Fatalf("generated source lacks %q:\n%s", expected, generated)
		}
	}
	if bytes.Contains(generated, []byte("//")) || bytes.Contains(generated, []byte("/*")) {
		t.Fatalf("generated source contains comments:\n%s", generated)
	}
	if err := Run(&Options{Dir: directory, Check: true}); err != nil {
		t.Fatalf("fresh check = %v", err)
	}
	writeFile(t, filepath.Join(directory, "catalog_test.go"), `package sample

import "testing"

func TestGeneratedCatalog(t *testing.T) {
	if VVJobsCatalog.Len() != 2 {
		t.Fatalf("catalog length = %d", VVJobsCatalog.Len())
	}
	if Alpha.Name().String() != "sample.alpha" || Beta.Name().String() != "sample.beta" {
		t.Fatalf("names = %s, %s", Alpha.Name(), Beta.Name())
	}
}
`)
	goTest(t, directory)
	document.Jobs[0].Name = "billing.alpha"
	document.Jobs[0].Codec.Version = 2
	document.Jobs[0].Partition = "tenant_required"
	manifestSource, err := marshalManifest(*document)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifestPath, string(manifestSource))
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatal(err)
	}
	generated = readFile(t, generatedPath)
	for _, expected := range []string{"_vvJobsMustName(\"billing.alpha\")", "_vvjobs.SchemaVersion(2)", "_vvjobs.PartitionTenantRequired"} {
		if !bytes.Contains(generated, []byte(expected)) {
			t.Fatalf("regenerated source lacks %q:\n%s", expected, generated)
		}
	}
}

func TestCheckReportsDriftWithoutWriting(t *testing.T) {
	directory := fixturePackage(t, `package sample

import jobs "github.com/frostgrove/vv/jobs"

type Payload struct {
	Value string
}

var Work = jobs.Declare[Payload]()
`)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatal(err)
	}
	generatedPath := filepath.Join(directory, "vv_jobs_gen.go")
	stale := append(readFile(t, generatedPath), '\n')
	writeFile(t, generatedPath, string(stale))
	err := Run(&Options{Dir: directory, Check: true})
	var drift *DriftError
	if !errors.As(err, &drift) || len(drift.Paths) != 1 || drift.Paths[0] != generatedPath {
		t.Fatalf("check error = %v", err)
	}
	if !bytes.Equal(stale, readFile(t, generatedPath)) {
		t.Fatal("check changed generated source")
	}
}

func TestDiscoveryRejectsDeclarationsOutsidePackageInitializers(t *testing.T) {
	directory := fixturePackage(t, `package sample

import jobs "github.com/frostgrove/vv/jobs"

type Payload struct {
	Value string
}

func hidden() *jobs.Automatic[Payload] {
	return jobs.Declare[Payload]()
}
`)
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "only supported as a direct package-level variable initializer") {
		t.Fatalf("generation error = %v", err)
	}
}

func TestGenerationQualifiesExternalPayloadTypes(t *testing.T) {
	directory := fixturePackage(t, `package sample

import (
	jobs "github.com/frostgrove/vv/jobs"
	model "example.com/sample/model"
)

var Work = jobs.Declare[model.Payload]()
`)
	modelDirectory := filepath.Join(directory, "model")
	if err := os.Mkdir(modelDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(modelDirectory, "payload.go"), `package model

type Payload struct {
	Value string
}
`)
	if err := Run(&Options{Dir: directory}); err != nil {
		t.Fatal(err)
	}
	generated := readFile(t, filepath.Join(directory, "vv_jobs_gen.go"))
	if !bytes.Contains(generated, []byte(`_vvjobstype0 "example.com/sample/model"`)) || !bytes.Contains(generated, []byte("_vvjobs.JSON[_vvjobstype0.Payload]")) {
		t.Fatalf("external payload import is missing:\n%s", generated)
	}
	goTest(t, directory)
}

func TestGenerationRefusesToOverwriteAuthoredGoFile(t *testing.T) {
	directory := fixturePackage(t, `package sample

import jobs "github.com/frostgrove/vv/jobs"

type Payload struct {
	Value string
}

var Work = jobs.Declare[Payload]()
`)
	path := filepath.Join(directory, "vv_jobs_gen.go")
	writeFile(t, path, "package sample\n")
	err := Run(&Options{Dir: directory})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite authored file") {
		t.Fatalf("generation error = %v", err)
	}
}

func fixturePackage(t *testing.T, source string) string {
	t.Helper()
	directory := t.TempDir()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.com/sample\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => %s\n", filepath.ToSlash(root))
	writeFile(t, filepath.Join(directory, "go.mod"), module)
	writeFile(t, filepath.Join(directory, "jobs.go"), source)
	return directory
}

func goTest(t *testing.T, directory string) {
	t.Helper()
	command := exec.Command("go", "test", "./...")
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, output)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func writeFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
