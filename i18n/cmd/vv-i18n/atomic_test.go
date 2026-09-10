package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublishAtomicWritesChecksAndPreservesMode(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishAtomic(context.Background(), path, []byte("new"), publishHooks{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new" || info.Mode().Perm() != 0o600 {
		t.Fatalf("published = (%q, %o)", raw, info.Mode().Perm())
	}
	if err := writeOutput(context.Background(), path, []byte("new"), true); err != nil {
		t.Fatalf("current output rejected: %v", err)
	}
	if err := writeOutput(context.Background(), path, []byte("other"), true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale output error = %v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "new" {
		t.Fatalf("check mode mutated output: %q, %v", raw, err)
	}
}

func TestBoundedOutputPublicationUsesTheSelectedArtifactClass(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "bounded.json")
	if err := writeOutputBounded(context.Background(), path, []byte("four"), false, 3); err == nil {
		t.Fatal("oversized bounded output was published")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected output exists: %v", err)
	}
	if err := writeOutputBounded(context.Background(), path, []byte("four"), false, 4); err != nil {
		t.Fatal(err)
	}
	if err := writeOutputBounded(context.Background(), path, []byte("four"), true, 4); err != nil {
		t.Fatal(err)
	}
	if err := writeOutputBounded(context.Background(), path, []byte("four"), true, 3); err == nil {
		t.Fatal("existing output escaped the selected check ceiling")
	}
	if err := writeOutputBounded(context.Background(), path, nil, false, 0); err == nil {
		t.Fatal("invalid output ceiling was accepted")
	}
}

func TestPublishAtomicFailureAndCancellationLeavePriorFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(path, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("publication stopped")
	err := publishAtomic(context.Background(), path, []byte("candidate"), publishHooks{beforeRename: func() error { return sentinel }})
	if !errors.Is(err, sentinel) {
		t.Fatalf("hook error = %v", err)
	}
	assertFileContent(t, path, "stable")
	ctx, cancel := context.WithCancel(context.Background())
	err = publishAtomic(ctx, path, []byte("candidate"), publishHooks{beforeRename: func() error {
		cancel()
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("hook cancellation = %v", err)
	}
	assertFileContent(t, path, "stable")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != publisherLockName || entries[1].Name() != "catalog.json" {
		t.Fatalf("temporary output leaked: %v", entries)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := publishAtomic(ctx, path, []byte("canceled"), publishHooks{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication = %v", err)
	}
	assertFileContent(t, path, "stable")
}

func TestPublishAtomicUsesThePinnedDirectoryAfterAnAncestorReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit renaming an open directory")
	}
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	retained := filepath.Join(base, "retained")
	alternate := filepath.Join(base, "alternate")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(alternate, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(selected, "catalog.json")
	err := publishAtomic(context.Background(), target, []byte("candidate"), publishHooks{beforeRename: func() error {
		if err := os.Rename(selected, retained); err != nil {
			return err
		}
		return os.Symlink(filepath.Base(alternate), selected)
	}})
	if err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(retained, "catalog.json"), "candidate")
	if _, err := os.Lstat(filepath.Join(alternate, "catalog.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alternate target was mutated: %v", err)
	}
}

func TestPublishStagedSetRollsBackAReportedFailure(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "messages.d.ts")
	second := filepath.Join(directory, "messages.json")
	if err := os.WriteFile(first, []byte("old types"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("old manifest"), 0o640); err != nil {
		t.Fatal(err)
	}
	outputs := []outputFile{{path: first, content: []byte("new types")}, {path: second, content: []byte("new manifest")}}
	sentinel := errors.New("second rename refused")
	err := publishStagedSet(context.Background(), directory, outputs, stagedSetHooks{beforeRename: func(index int) error {
		if index == 1 {
			return sentinel
		}
		return nil
	}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("transaction error = %v", err)
	}
	assertFileContent(t, first, "old types")
	assertFileContent(t, second, "old manifest")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name() != publisherLockName {
		t.Fatalf("transaction artifacts leaked: %v", entries)
	}
	if err := writeOutputSet(context.Background(), outputs, false); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, first, "new types")
	assertFileContent(t, second, "new manifest")
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstInfo.Mode().Perm() != 0o600 || secondInfo.Mode().Perm() != 0o640 {
		t.Fatalf("modes = %o, %o", firstInfo.Mode().Perm(), secondInfo.Mode().Perm())
	}
}

func TestPublishStagedSetRollsBackCancellationFromARenameHook(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.json")
	second := filepath.Join(directory, "second.json")
	if err := os.WriteFile(first, []byte("old first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("old second"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	outputs := []outputFile{{path: first, content: []byte("new first")}, {path: second, content: []byte("new second")}}
	err := publishStagedSet(ctx, directory, outputs, stagedSetHooks{beforeRename: func(index int) error {
		if index == 1 {
			cancel()
		}
		return nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("staged hook cancellation = %v", err)
	}
	assertFileContent(t, first, "old first")
	assertFileContent(t, second, "old second")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name() != publisherLockName {
		t.Fatalf("canceled staged publication leaked files: %v", entries)
	}
}

func TestPublishStagedSetRollbackPreservesAConcurrentReplacementAndRecoveryBackup(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.json")
	second := filepath.Join(directory, "second.json")
	replacement := filepath.Join(directory, "concurrent.json")
	if err := os.WriteFile(first, []byte("old first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("old second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("concurrent first"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("second rename refused")
	outputs := []outputFile{{path: first, content: []byte("new first")}, {path: second, content: []byte("new second")}}
	err := publishStagedSet(context.Background(), directory, outputs, stagedSetHooks{beforeRename: func(index int) error {
		if index != 1 {
			return nil
		}
		if err := os.Remove(first); err != nil {
			return err
		}
		if err := os.Rename(replacement, first); err != nil {
			return err
		}
		return sentinel
	}})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "backup preserved") {
		t.Fatalf("rollback recovery error = %v", err)
	}
	assertFileContent(t, first, "concurrent first")
	assertFileContent(t, second, "old second")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	backup := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".first.json.backup.tmp-") {
			backup = filepath.Join(directory, entry.Name())
		}
	}
	if backup == "" {
		t.Fatalf("recovery backup missing: %v", entries)
	}
	assertFileContent(t, backup, "old first")
}

func TestPublishStagedSetRollbackDoesNotRemoveAConcurrentReplacementOfANewTarget(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first.json")
	second := filepath.Join(directory, "second.json")
	replacement := filepath.Join(directory, "concurrent.json")
	if err := os.WriteFile(second, []byte("old second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("concurrent first"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("second rename refused")
	outputs := []outputFile{{path: first, content: []byte("new first")}, {path: second, content: []byte("new second")}}
	err := publishStagedSet(context.Background(), directory, outputs, stagedSetHooks{beforeRename: func(index int) error {
		if index != 1 {
			return nil
		}
		if err := os.Remove(first); err != nil {
			return err
		}
		if err := os.Rename(replacement, first); err != nil {
			return err
		}
		return sentinel
	}})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "refuse rollback") || strings.Contains(err.Error(), "backup preserved") {
		t.Fatalf("rollback recovery error = %v", err)
	}
	assertFileContent(t, first, "concurrent first")
	assertFileContent(t, second, "old second")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name() != publisherLockName {
		t.Fatalf("new-target rollback leaked staging files: %v", entries)
	}
}

func TestOutputSetRejectsAliasesAndCrossDirectoryMutation(t *testing.T) {
	firstDirectory := t.TempDir()
	secondDirectory := t.TempDir()
	path := filepath.Join(firstDirectory, "out")
	if err := writeOutputSet(context.Background(), []outputFile{{path: path}, {path: path}}, false); err == nil {
		t.Fatal("repeated output accepted")
	}
	other := filepath.Join(secondDirectory, "out")
	if err := writeOutputSet(context.Background(), []outputFile{{path: path}, {path: other}}, false); err == nil {
		t.Fatal("cross-directory staged output accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first path changed: %v", err)
	}
	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("second path changed: %v", err)
	}
}

func TestAtomicIORejectsLinksDirectoriesAndInvalidTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior requires Windows privileges")
	}
	directory := t.TempDir()
	realDirectory := filepath.Join(directory, "real")
	if err := os.Mkdir(realDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(realDirectory, "real.json")
	if err := os.WriteFile(realFile, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkFile := filepath.Join(realDirectory, "link.json")
	if err := os.Symlink(realFile, linkFile); err != nil {
		t.Fatal(err)
	}
	if err := publishAtomic(context.Background(), linkFile, []byte("candidate"), publishHooks{}); err == nil {
		t.Fatal("symlink publication target accepted")
	}
	if _, err := readRegularFile(context.Background(), linkFile, 1024); err == nil {
		t.Fatal("symlink input accepted")
	}
	linkDirectory := filepath.Join(directory, "linked")
	if err := os.Symlink(realDirectory, linkDirectory); err != nil {
		t.Fatal(err)
	}
	if err := publishAtomic(context.Background(), filepath.Join(linkDirectory, "out.json"), []byte("candidate"), publishHooks{}); err == nil {
		t.Fatal("symlinked publication directory accepted")
	}
	if _, err := readRegularFile(context.Background(), filepath.Join(linkDirectory, "real.json"), 1024); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlinked input ancestor error = %v", err)
	}
	if err := publishAtomic(context.Background(), realDirectory, []byte("candidate"), publishHooks{}); err == nil {
		t.Fatal("directory publication target accepted")
	}
	if err := publishAtomic(context.Background(), "", nil, publishHooks{}); err == nil {
		t.Fatal("empty publication target accepted")
	}
	assertFileContent(t, realFile, "stable")
}

func TestReadRegularFileIsBoundedAndCancellationAware(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.json")
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegularFile(context.Background(), path, 4); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("limit error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readRegularFile(ctx, path, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read = %v", err)
	}
	if _, err := readRegularFile(nil, path, 100); err == nil {
		t.Fatal("nil read context accepted")
	}
}

func assertFileContent(t testing.TB, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("file %q = %q, want %q", path, raw, want)
	}
}
