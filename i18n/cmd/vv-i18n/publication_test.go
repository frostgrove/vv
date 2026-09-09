package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/i18n"
)

func TestPublicPublicationPublishesIdempotentlyAndRetainsPinnedGenerations(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	first := publicationTestExport("first")
	if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	firstPointer, firstFiles, err := readPublicPublication(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicationFiles(t, firstFiles, first)
	called := false
	if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{
		afterGeneration: func() error { called = true; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("idempotent publication reached the commit path")
	}

	second := publicationTestExport("second")
	if err := writePublicPublication(context.Background(), rootPath, second, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	secondPointer, secondFiles, err := readPublicPublication(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if secondPointer.Generation == firstPointer.Generation {
		t.Fatal("distinct exports used one generation")
	}
	assertPublicationFiles(t, secondFiles, second)
	root, err := openPublicationRoot(rootPath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	pinned, err := verifyPublicationGeneration(context.Background(), root, firstPointer)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicationFiles(t, pinned, first)
}

func TestPublicPublicationHonorsTheConfiguredOutputCeiling(t *testing.T) {
	exported := publicationTestExport("bounded")
	pointer, _, err := publicPublication(exported)
	if err != nil {
		t.Fatal(err)
	}
	pointerRaw, err := encodePublicationPointer(pointer)
	if err != nil {
		t.Fatal(err)
	}
	maximum := max(len(exported.Manifest), len(exported.TypeScript), len(pointerRaw))
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublicationBounded(context.Background(), rootPath, exported, false, publicationHooks{}, maximum-1); err == nil {
		t.Fatal("oversized publication was accepted")
	}
	if _, err := os.Stat(rootPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected publication created its root: %v", err)
	}
	if err := writePublicPublicationBounded(context.Background(), rootPath, exported, false, publicationHooks{}, maximum); err != nil {
		t.Fatal(err)
	}
	pointer, files, _, err := readPublicPublicationRawBounded(context.Background(), rootPath, maximum)
	if err != nil || pointer.Address != exported.Address || !matchesPublication(files, exported) {
		t.Fatalf("bounded publication = %+v, %v", pointer, err)
	}
	if _, _, _, err := readPublicPublicationRawBounded(context.Background(), rootPath, maximum-1); err == nil {
		t.Fatal("publication reader ignored its local ceiling")
	}
}

func TestPublicPublicationRefusesContentThatDoesNotMatchItsAddress(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	exported := publicationTestExport("valid")
	exported.TypeScript = append(slices.Clone(exported.TypeScript), []byte("export type Forged = never;\n")...)
	if err := writePublicPublication(context.Background(), rootPath, exported, false, publicationHooks{}); err == nil || !strings.Contains(err.Error(), "does not match its address") {
		t.Fatalf("mismatched public export error = %v", err)
	}
	if _, err := os.Stat(rootPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mismatched public export changed the filesystem: %v", err)
	}
}

func TestPublicPublicationReaderRefusesSelfConsistentForgedPointer(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	exported := publicationTestExport("forged")
	writeSelfConsistentForgedPublication(t, rootPath, exported)
	if _, _, err := readPublicPublication(context.Background(), rootPath); err == nil || !strings.Contains(err.Error(), "public export address") {
		t.Fatalf("self-consistent forged pointer error = %v", err)
	}
}

func TestStandalonePublicationReaderConsumesPublishedGeneration(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublication(context.Background(), rootPath, publicationTestExport("standalone"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "i18n-consumer-fixture", "publication_reader.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	readerDirectory := t.TempDir()
	readerPath := filepath.Join(readerDirectory, "publication_reader.go")
	if err := os.WriteFile(readerPath, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.test/publication-reader\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/frostgrove/vv v0.0.0\n\tgithub.com/frostgrove/vv/i18n v0.0.0\n)\n\nreplace github.com/frostgrove/vv => %s\nreplace github.com/frostgrove/vv/i18n => %s\n", filepath.Dir(moduleRoot), moduleRoot)
	if err := os.WriteFile(filepath.Join(readerDirectory, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	readerBinary := filepath.Join(readerDirectory, "publication-reader")
	command := exec.Command("go", "build", "-mod=mod", "-o", readerBinary, ".")
	command.Dir = readerDirectory
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build standalone publication reader: %v\n%s", err, output)
	}
	run := func(path string, accepted bool) {
		t.Helper()
		output, err := exec.Command(readerBinary, path).CombinedOutput()
		if accepted && err != nil {
			t.Fatalf("standalone publication reader: %v\n%s", err, output)
		}
		if !accepted && err == nil {
			t.Fatalf("standalone reader accepted invalid publication:\n%s", output)
		}
	}
	run(rootPath, true)
	forgedRoot := filepath.Join(t.TempDir(), "public.i18n")
	writeSelfConsistentForgedPublication(t, forgedRoot, publicationTestExport("standalone-forged"))
	run(forgedRoot, false)
	if runtime.GOOS == "windows" {
		return
	}

	linkedRootTarget := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublication(context.Background(), linkedRootTarget, publicationTestExport("linked-root"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(t.TempDir(), "linked-publication")
	if err := os.Symlink(linkedRootTarget, linkedRoot); err != nil {
		t.Fatal(err)
	}
	run(linkedRoot, false)

	linkedParentBase := t.TempDir()
	realParent := filepath.Join(linkedParentBase, "real-parent")
	linkedParent := filepath.Join(linkedParentBase, "linked-parent")
	parentRoot := filepath.Join(realParent, "public.i18n")
	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writePublicPublication(context.Background(), parentRoot, publicationTestExport("linked-parent"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, linkedParent); err != nil {
		t.Fatal(err)
	}
	run(filepath.Join(linkedParent, "public.i18n"), false)

	linkedGenerationsRoot := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublication(context.Background(), linkedGenerationsRoot, publicationTestExport("linked-generations"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	realGenerations := filepath.Join(linkedGenerationsRoot, "retained-generations")
	if err := os.Rename(filepath.Join(linkedGenerationsRoot, publicationGenerationsName), realGenerations); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(realGenerations), filepath.Join(linkedGenerationsRoot, publicationGenerationsName)); err != nil {
		t.Fatal(err)
	}
	run(linkedGenerationsRoot, false)

	linkedFileRoot := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublication(context.Background(), linkedFileRoot, publicationTestExport("linked-file"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	linkedPointer, _, err := readPublicPublication(context.Background(), linkedFileRoot)
	if err != nil {
		t.Fatal(err)
	}
	linkedGeneration := filepath.Join(linkedFileRoot, filepath.FromSlash(publicationGenerationDirectory(linkedPointer.Generation)))
	if err := os.Remove(filepath.Join(linkedGeneration, publicationManifestName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(publicationTypeScriptName, filepath.Join(linkedGeneration, publicationManifestName)); err != nil {
		t.Fatal(err)
	}
	run(linkedFileRoot, false)

	extraEntryRoot := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublication(context.Background(), extraEntryRoot, publicationTestExport("extra-entry"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	extraPointer, _, err := readPublicPublication(context.Background(), extraEntryRoot)
	if err != nil {
		t.Fatal(err)
	}
	extraGeneration := filepath.Join(extraEntryRoot, filepath.FromSlash(publicationGenerationDirectory(extraPointer.Generation)))
	if err := os.WriteFile(filepath.Join(extraGeneration, "unexpected"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	run(extraEntryRoot, false)

	oversizedFileRoot := filepath.Join(t.TempDir(), "public.i18n")
	if err := writePublicPublication(context.Background(), oversizedFileRoot, publicationTestExport("oversized-file"), false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	oversizedPointer, _, err := readPublicPublication(context.Background(), oversizedFileRoot)
	if err != nil {
		t.Fatal(err)
	}
	oversizedGeneration := filepath.Join(oversizedFileRoot, filepath.FromSlash(publicationGenerationDirectory(oversizedPointer.Generation)))
	if err := os.Truncate(filepath.Join(oversizedGeneration, publicationManifestName), maximumCommandInput+1); err != nil {
		t.Fatal(err)
	}
	run(oversizedFileRoot, false)
}

type cancelingPublicationReader struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancelingPublicationReader) Read(buffer []byte) (int, error) {
	r.reads++
	if r.reads != 1 {
		return 0, errors.New("read continued after cancellation")
	}
	buffer[0] = 'x'
	r.cancel()
	return 1, nil
}

func TestPublicationBoundedReadStopsBetweenChunksOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingPublicationReader{cancel: cancel}
	if _, err := readContextBounded(ctx, reader, 1<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded read cancellation = %v", err)
	}
	if reader.reads != 1 {
		t.Fatalf("bounded read calls after cancellation = %d", reader.reads)
	}
}

func writeSelfConsistentForgedPublication(t testing.TB, rootPath string, exported i18n.PublicExport) {
	t.Helper()
	forgedAddress := sha256Address([]byte("unrelated public export"))
	contents := []publicationContent{
		{role: publicationManifestRole, name: publicationManifestName, content: exported.Manifest},
		{role: publicationTypeScriptRole, name: publicationTypeScriptName, content: exported.TypeScript},
	}
	pointer := publicationPointer{
		Schema:     publicationSchema,
		Address:    forgedAddress,
		Generation: publicationGeneration(forgedAddress, contents),
		Files: []publicationFile{
			{Role: publicationManifestRole, Name: publicationManifestName, Bytes: int64(len(exported.Manifest)), SHA256: sha256Address(exported.Manifest)},
			{Role: publicationTypeScriptRole, Name: publicationTypeScriptName, Bytes: int64(len(exported.TypeScript)), SHA256: sha256Address(exported.TypeScript)},
		},
	}
	pointerBytes, err := encodePublicationPointer(pointer)
	if err != nil {
		t.Fatal(err)
	}
	generationPath := filepath.Join(rootPath, filepath.FromSlash(publicationGenerationDirectory(pointer.Generation)))
	if err := os.MkdirAll(generationPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generationPath, publicationManifestName), exported.Manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generationPath, publicationTypeScriptName), exported.TypeScript, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, publicationPointerName), pointerBytes, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublicPublicationPreCommitFailuresKeepThePreviousPointer(t *testing.T) {
	for _, phase := range []string{"before generation", "after generation", "before pointer"} {
		t.Run(phase, func(t *testing.T) {
			rootPath := filepath.Join(t.TempDir(), "public.i18n")
			first := publicationTestExport("first")
			if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{}); err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("publication stopped")
			hooks := publicationHooks{}
			switch phase {
			case "before generation":
				hooks.beforeGenerationCommit = func() error { return sentinel }
			case "after generation":
				hooks.afterGeneration = func() error { return sentinel }
			case "before pointer":
				hooks.beforeCommit = func() error { return sentinel }
			}
			if err := writePublicPublication(context.Background(), rootPath, publicationTestExport("second"), false, hooks); !errors.Is(err, sentinel) {
				t.Fatalf("phase error = %v", err)
			}
			_, files, err := readPublicPublication(context.Background(), rootPath)
			if err != nil {
				t.Fatal(err)
			}
			assertPublicationFiles(t, files, first)
		})
	}

	t.Run("cancellation", func(t *testing.T) {
		rootPath := filepath.Join(t.TempDir(), "public.i18n")
		first := publicationTestExport("first")
		if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		err := writePublicPublication(ctx, rootPath, publicationTestExport("second"), false, publicationHooks{
			afterGeneration: func() error { cancel(); return nil },
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled publication = %v", err)
		}
		_, files, err := readPublicPublication(context.Background(), rootPath)
		if err != nil {
			t.Fatal(err)
		}
		assertPublicationFiles(t, files, first)
	})

	t.Run("generation commit hook cancellation", func(t *testing.T) {
		rootPath := filepath.Join(t.TempDir(), "public.i18n")
		first := publicationTestExport("first")
		if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{}); err != nil {
			t.Fatal(err)
		}
		before := publicationTree(t, rootPath)
		ctx, cancel := context.WithCancel(context.Background())
		err := writePublicPublication(ctx, rootPath, publicationTestExport("second"), false, publicationHooks{
			beforeGenerationCommit: func() error { cancel(); return nil },
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("generation hook cancellation = %v", err)
		}
		after := publicationTree(t, rootPath)
		if !slices.Equal(before, after) {
			t.Fatalf("generation hook cancellation changed publication: %v -> %v", before, after)
		}
	})

	t.Run("post commit error", func(t *testing.T) {
		rootPath := filepath.Join(t.TempDir(), "public.i18n")
		if err := writePublicPublication(context.Background(), rootPath, publicationTestExport("first"), false, publicationHooks{}); err != nil {
			t.Fatal(err)
		}
		second := publicationTestExport("second")
		sentinel := errors.New("directory sync failed")
		err := writePublicPublication(context.Background(), rootPath, second, false, publicationHooks{
			afterCommit: func() error { return sentinel },
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("post-commit error = %v", err)
		}
		_, files, readErr := readPublicPublication(context.Background(), rootPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertPublicationFiles(t, files, second)
	})

	t.Run("post commit cancellation", func(t *testing.T) {
		rootPath := filepath.Join(t.TempDir(), "public.i18n")
		if err := writePublicPublication(context.Background(), rootPath, publicationTestExport("first"), false, publicationHooks{}); err != nil {
			t.Fatal(err)
		}
		second := publicationTestExport("second")
		ctx, cancel := context.WithCancel(context.Background())
		err := writePublicPublication(ctx, rootPath, second, false, publicationHooks{
			afterCommit: func() error { cancel(); return nil },
		})
		if err != nil {
			t.Fatalf("post-commit cancellation changed the committed outcome: %v", err)
		}
		_, files, readErr := readPublicPublication(context.Background(), rootPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertPublicationFiles(t, files, second)
	})
}

func TestPublicPublicationCheckIsExactReadOnlyAndBounded(t *testing.T) {
	if err := writePublicPublication(nil, "unused", publicationTestExport("nil"), false, publicationHooks{}); err == nil {
		t.Fatal("nil publication context accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	canceledRoot := filepath.Join(t.TempDir(), "canceled")
	if err := writePublicPublication(canceled, canceledRoot, publicationTestExport("canceled"), false, publicationHooks{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication = %v", err)
	}
	if _, err := os.Stat(canceledRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled publication created its root: %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := writePublicPublication(context.Background(), missing, publicationTestExport("missing"), true, publicationHooks{}); err == nil {
		t.Fatal("missing publication passed check mode")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check mode created its root: %v", err)
	}
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	first := publicationTestExport("first")
	if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	before := publicationTree(t, rootPath)
	if err := writePublicPublication(context.Background(), rootPath, first, true, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	if err := writePublicPublication(context.Background(), rootPath, publicationTestExport("second"), true, publicationHooks{}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale check = %v", err)
	}
	after := publicationTree(t, rootPath)
	if !slices.Equal(before, after) {
		t.Fatalf("check mutated publication: %v -> %v", before, after)
	}

	pointerPath := filepath.Join(rootPath, publicationPointerName)
	if err := os.WriteFile(pointerPath, bytes.Repeat([]byte("x"), maximumPublicationPointerBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPublicPublication(context.Background(), rootPath); err == nil {
		t.Fatal("oversized pointer accepted")
	}
}

func FuzzPublicationPointerNeverPanics(f *testing.F) {
	pointer, _, err := publicPublication(publicationTestExport("fuzz"))
	if err != nil {
		f.Fatal(err)
	}
	raw, err := encodePublicationPointer(pointer)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw)
	f.Add([]byte(`{"schema":"frostgrove.i18n.publication/v1"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = decodePublicationPointer(input)
	})
}

func TestPublicationPointerCodecIsStrictAndCanonical(t *testing.T) {
	pointer, _, err := publicPublication(publicationTestExport("strict"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodePublicationPointer(pointer)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePublicationPointer(raw)
	if err != nil || !equalPublicationPointers(decoded, pointer) {
		t.Fatalf("round trip = %+v, %v", decoded, err)
	}
	valid := strings.TrimSpace(string(raw))
	cases := map[string]string{
		"noncanonical compact": strings.NewReplacer("\n", "", "  ", "").Replace(string(raw)),
		"noncanonical space":   valid + "\n ",
		"unknown":              strings.Replace(valid, `"schema":`, `"unknown":true,"schema":`, 1),
		"duplicate":            strings.Replace(valid, `"schema":`, `"schema":"`+publicationSchema+`","\u0073chema":`, 1),
		"trailing":             valid + `{}`,
		"unsupported schema":   strings.Replace(valid, publicationSchema, "frostgrove.i18n.publication/v2", 1),
		"uppercase digest":     strings.Replace(valid, pointer.Generation, strings.ToUpper(pointer.Generation), 1),
		"short digest":         strings.Replace(valid, pointer.Generation, "sha256:00", 1),
		"null table":           strings.Replace(valid, `"files": [`, `"files": null,"ignored": [`, 1),
		"wrong order":          strings.Replace(valid, publicationManifestRole, publicationTypeScriptRole, 1),
		"traversal":            strings.Replace(valid, publicationManifestName, "../manifest.json", 1),
		"negative bytes":       strings.Replace(valid, `"bytes": `, `"bytes": -`, 1),
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodePublicationPointer([]byte(candidate)); err == nil {
				t.Fatal("malformed publication pointer accepted")
			}
		})
	}
	manyFiles := `{"files":[` + strings.Repeat(`{},`, maximumPublicationFiles) + `{}` + `]}`
	if _, err := decodePublicationPointer([]byte(manyFiles)); err == nil || !strings.Contains(err.Error(), "file limit") {
		t.Fatalf("oversized file table = %v", err)
	}
}

func TestPublicPublicationRejectsCorruptAndLinkedState(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	exported := publicationTestExport("secure")
	if err := writePublicPublication(context.Background(), rootPath, exported, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	pointer, _, err := readPublicPublication(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(rootPath, filepath.FromSlash(publicationGenerationDirectory(pointer.Generation)))
	if err := os.WriteFile(filepath.Join(generation, publicationManifestName), []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPublicPublication(context.Background(), rootPath); err == nil {
		t.Fatal("corrupt generation accepted")
	}
	if err := writePublicPublication(context.Background(), rootPath, exported, false, publicationHooks{}); err == nil {
		t.Fatal("corrupt content-addressed generation replaced in place")
	}

	if runtime.GOOS == "windows" {
		return
	}
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(parent, "linked")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := writePublicPublication(context.Background(), linkedRoot, exported, false, publicationHooks{}); err == nil {
		t.Fatal("symlink publication root accepted")
	}
	rootWithLinkedPointer := filepath.Join(t.TempDir(), "publication")
	if err := os.Mkdir(rootWithLinkedPointer, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(realRoot, "pointer.json"), filepath.Join(rootWithLinkedPointer, publicationPointerName)); err != nil {
		t.Fatal(err)
	}
	if err := writePublicPublication(context.Background(), rootWithLinkedPointer, exported, false, publicationHooks{}); err == nil {
		t.Fatal("symlink publication pointer accepted")
	}
	if _, err := os.Stat(filepath.Join(rootWithLinkedPointer, publicationGenerationsName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("linked pointer refusal staged a generation: %v", err)
	}
	rootWithLinkedGenerations := filepath.Join(t.TempDir(), "publication")
	if err := os.Mkdir(rootWithLinkedGenerations, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, filepath.Join(rootWithLinkedGenerations, publicationGenerationsName)); err != nil {
		t.Fatal(err)
	}
	if err := writePublicPublication(context.Background(), rootWithLinkedGenerations, exported, false, publicationHooks{}); err == nil {
		t.Fatal("symlink generations directory accepted")
	}
	rootWithRetargetedGenerations := filepath.Join(t.TempDir(), "publication")
	if err := writePublicPublication(context.Background(), rootWithRetargetedGenerations, exported, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	retargetedGenerations := filepath.Join(rootWithRetargetedGenerations, "retargeted-generations")
	if err := os.Rename(filepath.Join(rootWithRetargetedGenerations, publicationGenerationsName), retargetedGenerations); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(retargetedGenerations), filepath.Join(rootWithRetargetedGenerations, publicationGenerationsName)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPublicPublication(context.Background(), rootWithRetargetedGenerations); err == nil {
		t.Fatal("reader accepted a linked generations component")
	}
	linkedFileRoot := filepath.Join(t.TempDir(), "publication")
	if err := writePublicPublication(context.Background(), linkedFileRoot, exported, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	linkedPointer, _, err := readPublicPublication(context.Background(), linkedFileRoot)
	if err != nil {
		t.Fatal(err)
	}
	linkedGeneration := filepath.Join(linkedFileRoot, filepath.FromSlash(publicationGenerationDirectory(linkedPointer.Generation)))
	manifestPath := filepath.Join(linkedGeneration, publicationManifestName)
	if err := os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(publicationTypeScriptName, manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPublicPublication(context.Background(), linkedFileRoot); err == nil {
		t.Fatal("symlink generation file accepted")
	}
}

func TestPublicPublicationAncestorSwapCannotRedirectRootCreation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	alternate := filepath.Join(base, "alternate")
	retained := filepath.Join(base, "retained")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(alternate, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(alternate, "marker")
	if err := os.WriteFile(marker, []byte("alternate"), 0o644); err != nil {
		t.Fatal(err)
	}
	exported := publicationTestExport("ancestor-swap")
	err := writePublicPublication(context.Background(), filepath.Join(selected, "public.i18n"), exported, false, publicationHooks{
		beforeRootCreate: func() error {
			if err := os.Rename(selected, retained); err != nil {
				return err
			}
			return os.Rename(alternate, selected)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(selected, "public.i18n")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alternate target publication root: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(selected, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "alternate" {
		t.Fatalf("alternate marker = %q", content)
	}
	_, files, err := readPublicPublication(context.Background(), filepath.Join(retained, "public.i18n"))
	if err != nil {
		t.Fatal(err)
	}
	assertPublicationFiles(t, files, exported)
}

func TestPublicPublicationRootCreationStopsWhenItsHookCancels(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "parent", "public.i18n")
	if err := os.Mkdir(filepath.Dir(rootPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := writePublicPublication(ctx, rootPath, publicationTestExport("root-cancel"), false, publicationHooks{
		beforeRootCreate: func() error { cancel(); return nil },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("root hook cancellation = %v", err)
	}
	if _, err := os.Lstat(rootPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root hook cancellation created a root: %v", err)
	}
}

func TestPublicPublicationBoundsUnexpectedGenerationEntries(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	exported := publicationTestExport("bounded-directory")
	if err := writePublicPublication(context.Background(), rootPath, exported, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	pointer, _, err := readPublicPublication(context.Background(), rootPath)
	if err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(rootPath, filepath.FromSlash(publicationGenerationDirectory(pointer.Generation)))
	for index := range maximumPublicationFiles + 2 {
		if err := os.WriteFile(filepath.Join(generation, fmt.Sprintf("extra-%02d", index)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := readPublicPublication(context.Background(), rootPath); err == nil || !strings.Contains(err.Error(), "file limit") {
		t.Fatalf("unbounded generation directory = %v", err)
	}
}

func TestPublicPublicationConcurrentReadersObserveOneCompleteGeneration(t *testing.T) {
	rootPath := filepath.Join(t.TempDir(), "public.i18n")
	exports := []i18n.PublicExport{publicationTestExport("a"), publicationTestExport("b"), publicationTestExport("c")}
	if err := writePublicPublication(context.Background(), rootPath, exports[0], false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	wanted := make(map[string]i18n.PublicExport, len(exports))
	for _, exported := range exports {
		wanted[exported.Address] = exported
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errorsOut := make(chan error, 16)
	var writers sync.WaitGroup
	for writer := range 4 {
		writers.Add(1)
		go func(offset int) {
			defer writers.Done()
			for iteration := range 30 {
				exported := exports[(offset+iteration)%len(exports)]
				if err := writePublicPublication(ctx, rootPath, exported, false, publicationHooks{}); err != nil {
					errorsOut <- err
					return
				}
			}
		}(writer)
	}
	done := make(chan struct{})
	go func() {
		writers.Wait()
		close(done)
	}()
	for {
		select {
		case err := <-errorsOut:
			t.Fatal(err)
		case <-done:
			_, files, err := readPublicPublication(context.Background(), rootPath)
			if err != nil {
				t.Fatal(err)
			}
			if !matchesAnyPublication(files, exports) {
				t.Fatal("final reader observed a mixed generation")
			}
			return
		default:
			pointer, files, err := readPublicPublication(context.Background(), rootPath)
			if err != nil {
				t.Fatal(err)
			}
			exported, ok := wanted[pointer.Address]
			if !ok || !matchesPublication(files, exported) {
				t.Fatalf("reader observed a mixed generation: %+v", pointer)
			}
		}
	}
}

func TestPublicPublicationSurvivesWriterProcessKillAtTheCommitBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process kill timing uses Unix process semantics")
	}
	for _, phase := range []string{"generation", "before", "after"} {
		t.Run(phase, func(t *testing.T) {
			rootPath := filepath.Join(t.TempDir(), "public.i18n")
			first := publicationTestExport("first")
			if err := writePublicPublication(context.Background(), rootPath, first, false, publicationHooks{}); err != nil {
				t.Fatal(err)
			}
			ready := filepath.Join(t.TempDir(), "ready")
			command := exec.Command(os.Args[0], "-test.run=^TestPublicPublicationProcessHelper$")
			command.Env = append(os.Environ(),
				"VV_I18N_PUBLICATION_HELPER=1",
				"VV_I18N_PUBLICATION_ROOT="+rootPath,
				"VV_I18N_PUBLICATION_PHASE="+phase,
				"VV_I18N_PUBLICATION_READY="+ready,
			)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waitForPublicationHelper(t, command, ready)
			if err := command.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = command.Wait()
			_, files, err := readPublicPublication(context.Background(), rootPath)
			if err != nil {
				t.Fatal(err)
			}
			want := first
			if phase == "after" {
				want = publicationTestExport("second")
			}
			assertPublicationFiles(t, files, want)
		})
	}
}

func TestPublicPublicationProcessHelper(t *testing.T) {
	if os.Getenv("VV_I18N_PUBLICATION_HELPER") != "1" {
		return
	}
	ready := func() error {
		if err := os.WriteFile(os.Getenv("VV_I18N_PUBLICATION_READY"), []byte("ready"), 0o644); err != nil {
			return err
		}
		select {}
	}
	hooks := publicationHooks{}
	switch os.Getenv("VV_I18N_PUBLICATION_PHASE") {
	case "generation":
		hooks.beforeGenerationCommit = ready
	case "before":
		hooks.beforeCommit = ready
	case "after":
		hooks.afterCommit = ready
	default:
		os.Exit(2)
	}
	if err := writePublicPublication(context.Background(), os.Getenv("VV_I18N_PUBLICATION_ROOT"), publicationTestExport("second"), false, hooks); err != nil {
		os.Exit(1)
	}
}

func publicationTestExport(value string) i18n.PublicExport {
	typeScript := []byte("export interface " + value + " {}\n")
	manifest := []byte(`{"value":"` + value + `"}`)
	return i18n.PublicExport{
		TypeScript: typeScript,
		Manifest:   manifest,
		Address:    i18n.ExpectedPublicExportAddress(manifest, typeScript),
	}
}

func assertPublicationFiles(t testing.TB, files map[string][]byte, exported i18n.PublicExport) {
	t.Helper()
	if !matchesPublication(files, exported) {
		t.Fatalf("publication files = %#v", files)
	}
}

func matchesPublication(files map[string][]byte, exported i18n.PublicExport) bool {
	return len(files) == 2 && bytes.Equal(files[publicationManifestRole], exported.Manifest) && bytes.Equal(files[publicationTypeScriptRole], exported.TypeScript)
}

func matchesAnyPublication(files map[string][]byte, exports []i18n.PublicExport) bool {
	for _, exported := range exports {
		if matchesPublication(files, exported) {
			return true
		}
	}
	return false
}

func publicationTree(t testing.TB, root string) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		identity := relative
		if !entry.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			identity += "=" + sha256Address(raw)
		}
		paths = append(paths, identity)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}

func waitForPublicationHelper(t testing.TB, command *exec.Cmd, ready string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		if command.ProcessState != nil && command.ProcessState.Exited() {
			t.Fatalf("publication helper exited before its hook: %v", command.ProcessState)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_ = command.Wait()
	t.Fatal(fmt.Errorf("publication helper did not reach its hook"))
}
