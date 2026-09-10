package main

import (
	"os"
	"strings"
	"testing"
)

func TestDirectorySyncPolicyIsCompileTimeSelected(t *testing.T) {
	windowsSource, err := os.ReadFile("directory_sync_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	windows := string(windowsSource)
	if !strings.HasPrefix(windows, "//go:build windows\n") || strings.Contains(windows, ".Sync(") || strings.Contains(windows, "errors.Is") || strings.Count(windows, "return nil") != 2 {
		t.Fatalf("Windows directory-sync policy is not an explicit compile-time no-op:\n%s", windows)
	}
	nonWindowsSource, err := os.ReadFile("directory_sync_nonwindows.go")
	if err != nil {
		t.Fatal(err)
	}
	nonWindows := string(nonWindowsSource)
	if !strings.HasPrefix(nonWindows, "//go:build !windows\n") || !strings.Contains(nonWindows, "directory.Sync()") {
		t.Fatalf("non-Windows directory-sync policy does not call File.Sync:\n%s", nonWindows)
	}
	for _, name := range []string{"atomic.go", "publication.go"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), "directoryFile.Sync()") {
			t.Fatalf("%s bypasses the platform directory-sync policy", name)
		}
	}
}

func TestDirectorySyncCurrentPlatform(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("child", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syncRootPath(root, "child"); err != nil {
		t.Fatal(err)
	}
	if err := syncRootDirectory(root); err != nil {
		t.Fatal(err)
	}
}
