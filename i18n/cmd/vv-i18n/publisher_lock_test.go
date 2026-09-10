package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type publisherLockHelperSpec struct {
	mode      string
	directory string
	target    string
	second    string
	content   string
	started   string
	ready     string
	release   string
}

type publisherLockHelperProcess struct {
	command *exec.Cmd
	done    chan error
	output  *bytes.Buffer
}

func TestPublisherLockSerializesTwoWriterProcesses(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(target, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	markers := t.TempDir()
	release := filepath.Join(markers, "release")
	first := startPublisherLockHelper(t, publisherLockHelperSpec{
		mode:      "atomic",
		directory: directory,
		target:    target,
		content:   "first",
		started:   filepath.Join(markers, "first-started"),
		ready:     filepath.Join(markers, "first-acquired"),
		release:   release,
	})
	waitForPublisherLockMarker(t, first, filepath.Join(markers, "first-acquired"))
	second := startPublisherLockHelper(t, publisherLockHelperSpec{
		mode:      "atomic",
		directory: directory,
		target:    target,
		content:   "second",
		started:   filepath.Join(markers, "second-started"),
		ready:     filepath.Join(markers, "second-acquired"),
	})
	waitForPublisherLockMarker(t, second, filepath.Join(markers, "second-started"))
	assertPublisherLockHelperWaits(t, second, filepath.Join(markers, "second-acquired"))
	if err := os.WriteFile(release, []byte("release"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForPublisherLockHelper(t, first)
	waitForPublisherLockHelper(t, second)
	if _, err := os.Stat(filepath.Join(markers, "second-acquired")); err != nil {
		t.Fatalf("second writer never acquired the lock: %v", err)
	}
	assertFileContent(t, target, "second")
}

func TestPublisherLockWaitHonorsContextWithoutMutation(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(target, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	markers := t.TempDir()
	release := filepath.Join(markers, "release")
	holder := startPublisherLockHelper(t, publisherLockHelperSpec{
		mode:      "atomic",
		directory: directory,
		target:    target,
		content:   "holder",
		ready:     filepath.Join(markers, "holder-acquired"),
		release:   release,
	})
	waitForPublisherLockMarker(t, holder, filepath.Join(markers, "holder-acquired"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := publishAtomic(ctx, target, []byte("waiter"), publishHooks{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting publisher error = %v", err)
	}
	assertFileContent(t, target, "stable")
	if err := os.WriteFile(release, []byte("release"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForPublisherLockHelper(t, holder)
	assertFileContent(t, target, "holder")
}

func TestPublisherLockIsReleasedWhenTheHoldingProcessCrashes(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "catalog.json")
	markers := t.TempDir()
	holder := startPublisherLockHelper(t, publisherLockHelperSpec{
		mode:      "hold",
		directory: directory,
		ready:     filepath.Join(markers, "holder-acquired"),
	})
	waitForPublisherLockMarker(t, holder, filepath.Join(markers, "holder-acquired"))
	before, err := os.Stat(filepath.Join(directory, publisherLockName))
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-holder.done; err == nil {
		t.Fatal("crashed lock holder exited successfully")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := publishAtomic(ctx, target, []byte("after crash"), publishHooks{}); err != nil {
		t.Fatalf("publisher did not acquire the crash-released lock: %v", err)
	}
	after, err := os.Stat(filepath.Join(directory, publisherLockName))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("persistent lock file identity changed after holder crash")
	}
	assertFileContent(t, target, "after crash")
}

func TestPublisherLockCoversRollbackBeforeTheNextWriterSnapshots(t *testing.T) {
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.json")
	secondPath := filepath.Join(directory, "second.json")
	if err := os.WriteFile(firstPath, []byte("old first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("old second"), 0o644); err != nil {
		t.Fatal(err)
	}
	markers := t.TempDir()
	release := filepath.Join(markers, "release")
	rollback := startPublisherLockHelper(t, publisherLockHelperSpec{
		mode:      "staged-rollback",
		directory: directory,
		target:    firstPath,
		second:    secondPath,
		ready:     filepath.Join(markers, "rollback-started"),
		release:   release,
	})
	waitForPublisherLockMarker(t, rollback, filepath.Join(markers, "rollback-started"))
	assertFileContent(t, firstPath, "staged first")
	next := startPublisherLockHelper(t, publisherLockHelperSpec{
		mode:      "atomic",
		directory: directory,
		target:    firstPath,
		content:   "next writer",
		started:   filepath.Join(markers, "next-started"),
		ready:     filepath.Join(markers, "next-acquired"),
	})
	waitForPublisherLockMarker(t, next, filepath.Join(markers, "next-started"))
	assertPublisherLockHelperWaits(t, next, filepath.Join(markers, "next-acquired"))
	if err := os.WriteFile(release, []byte("release"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForPublisherLockHelper(t, rollback)
	waitForPublisherLockHelper(t, next)
	assertFileContent(t, firstPath, "next writer")
	assertFileContent(t, secondPath, "old second")
}

func TestEveryPublisherPathUsesTheSamePersistentLock(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lock, err := acquirePublisherLock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.close()
	assertWaitCanceled := func(name string, publish func(context.Context) error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()
		if err := publish(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s publisher lock wait = %v", name, err)
		}
	}
	assertWaitCanceled("single", func(ctx context.Context) error {
		return publishAtomic(ctx, filepath.Join(directory, "single.json"), []byte("single"), publishHooks{})
	})
	assertWaitCanceled("staged", func(ctx context.Context) error {
		return publishStagedSet(ctx, directory, []outputFile{
			{path: filepath.Join(directory, "first.json"), content: []byte("first")},
			{path: filepath.Join(directory, "second.json"), content: []byte("second")},
		}, stagedSetHooks{})
	})
	assertWaitCanceled("generational", func(ctx context.Context) error {
		return writePublicPublication(ctx, directory, publicationTestExport("locked"), false, publicationHooks{})
	})
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != publisherLockName {
		t.Fatalf("waiting publishers mutated the directory: %v", entries)
	}
}

func TestCheckPathsRemainReadOnlyAndDoNotAcquireThePublisherLock(t *testing.T) {
	directDirectory := t.TempDir()
	direct := filepath.Join(directDirectory, "catalog.json")
	if err := os.WriteFile(direct, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOutput(context.Background(), direct, []byte("current"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directDirectory, publisherLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("single-file check created a lock: %v", err)
	}
	second := filepath.Join(directDirectory, "second.json")
	if err := os.WriteFile(second, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOutputSet(context.Background(), []outputFile{
		{path: direct, content: []byte("current")},
		{path: second, content: []byte("second")},
	}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directDirectory, publisherLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged-set check created a lock: %v", err)
	}
	publicationRoot := filepath.Join(t.TempDir(), "public.i18n")
	exported := publicationTestExport("read-only-check")
	if err := writePublicPublication(context.Background(), publicationRoot, exported, false, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(publicationRoot, publisherLockName)); err != nil {
		t.Fatal(err)
	}
	if err := writePublicPublication(context.Background(), publicationRoot, exported, true, publicationHooks{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(publicationRoot, publisherLockName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generational check created a lock: %v", err)
	}
}

func TestPublisherLockNameAndIdentityAreReserved(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{publisherLockName, ".VV-I18N.LOCK", publisherLockName + ".", publisherLockName + " ", publisherLockName + ":stream"} {
		if err := writeOutput(context.Background(), filepath.Join(directory, name), []byte("collision"), false); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("reserved lock output %q error = %v", name, err)
		}
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("reserved output mutated directory: %v, %v", entries, err)
	}
	reserved := filepath.Join(directory, publisherLockName)
	if err := os.Mkdir(reserved, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "catalog.json")
	if err := publishAtomic(context.Background(), target, []byte("candidate"), publishHooks{}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory lock path error = %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe lock path allowed target mutation: %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	if err := os.Remove(reserved); err != nil {
		t.Fatal(err)
	}
	realLock := filepath.Join(t.TempDir(), "real-lock")
	if err := os.WriteFile(realLock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realLock, reserved); err != nil {
		t.Fatal(err)
	}
	if err := publishAtomic(context.Background(), target, []byte("candidate"), publishHooks{}); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("linked lock path error = %v", err)
	}
	assertFileContent(t, realLock, "")
}

func TestPublisherRejectsFilesystemAliasOfHeldLock(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "catalog.json")
	if err := publishAtomic(context.Background(), target, []byte("stable"), publishHooks{}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "lock-alias.json")
	if err := os.Link(filepath.Join(directory, publisherLockName), alias); err != nil {
		t.Skipf("filesystem does not support lock identity aliases: %v", err)
	}
	if err := publishAtomic(context.Background(), alias, []byte("candidate"), publishHooks{}); err == nil || !strings.Contains(err.Error(), "aliases the reserved") {
		t.Fatalf("direct lock alias error = %v", err)
	}
	if err := publishStagedSet(context.Background(), directory, []outputFile{{path: alias, content: []byte("candidate")}}, stagedSetHooks{}); err == nil || !strings.Contains(err.Error(), "aliases the reserved") {
		t.Fatalf("staged lock alias error = %v", err)
	}
	assertFileContent(t, target, "stable")
	assertFileContent(t, filepath.Join(directory, publisherLockName), "")
}

func TestPublisherDetectsExternalLockIdentityReplacementAtCommitCheckpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit replacing an open locked file")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(target, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := publishAtomic(context.Background(), target, []byte("candidate"), publishHooks{beforeRename: func() error {
		if err := os.Remove(filepath.Join(directory, publisherLockName)); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(directory, publisherLockName), []byte("replacement"), 0o600)
	}})
	if !errors.Is(err, errPublisherLockChanged) {
		t.Fatalf("lock identity replacement error = %v", err)
	}
	assertFileContent(t, target, "stable")
	assertFileContent(t, filepath.Join(directory, publisherLockName), "replacement")
}

func TestPublisherLockPlatformImplementationsAreExplicit(t *testing.T) {
	assertSource := func(name string, fragments ...string) {
		t.Helper()
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(raw), fragment) {
				t.Fatalf("%s does not contain %q", name, fragment)
			}
		}
	}
	assertSource("publisher_lock_unix.go", "//go:build", "syscall.Flock", "syscall.LOCK_NB", "syscall.LOCK_UN")
	assertSource("publisher_lock_windows.go", "//go:build windows", "LockFileEx", "UnlockFileEx", "windowsLockFileFailImmediately")
	assertSource("publisher_lock_unsupported.go", "//go:build", "return false", "publisher locking is unsupported")
}

func TestPublisherLockProcessHelper(t *testing.T) {
	if os.Getenv("VV_I18N_PUBLISHER_LOCK_HELPER") != "1" {
		return
	}
	spec := publisherLockHelperSpec{
		mode:      os.Getenv("VV_I18N_PUBLISHER_LOCK_MODE"),
		directory: os.Getenv("VV_I18N_PUBLISHER_LOCK_DIRECTORY"),
		target:    os.Getenv("VV_I18N_PUBLISHER_LOCK_TARGET"),
		second:    os.Getenv("VV_I18N_PUBLISHER_LOCK_SECOND"),
		content:   os.Getenv("VV_I18N_PUBLISHER_LOCK_CONTENT"),
		started:   os.Getenv("VV_I18N_PUBLISHER_LOCK_STARTED"),
		ready:     os.Getenv("VV_I18N_PUBLISHER_LOCK_READY"),
		release:   os.Getenv("VV_I18N_PUBLISHER_LOCK_RELEASE"),
	}
	if spec.started != "" {
		writePublisherLockMarker(t, spec.started)
	}
	switch spec.mode {
	case "atomic":
		hooks := publishHooks{}
		if spec.ready != "" {
			hooks.beforeRename = func() error {
				writePublisherLockMarker(t, spec.ready)
				return waitForPublisherLockRelease(spec.release)
			}
		}
		if err := publishAtomic(context.Background(), spec.target, []byte(spec.content), hooks); err != nil {
			t.Fatal(err)
		}
	case "staged-rollback":
		sentinel := errors.New("staged helper rollback")
		err := publishStagedSet(context.Background(), spec.directory, []outputFile{
			{path: spec.target, content: []byte("staged first")},
			{path: spec.second, content: []byte("staged second")},
		}, stagedSetHooks{beforeRename: func(index int) error {
			if index != 1 {
				return nil
			}
			writePublisherLockMarker(t, spec.ready)
			if err := waitForPublisherLockRelease(spec.release); err != nil {
				return err
			}
			return sentinel
		}})
		if !errors.Is(err, sentinel) {
			t.Fatalf("staged helper error = %v", err)
		}
	case "hold":
		root, err := os.OpenRoot(spec.directory)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		lock, err := acquirePublisherLock(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.close()
		writePublisherLockMarker(t, spec.ready)
		for {
			time.Sleep(time.Second)
		}
	default:
		t.Fatalf("unknown publisher lock helper mode %q", spec.mode)
	}
}

func startPublisherLockHelper(t testing.TB, spec publisherLockHelperSpec) *publisherLockHelperProcess {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestPublisherLockProcessHelper$")
	command.Env = append(os.Environ(),
		"VV_I18N_PUBLISHER_LOCK_HELPER=1",
		"VV_I18N_PUBLISHER_LOCK_MODE="+spec.mode,
		"VV_I18N_PUBLISHER_LOCK_DIRECTORY="+spec.directory,
		"VV_I18N_PUBLISHER_LOCK_TARGET="+spec.target,
		"VV_I18N_PUBLISHER_LOCK_SECOND="+spec.second,
		"VV_I18N_PUBLISHER_LOCK_CONTENT="+spec.content,
		"VV_I18N_PUBLISHER_LOCK_STARTED="+spec.started,
		"VV_I18N_PUBLISHER_LOCK_READY="+spec.ready,
		"VV_I18N_PUBLISHER_LOCK_RELEASE="+spec.release,
	)
	output := &bytes.Buffer{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	process := &publisherLockHelperProcess{command: command, done: done, output: output}
	t.Cleanup(func() {
		_ = command.Process.Kill()
	})
	return process
}

func waitForPublisherLockMarker(t testing.TB, process *publisherLockHelperProcess, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-process.done:
			t.Fatalf("publisher lock helper exited before %q: %v\n%s", path, err, process.output.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("publisher lock helper did not create %q\n%s", path, process.output.String())
}

func assertPublisherLockHelperWaits(t testing.TB, process *publisherLockHelperProcess, acquired string) {
	t.Helper()
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-process.done:
		t.Fatalf("waiting publisher exited before lock release: %v\n%s", err, process.output.String())
	case <-timer.C:
	}
	if _, err := os.Stat(acquired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("waiting publisher reached its commit hook before lock release: %v", err)
	}
}

func waitForPublisherLockHelper(t testing.TB, process *publisherLockHelperProcess) {
	t.Helper()
	select {
	case err := <-process.done:
		if err != nil {
			t.Fatalf("publisher lock helper failed: %v\n%s", err, process.output.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("publisher lock helper did not exit\n%s", process.output.String())
	}
}

func writePublisherLockMarker(t testing.TB, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitForPublisherLockRelease(path string) error {
	if path == "" {
		return nil
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.New("publisher lock helper release timed out")
}
