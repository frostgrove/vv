package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostgrove/vv/i18n"
)

func TestCLIMergeCarriesPriorWorkAndSupportsDriftChecks(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.json")
	previousPath := filepath.Join(directory, "previous.json")
	outPath := filepath.Join(directory, "merged.json")
	newSource, previous := commandMergeFixtures(t)
	writeSourceFixture(t, sourcePath, newSource)
	writeSourceFixture(t, previousPath, previous)

	stderr := new(bytes.Buffer)
	args := []string{"merge", "-source", sourcePath, "-previous", previousPath, "-out", outPath}
	if code := runCLI(context.Background(), args, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("merge code = %d: %s", code, stderr)
	}
	merged := readSourceFixture(t, outPath)
	translation := merged.Modules[0].Messages[0].Translations[0]
	override := merged.Overrides[0]
	if merged.Revision != "catalog-v2" || translation != previous.Modules[0].Messages[0].Translations[0] || override != previous.Overrides[0] {
		t.Fatalf("merged source = %+v", merged)
	}
	if code := runCLI(context.Background(), append(args, "-check"), strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("current merge check code = %d: %s", code, stderr)
	}
	stable, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	newSource.Revision = "catalog-v3"
	writeSourceFixture(t, sourcePath, newSource)
	stderr.Reset()
	if code := runCLI(context.Background(), append(args, "-check"), strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), "stale") {
		t.Fatalf("stale merge check = %d: %s", code, stderr)
	}
	if after, err := os.ReadFile(outPath); err != nil || !bytes.Equal(after, stable) {
		t.Fatalf("drift check mutated output: %v", err)
	}
}

func TestCLIExtractAndMergedAuthoringBranchesComposeOnlyAtCheck(t *testing.T) {
	directory := t.TempDir()
	consumerRoot := filepath.Join(directory, "consumer")
	writeTestFile(t, filepath.Join(consumerRoot, "go.mod"), "module example.test/consumer\n")
	writeTestFile(t, filepath.Join(consumerRoot, "use.go"), `package consumer
import "github.com/frostgrove/vv/i18n"
func use(snapshot *i18n.Snapshot) { _, _ = snapshot.Bind("app.welcome") }
`)
	usagePath := filepath.Join(directory, "usage.json")
	stderr := new(bytes.Buffer)
	if code := runCLI(context.Background(), []string{"extract", "-root", consumerRoot, "-out", usagePath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("extract branch code = %d: %s", code, stderr)
	}
	usageRaw, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := decodeUsage(usageRaw)
	if err != nil || len(usage.Occurrences) != 1 || usage.Occurrences[0].Path != "example.test/consumer/use.go" {
		t.Fatalf("extracted branch = %+v, %v", usage, err)
	}

	next, previous := commandMergeFixtures(t)
	priorTranslation := &previous.Modules[0].Messages[0].Translations[0]
	priorTranslation.Review = i18n.ReviewApproved
	priorTranslation.ReviewDigest, err = i18n.ExpectedReviewDigest(priorTranslation.SourceDigest, priorTranslation.Locale, priorTranslation.Text)
	if err != nil {
		t.Fatal(err)
	}
	priorOverride := &previous.Overrides[0]
	priorOverride.Review = i18n.ReviewApproved
	priorOverride.ReviewDigest, err = i18n.ExpectedReviewDigest(priorOverride.SourceDigest, priorOverride.Locale, priorOverride.Text)
	if err != nil {
		t.Fatal(err)
	}
	previousPath := filepath.Join(directory, "previous.json")
	nextPath := filepath.Join(directory, "next.json")
	mergedPath := filepath.Join(directory, "merged.json")
	reviewedPath := filepath.Join(directory, "reviewed.json")
	writeSourceFixture(t, previousPath, previous)
	writeSourceFixture(t, nextPath, next)
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"merge", "-source", nextPath, "-previous", previousPath, "-out", mergedPath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("merge branch code = %d: %s", code, stderr)
	}
	merged := readSourceFixture(t, mergedPath)
	carriedTranslation := merged.Modules[0].Messages[0].Translations[0]
	carriedOverride := merged.Overrides[0]
	if carriedTranslation != *priorTranslation || carriedOverride != *priorOverride {
		t.Fatalf("merge changed prior review identities: %+v, %+v", carriedTranslation, carriedOverride)
	}
	expectedSourceDigest, err := i18n.ExpectedSourceDigestForLocale(merged.Profile, merged.SourceLocale, merged.Modules[0].Name, merged.Modules[0].Messages[0])
	if err != nil {
		t.Fatal(err)
	}
	if carriedTranslation.SourceDigest == expectedSourceDigest || carriedOverride.SourceDigest == expectedSourceDigest {
		t.Fatal("merge silently approved prior work against changed source identity")
	}

	refusedReviewPath := filepath.Join(directory, "review-with-usage.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{
		"review", "-source", mergedPath, "-out", refusedReviewPath, "-locale", "ru", "-state", "approved", "-usage", usagePath,
	}, strings.NewReader(""), io.Discard, stderr); code != 2 {
		t.Fatalf("review accepted check-only usage input: code=%d stderr=%s", code, stderr)
	}
	if _, err := os.Stat(refusedReviewPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused review published output: %v", err)
	}
	stderr.Reset()
	if code := runCLI(context.Background(), []string{
		"review", "-source", mergedPath, "-out", reviewedPath, "-locale", "ru", "-state", "approved",
	}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("review branch code = %d: %s", code, stderr)
	}
	reviewed := readSourceFixture(t, reviewedPath)
	reviewedTranslation := reviewed.Modules[0].Messages[0].Translations[0]
	reviewedOverride := reviewed.Overrides[0]
	wantTranslationReview, err := i18n.ExpectedReviewDigest(expectedSourceDigest, reviewedTranslation.Locale, reviewedTranslation.Text)
	if err != nil {
		t.Fatal(err)
	}
	wantOverrideReview, err := i18n.ExpectedReviewDigest(expectedSourceDigest, reviewedOverride.Locale, reviewedOverride.Text)
	if err != nil {
		t.Fatal(err)
	}
	if reviewedTranslation.Review != i18n.ReviewApproved || reviewedTranslation.ContractRevision != "welcome-v2" ||
		reviewedTranslation.SourceDigest != expectedSourceDigest || reviewedTranslation.ReviewDigest != wantTranslationReview ||
		reviewedOverride.Review != i18n.ReviewApproved || reviewedOverride.ContractRevision != "welcome-v2" ||
		reviewedOverride.SourceDigest != expectedSourceDigest || reviewedOverride.ReviewDigest != wantOverrideReview {
		t.Fatalf("review did not update merged identities: %+v, %+v", reviewedTranslation, reviewedOverride)
	}

	reportPath := filepath.Join(directory, "report.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{
		"check", "-source", reviewedPath, "-usage", usagePath, "-strict", "-out", reportPath,
	}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("composed check code = %d: %s", code, stderr)
	}
	artifactPath := filepath.Join(directory, "catalog.json")
	stderr.Reset()
	if code := runCLI(context.Background(), []string{"compile", "-source", reviewedPath, "-out", artifactPath}, strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("compile code = %d: %s", code, stderr)
	}
	artifact, err := os.Open(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, loadErr := i18n.Load(context.Background(), artifact)
	closeErr := artifact.Close()
	if loadErr != nil || closeErr != nil || snapshot.Revision() != "catalog-v2" {
		t.Fatalf("loaded merged artifact = (%v, %v), revision=%q", loadErr, closeErr, snapshot.Revision())
	}
}

func TestCLIMergeIsSafeWhenOutputReplacesEitherInput(t *testing.T) {
	for _, target := range []string{"source", "previous"} {
		t.Run(target, func(t *testing.T) {
			directory := t.TempDir()
			sourcePath := filepath.Join(directory, "source.json")
			previousPath := filepath.Join(directory, "previous.json")
			newSource, previous := commandMergeFixtures(t)
			writeSourceFixture(t, sourcePath, newSource)
			writeSourceFixture(t, previousPath, previous)
			outPath := sourcePath
			if target == "previous" {
				outPath = previousPath
			}
			if err := os.Chmod(outPath, 0o600); err != nil {
				t.Fatal(err)
			}
			stderr := new(bytes.Buffer)
			args := []string{"merge", "-source", sourcePath, "-previous", previousPath, "-out", outPath}
			if code := runCLI(context.Background(), args, strings.NewReader(""), io.Discard, stderr); code != 0 {
				t.Fatalf("in-place merge code = %d: %s", code, stderr)
			}
			merged := readSourceFixture(t, outPath)
			if merged.Revision != "catalog-v2" || len(merged.Modules[0].Messages[0].Translations) != 1 || len(merged.Overrides) != 1 {
				t.Fatalf("in-place merge = %+v", merged)
			}
			info, err := os.Stat(outPath)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("in-place mode = %v, %v", info, err)
			}
		})
	}
}

func TestCLIMergePruningStdinAndUsageFailures(t *testing.T) {
	newSource, previous := commandMergeFixtures(t)
	previous.Modules[0].Messages = append(previous.Modules[0].Messages, i18n.MessageSpec{
		ID: "obsolete", Revision: "r1", Source: "Obsolete", Description: "Obsolete message",
		Output: i18n.OutputPlain, Translations: []i18n.Translation{{Locale: "ru", Text: "Устарело"}},
	})
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.json")
	previousPath := filepath.Join(directory, "previous.json")
	outPath := filepath.Join(directory, "merged.json")
	writeSourceFixture(t, sourcePath, newSource)
	writeSourceFixture(t, previousPath, previous)
	stderr := new(bytes.Buffer)
	args := []string{"merge", "-source", sourcePath, "-previous", previousPath, "-out", outPath}
	if code := runCLI(context.Background(), args, strings.NewReader(""), io.Discard, stderr); code != 1 || !strings.Contains(stderr.String(), i18n.ErrMergeWouldDiscard.Error()) {
		t.Fatalf("obsolete merge code = %d: %s", code, stderr)
	}
	if _, err := os.Stat(outPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused merge published output: %v", err)
	}
	if code := runCLI(context.Background(), append(args, "-prune-obsolete"), strings.NewReader(""), io.Discard, stderr); code != 0 {
		t.Fatalf("pruned merge code = %d: %s", code, stderr)
	}
	if merged := readSourceFixture(t, outPath); len(merged.Modules[0].Messages) != 1 {
		t.Fatalf("obsolete message survived: %+v", merged.Modules)
	}

	raw, err := i18n.EncodeSource(previous)
	if err != nil {
		t.Fatal(err)
	}
	stdout := new(bytes.Buffer)
	stdinArgs := []string{"merge", "-source", sourcePath, "-previous", "-", "-out", "-", "-prune-obsolete"}
	if code := runCLI(context.Background(), stdinArgs, bytes.NewReader(raw), stdout, stderr); code != 0 {
		t.Fatalf("stdin merge code = %d: %s", code, stderr)
	}
	if _, err := i18n.DecodeSource(context.Background(), bytes.NewReader(stdout.Bytes())); err != nil {
		t.Fatalf("stdout merge is not a source: %v", err)
	}

	for _, invalid := range [][]string{
		{"merge"},
		{"merge", "-source", "-", "-previous", "-", "-out", "-"},
		{"merge", "-source", sourcePath, "-previous", previousPath, "-out", "-", "-check", "-prune-obsolete"},
	} {
		if code := runCLI(context.Background(), invalid, panicReader{}, io.Discard, io.Discard); code != 2 {
			t.Fatalf("usage args %v code = %d", invalid, code)
		}
	}
}

func TestCLIMergeCancellationAndInputFailureDoNotChangeOutput(t *testing.T) {
	newSource, previous := commandMergeFixtures(t)
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.json")
	previousPath := filepath.Join(directory, "previous.json")
	writeSourceFixture(t, sourcePath, newSource)
	writeSourceFixture(t, previousPath, previous)
	stable, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := []string{"merge", "-source", sourcePath, "-previous", previousPath, "-out", sourcePath}
	if code := runCLI(ctx, args, strings.NewReader(""), io.Discard, io.Discard); code != 1 {
		t.Fatalf("canceled merge code = %d", code)
	}
	if after, err := os.ReadFile(sourcePath); err != nil || !bytes.Equal(after, stable) {
		t.Fatalf("canceled merge changed source: %v", err)
	}
	if err := os.WriteFile(previousPath, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runCLI(context.Background(), args, strings.NewReader(""), io.Discard, io.Discard); code != 1 {
		t.Fatalf("invalid previous merge code = %d", code)
	}
	if after, err := os.ReadFile(sourcePath); err != nil || !bytes.Equal(after, stable) {
		t.Fatalf("invalid previous changed source: %v", err)
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("stdin must not be read") }

func commandMergeFixtures(t testing.TB) (i18n.CatalogSpec, i18n.CatalogSpec) {
	t.Helper()
	previous := commandSourceFixture(t)
	newSource := commandSourceFixture(t)
	newSource.Revision = "catalog-v2"
	newSource.Modules[0].Messages[0].Revision = "welcome-v2"
	newSource.Modules[0].Messages[0].Source = "Welcome {$name}"
	newSource.Modules[0].Messages[0].Translations = nil
	newSource.Overrides = nil
	return newSource, previous
}
