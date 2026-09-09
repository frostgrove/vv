package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestCLILimitsApplyTheSourceBudgetToEverySourceConsumer(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "catalog.source.json")
	raw, err := i18n.EncodeSource(commandSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	maximum := len(raw) - 1
	limitsPath := writeCommandLimitsFixture(t, directory, commandLimitsDocument{
		Schema: commandLimitsSchema,
		Source: &commandArtifactLimits{MaxBytes: &maximum},
	})
	tests := []struct {
		name string
		args []string
	}{
		{name: "check", args: []string{"check", "-source", sourcePath, "-out", filepath.Join(directory, "report.json")}},
		{name: "compile", args: []string{"compile", "-source", sourcePath, "-out", filepath.Join(directory, "catalog.json")}},
		{name: "pseudo", args: []string{"pseudo", "-source", sourcePath, "-out", filepath.Join(directory, "pseudo.json"), "-locale", "en-XA"}},
		{name: "generate-go", args: []string{"generate-go", "-source", sourcePath, "-out", filepath.Join(directory, "messages.go"), "-package", "messages"}},
		{name: "export-ts", args: []string{"export-ts", "-source", sourcePath, "-out", filepath.Join(directory, "messages.d.ts"), "-manifest", filepath.Join(directory, "manifest.json")}},
		{name: "review", args: []string{"review", "-source", sourcePath, "-out", filepath.Join(directory, "reviewed.json"), "-locale", "ru", "-state", "approved"}},
		{name: "merge", args: []string{"merge", "-source", sourcePath, "-previous", sourcePath, "-out", filepath.Join(directory, "merged.json")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stderr := new(bytes.Buffer)
			arguments := append(test.args, "-limits", limitsPath)
			if code := runCLI(context.Background(), arguments, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), fmt.Sprintf("exceeds %d bytes", maximum)) {
				t.Fatalf("bounded command = %d: %s", code, stderr)
			}
		})
	}
}

func TestCLILimitsWidenCatalogDecodeMergeAndCompileTogether(t *testing.T) {
	const messages = 4105
	directory := t.TempDir()
	spec := largeCommandCatalog(messages)
	sourcePath := filepath.Join(directory, "large.source.json")
	raw, err := (i18n.SourceCodec{CatalogLimits: spec.Limits}).Encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	matching := messages
	matchingPath := writeCommandLimitsFixture(t, directory, commandLimitsDocument{
		Schema:  commandLimitsSchema,
		Catalog: &commandCatalogLimits{MaxMessages: &matching},
	})
	mergedPath := filepath.Join(directory, "merged.source.json")
	mergeArgs := []string{"merge", "-source", sourcePath, "-previous", sourcePath, "-out", mergedPath, "-limits", matchingPath}
	stderr := new(bytes.Buffer)
	if code := runCLI(context.Background(), mergeArgs, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("widened merge = %d: %s", code, stderr)
	}
	compiledPath := filepath.Join(directory, "catalog.json")
	compileArgs := []string{"compile", "-source", mergedPath, "-out", compiledPath, "-limits", matchingPath}
	stderr.Reset()
	if code := runCLI(context.Background(), compileArgs, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("widened compile = %d: %s", code, stderr)
	}
	artifact, err := os.Open(compiledPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, loadErr := (i18n.Loader{CatalogLimits: spec.Limits}).Load(context.Background(), artifact)
	closeErr := artifact.Close()
	if loadErr != nil || closeErr != nil || len(snapshot.Keys()) != messages {
		t.Fatalf("widened artifact = %d keys, load=%v close=%v", len(snapshot.Keys()), loadErr, closeErr)
	}

	lower := messages - 1
	lowerPath := writeCommandLimitsFixture(t, directory, commandLimitsDocument{
		Schema:  commandLimitsSchema,
		Catalog: &commandCatalogLimits{MaxMessages: &lower},
	})
	rejectedPath := filepath.Join(directory, "rejected.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"compile", "-source", sourcePath, "-out", rejectedPath, "-limits", lowerPath}, strings.NewReader(""), io.Discard, stderr); code != 1 {
		t.Fatalf("lower ceiling code = %d: %s", code, stderr)
	}
	if _, err := os.Stat(rejectedPath); !os.IsNotExist(err) {
		t.Fatalf("lower ceiling published output: %v", err)
	}
}

func TestCLILimitsKeepSourceCompiledAndDerivedOutputBudgetsIndependent(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "catalog.source.json")
	spec := commandSourceFixture(t)
	writeSourceFixture(t, sourcePath, spec)
	compiled, err := i18n.Compile(spec)
	if err == nil {
		t.Fatal("unreviewed fixture unexpectedly compiled")
	}
	reviewedPath := filepath.Join(directory, "reviewed.source.json")
	stderr := new(bytes.Buffer)
	if code := runCLI(context.Background(), []string{"review", "-source", sourcePath, "-out", reviewedPath, "-locale", "ru", "-state", "approved"}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("review = %d: %s", code, stderr)
	}
	reviewed := readSourceFixture(t, reviewedPath)
	compiled, err = i18n.Compile(reviewed)
	if err != nil {
		t.Fatal(err)
	}

	tooSmallCompiled := len(compiled) - 1
	compiledLimitsPath := writeCommandLimitsFixture(t, directory, commandLimitsDocument{
		Schema:   commandLimitsSchema,
		Compiled: &commandArtifactLimits{MaxBytes: &tooSmallCompiled},
	})
	compiledPath := filepath.Join(directory, "too-small.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"compile", "-source", reviewedPath, "-out", compiledPath, "-limits", compiledLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 1 {
		t.Fatalf("compiled ceiling code = %d: %s", code, stderr)
	}

	derivedMaximum := 32
	outputLimitsPath := writeCommandLimitsFixture(t, directory, commandLimitsDocument{
		Schema: commandLimitsSchema,
		Output: &commandOutputLimitsDocument{MaxBytes: &derivedMaximum},
	})
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"generate-go", "-source", reviewedPath, "-out", filepath.Join(directory, "messages.go"), "-package", "messages", "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), "exceeds 32 bytes") {
		t.Fatalf("derived ceiling code = %d: %s", code, stderr)
	}
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"check", "-source", reviewedPath, "-out", filepath.Join(directory, "report.json"), "-strict", "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), "check report") {
		t.Fatalf("report ceiling code = %d: %s", code, stderr)
	}
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"export-ts", "-source", reviewedPath, "-out", filepath.Join(directory, "messages.d.ts"), "-manifest", filepath.Join(directory, "manifest.json"), "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), "exceeds 32 bytes") {
		t.Fatalf("direct export ceiling code = %d: %s", code, stderr)
	}
	extractRoot := filepath.Join(directory, "extract")
	writeTestFile(t, filepath.Join(extractRoot, "go.mod"), "module example.test/limits\n")
	writeTestFile(t, filepath.Join(extractRoot, "main.go"), "package limits\n")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"extract", "-root", extractRoot, "-out", filepath.Join(directory, "usage.json"), "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), "output limit") {
		t.Fatalf("extract ceiling code = %d: %s", code, stderr)
	}
	publicationRoot := filepath.Join(directory, "publication")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"export-ts", "-source", reviewedPath, "-publication-root", publicationRoot, "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), "exceeds 32 bytes") {
		t.Fatalf("publication ceiling code = %d: %s", code, stderr)
	}
	if _, err := os.Stat(publicationRoot); !os.IsNotExist(err) {
		t.Fatalf("bounded publication created a root: %v", err)
	}
	independentCompiledPath := filepath.Join(directory, "compiled.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"compile", "-source", reviewedPath, "-out", independentCompiledPath, "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("output ceiling constrained compiled artifact = %d: %s", code, stderr)
	}
	independentSourcePath := filepath.Join(directory, "pseudo.source.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"pseudo", "-source", reviewedPath, "-out", independentSourcePath, "-locale", "en-XA", "-limits", outputLimitsPath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("output ceiling constrained source artifact = %d: %s", code, stderr)
	}
}

func TestCLILimitsRefuseInvalidLinkedCanceledAndStdinInputsWithoutFallback(t *testing.T) {
	directory := t.TempDir()
	invalidPath := filepath.Join(directory, "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{"schema":"frostgrove.i18n.limits/v2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runCLI(context.Background(), []string{"check", "-source", "-", "-limits", invalidPath}, panicReader{}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("invalid limits fallback code = %d", code)
	}
	linkedPath := filepath.Join(directory, "linked.json")
	if err := os.Symlink(invalidPath, linkedPath); err != nil {
		t.Fatal(err)
	}
	if code := runCLI(context.Background(), []string{"check", "-source", "-", "-limits", linkedPath}, panicReader{}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("linked limits code = %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := runCLI(ctx, []string{"check", "-source", "-", "-limits", invalidPath}, panicReader{}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("canceled limits code = %d", code)
	}

	raw, err := i18n.EncodeSource(commandSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	maximum := len(raw) - 1
	limitsPath := writeCommandLimitsFixture(t, directory, commandLimitsDocument{
		Schema: commandLimitsSchema,
		Source: &commandArtifactLimits{MaxBytes: &maximum},
	})
	if code := runCLI(context.Background(), []string{"check", "-source", "-", "-limits", limitsPath}, bytes.NewReader(raw), io.Discard, io.Discard); code != 1 {
		t.Fatalf("bounded stdin code = %d", code)
	}
}

func largeCommandCatalog(messages int) i18n.CatalogSpec {
	values := make([]i18n.MessageSpec, messages)
	for index := range values {
		values[index] = i18n.MessageSpec{
			ID:          fmt.Sprintf("m%05d", index),
			Revision:    "r1",
			Source:      "Message",
			Description: "Large limits fixture.",
			Output:      i18n.OutputPlain,
		}
	}
	limits := i18n.DefaultLimits()
	limits.MaxMessages = messages
	return i18n.CatalogSpec{
		Revision:        "large-v1",
		Profile:         i18n.GrammarProfile,
		SourceLocale:    "en",
		DefaultLocale:   "en",
		Supported:       []string{"en"},
		Required:        []string{"en"},
		DefaultTimeZone: "UTC",
		Limits:          limits,
		Modules:         []i18n.Module{{Name: "app", Messages: values}},
	}
}

func writeCommandLimitsFixture(t testing.TB, directory string, document commandLimitsDocument) string {
	t.Helper()
	raw, err := encodeCommandJSON("limits fixture", document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, fmt.Sprintf("limits-%d.json", len(readDirectoryNames(t, directory))))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readDirectoryNames(t testing.TB, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}
