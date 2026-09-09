package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestCLIEndToEndAuthoringPipeline(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "catalog.source.json")
	writeSourceFixture(t, sourcePath, commandSourceFixture(t))

	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	if code := runCLI(context.Background(), []string{"check", "-source", sourcePath}, strings.NewReader(""), stdout, stderr); code != 1 {
		t.Fatalf("unreviewed check code = %d, stdout=%s stderr=%s", code, stdout, stderr)
	}
	var report i18n.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Errors == 0 {
		t.Fatalf("check report = %+v, %v", report, err)
	}

	reviewedPath := filepath.Join(directory, "reviewed.source.json")
	stdout.Reset()
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"review", "-source", sourcePath, "-out", reviewedPath, "-locale", "RU", "-state", "approved"}, strings.NewReader(""), stdout, stderr); code != 0 {
		t.Fatalf("review code = %d: %s", code, stderr)
	}
	approved := readSourceFixture(t, reviewedPath)
	translation := approved.Modules[0].Messages[0].Translations[0]
	override := approved.Overrides[0]
	if translation.Review != i18n.ReviewApproved || override.Review != i18n.ReviewApproved || translation.SourceDigest == "" || translation.SourceDigest != override.SourceDigest || translation.ReviewDigest == "" || override.ReviewDigest == "" {
		t.Fatalf("reviewed identities = %+v, %+v", translation, override)
	}

	reportPath := filepath.Join(directory, "report.json")
	if code := runCLI(context.Background(), []string{"check", "-source", reviewedPath, "-out", reportPath, "-strict"}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("approved check code = %d: %s", code, stderr)
	}
	if code := runCLI(context.Background(), []string{"check", "-source", reviewedPath, "-out", reportPath, "-strict", "-check"}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("report drift check code = %d: %s", code, stderr)
	}

	artifactPath := filepath.Join(directory, "catalog.json")
	if code := runCLI(context.Background(), []string{"compile", "-source", reviewedPath, "-out", artifactPath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("compile code = %d: %s", code, stderr)
	}
	artifact, err := os.Open(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, loadErr := i18n.Load(context.Background(), artifact)
	closeErr := artifact.Close()
	if loadErr != nil || closeErr != nil || snapshot.Revision() != "catalog-v1" {
		t.Fatalf("loaded artifact = (%v, %v), revision=%q", loadErr, closeErr, snapshot.Revision())
	}

	generatedPath := filepath.Join(directory, "messages_gen.go")
	if code := runCLI(context.Background(), []string{"generate-go", "-source", reviewedPath, "-out", generatedPath, "-package", "messages"}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("generate-go code = %d: %s", code, stderr)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil || !bytes.Contains(generated, []byte("type Catalog struct")) || !bytes.Contains(generated, []byte("ContractRef")) {
		t.Fatalf("generated Go = %s, %v", generated, err)
	}

	typeScriptPath := filepath.Join(directory, "messages.d.ts")
	manifestPath := filepath.Join(directory, "messages.public.json")
	addressPath := filepath.Join(directory, "messages.address")
	if code := runCLI(context.Background(), []string{"export-ts", "-source", reviewedPath, "-out", typeScriptPath, "-manifest", manifestPath, "-address-out", addressPath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("export-ts code = %d: %s", code, stderr)
	}
	assertContainsFile(t, typeScriptPath, "MessageContracts")
	assertContainsFile(t, manifestPath, `"formattingParity":false`)
	assertContainsFile(t, addressPath, "sha256:")
	expectedExport, err := i18n.ExportPublic(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(readTestFile(t, addressPath)) != expectedExport.Address {
		t.Fatal("export address does not identify the exact on-disk export bundle")
	}
	publicationRoot := filepath.Join(directory, "public.i18n")
	if code := runCLI(context.Background(), []string{"export-ts", "-source", reviewedPath, "-publication-root", publicationRoot}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("generational export-ts code = %d: %s", code, stderr)
	}
	pointer, publicationFiles, err := readPublicPublication(context.Background(), publicationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if pointer.Address != expectedExport.Address || !matchesPublication(publicationFiles, expectedExport) {
		t.Fatalf("generational public export = %+v", pointer)
	}
	if code := runCLI(context.Background(), []string{"export-ts", "-source", reviewedPath, "-publication-root", publicationRoot, "-check"}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("generational export drift check code = %d: %s", code, stderr)
	}

	pseudoPath := filepath.Join(directory, "pseudo.source.json")
	if code := runCLI(context.Background(), []string{"pseudo", "-source", reviewedPath, "-out", pseudoPath, "-locale", "en-XA", "-mode", "accent"}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("pseudo code = %d: %s", code, stderr)
	}
	pseudo := readSourceFixture(t, pseudoPath)
	var pseudoReview i18n.ReviewState
	for _, candidate := range pseudo.Modules[0].Messages[0].Translations {
		if candidate.Locale == "en-XA" {
			pseudoReview = candidate.Review
		}
	}
	if !slices.Contains(pseudo.Supported, "en-XA") || len(pseudo.Modules[0].Messages[0].Translations) != 2 || pseudoReview != i18n.ReviewRequired {
		t.Fatalf("pseudolocale source = %+v", pseudo)
	}
}

func TestCLIExtractUsageAndCheckMissingContract(t *testing.T) {
	directory := t.TempDir()
	goPath := filepath.Join(directory, "consumer.go")
	writeTestFile(t, goPath, `package consumer
import "github.com/frostgrove/vv/i18n"
func use(snapshot *i18n.Snapshot) {
  _, _ = snapshot.Bind("app.welcome")
  _, _ = snapshot.Bind("app.missing")
}
`)
	usagePath := filepath.Join(directory, "usage.json")
	stderr := new(bytes.Buffer)
	if code := runCLI(context.Background(), []string{"extract", "-root", directory, "-out", usagePath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("extract code = %d: %s", code, stderr)
	}
	sourcePath := filepath.Join(directory, "catalog.source.json")
	spec := commandSourceFixture(t)
	spec.Overrides = nil
	spec.Required = []string{"en"}
	spec.Modules[0].Messages[0].Translations[0].Review = i18n.ReviewApproved
	writeSourceFixture(t, sourcePath, spec)
	stdout := new(bytes.Buffer)
	if code := runCLI(context.Background(), []string{"check", "-source", sourcePath, "-usage", usagePath}, strings.NewReader(""), stdout, stderr); code != 1 {
		t.Fatalf("usage check code = %d: stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout.String(), "app.missing") {
		t.Fatalf("missing extracted key absent from report: %s", stdout)
	}
}

func TestCLIStdinStdoutDriftAndUsageFailures(t *testing.T) {
	raw, err := i18n.EncodeSource(commandSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	code := runCLI(context.Background(), []string{"review", "-source", "-", "-out", "-", "-locale", "ru", "-state", "approved"}, bytes.NewReader(raw), stdout, stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"review":"approved"`)) {
		t.Fatalf("stdin/stdout review = %d, %s, %s", code, stdout, stderr)
	}
	if code := runCLI(context.Background(), []string{"check", "-source", "-", "-out", "-", "-check"}, bytes.NewReader(raw), io.Discard, stderr); code != 2 {
		t.Fatalf("stdout drift code = %d", code)
	}
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"compile"},
		{"export-ts", "-source", "x", "-out", "-", "-manifest", "m"},
		{"export-ts", "-source", "x", "-publication-root", "-"},
		{"export-ts", "-source", "x", "-publication-root", "public", "-out", "types.d.ts"},
		{"export-ts", "-source", "x", "-publication-root", "public", "-manifest", "manifest.json"},
		{"export-ts", "-source", "x", "-publication-root", "public", "-address-out", "address"},
	} {
		if code := runCLI(context.Background(), args, strings.NewReader(""), io.Discard, io.Discard); code != 2 {
			t.Fatalf("usage args %v code = %d", args, code)
		}
	}
	if code := runCLI(context.Background(), []string{"pseudo", "-source", "-", "-out", "-", "-locale", "en-XA", "-mode", "unknown"}, bytes.NewReader(raw), io.Discard, io.Discard); code != 2 {
		t.Fatalf("unknown pseudo mode code = %d", code)
	}
	if code := runCLI(context.Background(), []string{"help"}, strings.NewReader(""), stdout, stderr); code != 0 {
		t.Fatalf("help code = %d", code)
	}
	if code := runCLI(nil, []string{"help"}, strings.NewReader(""), stdout, stderr); code != 2 {
		t.Fatalf("nil context code = %d", code)
	}
}

func TestReviewFiltersAndRefusesInvalidApproval(t *testing.T) {
	spec := commandSourceFixture(t)
	originalDigest := spec.Modules[0].Messages[0].Translations[0].SourceDigest
	updated, count, err := reviewCatalog(spec, reviewSelector{locale: "ru", key: "app.welcome", scope: "translations", state: i18n.ReviewRejected})
	if err != nil || count != 1 || updated.Modules[0].Messages[0].Translations[0].Review != i18n.ReviewRejected || updated.Overrides[0].Review != i18n.ReviewRequired {
		t.Fatalf("filtered review = %d, %+v, %v", count, updated, err)
	}
	if updated.Modules[0].Messages[0].Translations[0].SourceDigest != originalDigest {
		t.Fatal("review changed a current digest")
	}
	if spec.Modules[0].Messages[0].Translations[0].Review != i18n.ReviewRequired {
		t.Fatal("review mutated its input catalog")
	}
	if _, _, err := reviewCatalog(commandSourceFixture(t), reviewSelector{locale: "fr", scope: "all", state: i18n.ReviewApproved}); err == nil {
		t.Fatal("missing review target accepted")
	}
	broken := commandSourceFixture(t)
	broken.Modules[0].Messages[0].Translations[0].Text = "Broken {$missing}"
	if _, _, err := reviewCatalog(broken, reviewSelector{locale: "ru", scope: "all", state: i18n.ReviewApproved}); err == nil || !strings.Contains(err.Error(), "structurally invalid") {
		t.Fatalf("invalid approval error = %v", err)
	}
	if _, _, err := reviewCatalogContext(nil, spec, reviewSelector{}); err == nil {
		t.Fatal("nil review context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := reviewCatalogContext(ctx, spec, reviewSelector{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled review = %v", err)
	}
}

func TestReviewApprovalScalesPastRetainedNonStructuralFindings(t *testing.T) {
	spec := scaledReviewCatalog(4105, false)
	report := i18n.Check(spec, i18n.CheckPolicy{})
	if !report.Truncated || report.StructuralErrors != 0 || len(report.Findings) != 4096 {
		t.Fatalf("pre-review report = %+v", report)
	}
	updated, count, err := reviewCatalog(spec, reviewSelector{locale: "fr", key: "app.m00000", scope: "translations", state: i18n.ReviewApproved})
	if err != nil || count != 1 || updated.Modules[0].Messages[0].Translations[1].Review != i18n.ReviewApproved {
		t.Fatalf("scaled review = count %d, state %v, error %v", count, updated.Modules[0].Messages[0].Translations[1].Review, err)
	}
}

func TestReviewApprovalFindsStructuralInvalidityBeyondRetainedFindings(t *testing.T) {
	spec := scaledReviewCatalog(4105, true)
	report := i18n.Check(spec, i18n.CheckPolicy{})
	if !report.Truncated || report.StructuralErrors < 1 || len(report.Findings) != 4096 {
		t.Fatalf("pre-review report = errors %d, warnings %d, structural %d, retained %d, truncated %v", report.Errors, report.Warnings, report.StructuralErrors, len(report.Findings), report.Truncated)
	}
	for _, finding := range report.Findings {
		if finding.Status == i18n.CheckInvalid {
			t.Fatalf("structural finding unexpectedly retained: %+v", finding)
		}
	}
	finding, ok := report.FirstStructuralError()
	if !ok || finding.Status != i18n.CheckInvalid || finding.Key != "app.m04104" {
		t.Fatalf("first structural error = %+v, %v", finding, ok)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded i18n.Report
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	decodedFinding, ok := decoded.FirstStructuralError()
	if !ok || decodedFinding != finding || decoded.StructuralErrors != report.StructuralErrors {
		t.Fatalf("decoded structural summary = %+v, %v, %d", decodedFinding, ok, decoded.StructuralErrors)
	}
	if _, _, err := reviewCatalog(spec, reviewSelector{locale: "fr", key: "app.m00000", scope: "translations", state: i18n.ReviewApproved}); err == nil || !strings.Contains(err.Error(), "modules[0].messages[4104]") {
		t.Fatalf("scaled invalid approval error = %v", err)
	}
}

func scaledReviewCatalog(messages int, invalidLast bool) i18n.CatalogSpec {
	values := make([]i18n.MessageSpec, messages)
	for index := range values {
		id := fmt.Sprintf("m%05d", index)
		message := i18n.MessageSpec{ID: id, Revision: "r1", Source: "Message", Description: "Scaled review message."}
		digest, err := i18n.ExpectedSourceDigestForLocale(i18n.GrammarProfile, "en", "app", message)
		if err != nil {
			panic(err)
		}
		state := i18n.ReviewRequired
		if invalidLast && index == messages-1 {
			state = i18n.ReviewState(255)
		}
		message.Translations = []i18n.Translation{{
			Locale: "ru", Text: "Сообщение", Review: state, ContractRevision: message.Revision, SourceDigest: digest,
		}}
		if index == 0 {
			message.Translations = append(message.Translations, i18n.Translation{
				Locale: "fr", Text: "Message", Review: i18n.ReviewRequired, ContractRevision: message.Revision, SourceDigest: digest,
			})
		}
		values[index] = message
	}
	limits := i18n.DefaultLimits()
	limits.MaxMessages = messages
	limits.MaxTranslations = messages*2 + 1
	return i18n.CatalogSpec{
		Revision: "scaled-review", Profile: i18n.GrammarProfile, SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "ru", "fr"}, Required: []string{"ru"}, Limits: limits,
		Modules: []i18n.Module{{Name: "app", Messages: values}},
	}
}

func TestCLIPropagatesCancellationAndWriterFailure(t *testing.T) {
	raw, err := i18n.EncodeSource(commandSourceFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := runCLI(ctx, []string{"check", "-source", "-"}, bytes.NewReader(raw), io.Discard, io.Discard); code != 1 {
		t.Fatalf("canceled code = %d", code)
	}
	if code := runCLI(context.Background(), []string{"check", "-source", "-"}, bytes.NewReader(raw), failingWriter{}, io.Discard); code != 1 {
		t.Fatalf("writer failure code = %d", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("sink failed") }

func commandSourceFixture(t testing.TB) i18n.CatalogSpec {
	t.Helper()
	message := i18n.MessageSpec{
		ID: "welcome", Revision: "welcome-v1", Source: "Hello {$name}", Description: "Greeting shown after sign in.",
		Arguments: []i18n.ArgumentSpec{{Name: "name", Type: i18n.TypeText, Required: true}},
		Output:    i18n.OutputPlain, Override: i18n.OverrideApplication, Public: true,
	}
	digest, err := i18n.ExpectedSourceDigestForLocale(i18n.GrammarProfile, "en", "app", message)
	if err != nil {
		t.Fatal(err)
	}
	message.Translations = []i18n.Translation{{
		Locale: "ru", Text: "Привет, {$name}", Review: i18n.ReviewRequired,
		ContractRevision: message.Revision, SourceDigest: digest,
	}}
	return i18n.CatalogSpec{
		Revision: "catalog-v1", Profile: i18n.GrammarProfile, SourceLocale: "en", DefaultLocale: "en",
		Supported: []string{"en", "ru"}, Required: []string{"en", "ru"}, DefaultTimeZone: "UTC", TimeZoneDataVersion: "2025b",
		Modules: []i18n.Module{{Name: "app", Messages: []i18n.MessageSpec{message}}},
		Overrides: []i18n.Override{{
			Key: "app.welcome", Locale: "ru", Text: "Здравствуйте, {$name}", Review: i18n.ReviewRequired,
			ContractRevision: message.Revision, SourceDigest: digest,
		}},
	}
}

func writeSourceFixture(t testing.TB, path string, spec i18n.CatalogSpec) {
	t.Helper()
	raw, err := i18n.EncodeSource(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSourceFixture(t testing.TB, path string) i18n.CatalogSpec {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := i18n.DecodeSource(context.Background(), bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func assertContainsFile(t testing.TB, path, fragment string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), fragment) {
		t.Fatalf("%s does not contain %q: %s", path, fragment, raw)
	}
}

func readTestFile(t testing.TB, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
