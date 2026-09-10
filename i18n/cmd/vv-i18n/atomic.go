package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type publishHooks struct {
	beforeRename func() error
}

type stagedSetHooks struct {
	beforeRename func(int) error
}

type outputFile struct {
	path    string
	content []byte
}

func writeOutput(ctx context.Context, path string, content []byte, check bool) error {
	return writeOutputBounded(ctx, path, content, check, maximumCommandInput)
}

func writeOutputBounded(ctx context.Context, path string, content []byte, check bool, maximum int) error {
	if ctx == nil {
		return errors.New("publication context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return fmt.Errorf("output limit %d is outside supported bounds", maximum)
	}
	if len(content) > maximum {
		return fmt.Errorf("output %q exceeds %d bytes", path, maximum)
	}
	if check {
		existing, err := readRegularFile(ctx, path, int64(maximum))
		if err != nil {
			return err
		}
		if !bytes.Equal(existing, content) {
			return fmt.Errorf("output %q is stale", path)
		}
		return nil
	}
	return publishAtomic(ctx, path, content, publishHooks{})
}

func writeOutputSet(ctx context.Context, outputs []outputFile, check bool) error {
	return writeOutputSetBounded(ctx, outputs, check, maximumCommandInput)
}

func writeOutputSetBounded(ctx context.Context, outputs []outputFile, check bool, maximum int) error {
	if ctx == nil {
		return errors.New("publication context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return fmt.Errorf("output limit %d is outside supported bounds", maximum)
	}
	if len(outputs) == 0 {
		return errors.New("output set is empty")
	}
	seen := make(map[string]bool, len(outputs))
	resolved := make([]outputFile, len(outputs))
	directory := ""
	for index, output := range outputs {
		if len(output.content) > maximum {
			return fmt.Errorf("output %q exceeds %d bytes", output.path, maximum)
		}
		absolute, candidateDirectory, _, err := publicationTarget(output.path)
		if err != nil {
			return err
		}
		if seen[absolute] {
			return fmt.Errorf("output path %q is repeated", output.path)
		}
		seen[absolute] = true
		if directory == "" {
			directory = candidateDirectory
		} else if !check && directory != candidateDirectory {
			return errors.New("a staged output set must share one directory")
		}
		resolved[index] = outputFile{path: absolute, content: output.content}
	}
	if check {
		for _, output := range resolved {
			if err := writeOutputBounded(ctx, output.path, output.content, true, maximum); err != nil {
				return err
			}
		}
		return nil
	}
	return publishStagedSetBounded(ctx, directory, resolved, stagedSetHooks{}, maximum)
}

type stagedOutput struct {
	name      string
	temporary string
	backup    string
	existed   bool
	published bool
	identity  fs.FileInfo
	candidate fs.FileInfo
	protected fs.FileInfo
}

func publishStagedSet(ctx context.Context, directory string, outputs []outputFile, hooks stagedSetHooks) error {
	return publishStagedSetBounded(ctx, directory, outputs, hooks, maximumCommandInput)
}

func publishStagedSetBounded(ctx context.Context, directory string, outputs []outputFile, hooks stagedSetHooks, maximum int) (resultErr error) {
	if ctx == nil {
		return errors.New("publication context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return fmt.Errorf("output limit %d is outside supported bounds", maximum)
	}
	if err := ensurePublisherLockSupported(); err != nil {
		return err
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve publication directory: %w", err)
	}
	absoluteDirectory = filepath.Clean(absoluteDirectory)
	root, err := openStableAbsoluteRoot(ctx, absoluteDirectory, false, nil)
	if err != nil {
		return fmt.Errorf("open publication directory: %w", err)
	}
	defer root.Close()
	lock, err := acquirePublisherLock(ctx, root)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lock.close())
	}()
	staged := make([]stagedOutput, len(outputs))
	defer cleanupStagedOutputs(root, staged)
	for index, output := range outputs {
		_, candidateDirectory, name, targetErr := publicationTarget(output.path)
		if targetErr != nil {
			return targetErr
		}
		if candidateDirectory != absoluteDirectory {
			return errors.New("a staged output set must share one directory")
		}
		mode, identity, _, targetErr := inspectPublicationTarget(root, name, output.path)
		if targetErr != nil {
			return targetErr
		}
		if targetErr := lock.validateTarget(identity, output.path); targetErr != nil {
			return targetErr
		}
		staged[index].name = name
		staged[index].protected = lock.identity
		temporary, file, createErr := createTemporary(root, name, mode)
		if createErr != nil {
			return createErr
		}
		staged[index].temporary = temporary
		if writeErr := writeFileContext(ctx, file, output.content); writeErr != nil {
			_ = file.Close()
			return writeErr
		}
		if syncErr := file.Sync(); syncErr != nil {
			_ = file.Close()
			return fmt.Errorf("sync temporary output: %w", syncErr)
		}
		candidate, inspectErr := file.Stat()
		if inspectErr != nil || !candidate.Mode().IsRegular() {
			_ = file.Close()
			return errors.Join(errors.New("inspect temporary output candidate"), inspectErr)
		}
		staged[index].candidate = candidate
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close temporary output: %w", closeErr)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for index := range staged {
		if err := stageOutputBackupBounded(ctx, root, &staged[index], maximum); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for index := range staged {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
		if hooks.beforeRename != nil {
			if err := hooks.beforeRename(index); err != nil {
				return errors.Join(err, rollbackOutputSet(root, staged))
			}
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
		if err := validateStagedOutputTarget(root, staged[index]); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
		if err := validateStagedOutputCandidate(root, staged[index]); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
		if err := lock.validate(); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
		if err := root.Rename(staged[index].temporary, staged[index].name); err != nil {
			return errors.Join(fmt.Errorf("publish output %q: %w", outputs[index].path, err), rollbackOutputSet(root, staged))
		}
		staged[index].temporary = ""
		staged[index].published = true
		if err := validatePublishedOutputCandidate(root, staged[index]); err != nil {
			return errors.Join(err, rollbackOutputSet(root, staged))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollbackOutputSet(root, staged))
	}
	if err := syncRootDirectory(root); err != nil {
		return errors.Join(err, rollbackOutputSet(root, staged))
	}
	for index := range staged {
		if staged[index].backup == "" {
			continue
		}
		if err := root.Remove(staged[index].backup); err != nil {
			return fmt.Errorf("remove publication backup: %w", err)
		}
		staged[index].backup = ""
	}
	return syncRootDirectory(root)
}

func stageOutputBackup(ctx context.Context, root *os.Root, output *stagedOutput) error {
	return stageOutputBackupBounded(ctx, root, output, maximumCommandInput)
}

func stageOutputBackupBounded(ctx context.Context, root *os.Root, output *stagedOutput, maximum int) error {
	info, err := root.Lstat(output.name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect publication target: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("publication target %q is not a regular file", output.name)
	}
	if output.protected != nil && os.SameFile(output.protected, info) {
		return fmt.Errorf("publication target %q aliases the reserved publisher lock", output.name)
	}
	if info.Size() > int64(maximum) {
		return fmt.Errorf("publication target %q exceeds %d bytes", output.name, maximum)
	}
	source, err := root.Open(output.name)
	if err != nil {
		return fmt.Errorf("open publication target backup: %w", err)
	}
	openedInfo, err := source.Stat()
	if err != nil {
		_ = source.Close()
		return fmt.Errorf("inspect opened publication target: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
		_ = source.Close()
		return fmt.Errorf("publication target %q changed while staging", output.name)
	}
	output.identity = info
	backup, destination, err := createTemporary(root, output.name+".backup", info.Mode().Perm())
	if err != nil {
		_ = source.Close()
		return err
	}
	output.backup = backup
	copied, copyErr := copyReaderContext(ctx, destination, io.LimitReader(source, int64(maximum)+1))
	if copyErr == nil && copied > int64(maximum) {
		copyErr = fmt.Errorf("publication target %q exceeds %d bytes", output.name, maximum)
	}
	sourceCloseErr := source.Close()
	syncErr := destination.Sync()
	destinationCloseErr := destination.Close()
	if copyErr != nil || sourceCloseErr != nil || syncErr != nil || destinationCloseErr != nil {
		return errors.Join(copyErr, sourceCloseErr, syncErr, destinationCloseErr)
	}
	output.existed = true
	return nil
}

func validateStagedOutputTarget(root *os.Root, output stagedOutput) error {
	info, err := root.Lstat(output.name)
	if !output.existed {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect publication target before commit: %w", err)
		}
		return fmt.Errorf("publication target %q appeared before commit", output.name)
	}
	if err != nil {
		return fmt.Errorf("inspect publication target before commit: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(output.identity, info) {
		return fmt.Errorf("publication target %q changed before commit", output.name)
	}
	return nil
}

func validateStagedOutputCandidate(root *os.Root, output stagedOutput) error {
	info, err := root.Lstat(output.temporary)
	if err != nil {
		return fmt.Errorf("inspect temporary output candidate: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || output.candidate == nil || !os.SameFile(output.candidate, info) {
		return fmt.Errorf("temporary output candidate %q changed before commit", output.name)
	}
	return nil
}

func validatePublishedOutputCandidate(root *os.Root, output stagedOutput) error {
	info, err := root.Lstat(output.name)
	if err != nil {
		return fmt.Errorf("inspect published output candidate %q: %w", output.name, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || output.candidate == nil || !os.SameFile(output.candidate, info) {
		return fmt.Errorf("published output candidate %q changed identity", output.name)
	}
	return nil
}

func copyReaderContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != read {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
		if read == 0 {
			return total, io.ErrNoProgress
		}
	}
}

func rollbackOutputSet(root *os.Root, staged []stagedOutput) error {
	var joined error
	for index := len(staged) - 1; index >= 0; index-- {
		output := &staged[index]
		if !output.published {
			continue
		}
		if err := validatePublishedOutputCandidate(root, *output); err != nil {
			preserved := output.backup
			output.backup = ""
			output.published = false
			if preserved != "" {
				joined = errors.Join(joined, fmt.Errorf("refuse rollback of output %q after concurrent replacement; backup preserved as %q: %w", output.name, preserved, err))
			} else {
				joined = errors.Join(joined, fmt.Errorf("refuse rollback of output %q after concurrent replacement: %w", output.name, err))
			}
			continue
		}
		if output.existed && output.backup != "" {
			if err := root.Rename(output.backup, output.name); err != nil {
				preserved := output.backup
				output.backup = ""
				joined = errors.Join(joined, fmt.Errorf("restore output %q from preserved backup %q: %w", output.name, preserved, err))
			} else {
				output.backup = ""
			}
		} else if err := root.Remove(output.name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			joined = errors.Join(joined, fmt.Errorf("remove output %q during rollback: %w", output.name, err))
		}
		output.published = false
	}
	joined = errors.Join(joined, syncRootDirectory(root))
	return joined
}

func cleanupStagedOutputs(root *os.Root, staged []stagedOutput) {
	for _, output := range staged {
		if output.temporary != "" {
			_ = root.Remove(output.temporary)
		}
		if output.backup != "" {
			_ = root.Remove(output.backup)
		}
	}
}

func publishAtomic(ctx context.Context, path string, content []byte, hooks publishHooks) (resultErr error) {
	if ctx == nil {
		return errors.New("publication context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, directory, name, err := publicationTarget(path)
	if err != nil {
		return err
	}
	if err := ensurePublisherLockSupported(); err != nil {
		return err
	}
	root, err := openStableAbsoluteRoot(ctx, directory, false, nil)
	if err != nil {
		return fmt.Errorf("open publication directory: %w", err)
	}
	defer root.Close()
	lock, err := acquirePublisherLock(ctx, root)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lock.close())
	}()
	mode, identity, existed, err := inspectPublicationTarget(root, name, path)
	if err != nil {
		return err
	}
	if err := lock.validateTarget(identity, path); err != nil {
		return err
	}

	temporary, file, err := createTemporary(root, name, mode)
	if err != nil {
		return err
	}
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = root.Remove(temporary)
		}
	}()
	if err := writeFileContext(ctx, file, content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if hooks.beforeRename != nil {
		if err := hooks.beforeRename(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePublicationTarget(root, name, path, identity, existed); err != nil {
		return err
	}
	if err := lock.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	keepTemporary = false
	return syncRootDirectory(root)
}

func publicationTarget(path string) (string, string, string, error) {
	if path == "" {
		return "", "", "", errors.New("output path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", "", "", fmt.Errorf("resolve output path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	directory := filepath.Dir(absolute)
	name := filepath.Base(absolute)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return "", "", "", fmt.Errorf("output path %q has no file name", path)
	}
	if publisherLockNameReserved(name) {
		return "", "", "", fmt.Errorf("output path %q uses the reserved publisher lock name", path)
	}
	return absolute, directory, name, nil
}

func publisherLockNameReserved(name string) bool {
	base, _, _ := strings.Cut(name, ":")
	return strings.EqualFold(strings.TrimRight(base, ". "), publisherLockName)
}

func inspectPublicationTarget(root *os.Root, name, path string) (fs.FileMode, fs.FileInfo, bool, error) {
	mode := fs.FileMode(0o644)
	if info, err := root.Lstat(name); err == nil {
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return 0, nil, false, fmt.Errorf("publication target %q is not a regular file", path)
		}
		return info.Mode().Perm(), info, true, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, nil, false, fmt.Errorf("inspect publication target: %w", err)
	}
	return mode, nil, false, nil
}

func validatePublicationTarget(root *os.Root, name, path string, identity fs.FileInfo, existed bool) error {
	info, err := root.Lstat(name)
	if !existed {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect publication target: %w", err)
		}
		return fmt.Errorf("publication target %q appeared before commit", path)
	}
	if err != nil {
		return fmt.Errorf("inspect publication target: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || !os.SameFile(identity, info) {
		return fmt.Errorf("publication target %q changed before commit", path)
	}
	return nil
}

func openStableAbsoluteRoot(ctx context.Context, path string, createFinal bool, beforeCreate func() error) (*os.Root, error) {
	if ctx == nil {
		return nil, errors.New("path context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	volumeRoot := filepath.VolumeName(absolute) + string(filepath.Separator)
	relative, err := filepath.Rel(volumeRoot, absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve path components: %w", err)
	}
	root, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, fmt.Errorf("open volume root: %w", err)
	}
	if relative == "." {
		return root, nil
	}
	components := strings.Split(relative, string(filepath.Separator))
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			_ = root.Close()
			return nil, errors.New("path contains an invalid component")
		}
		if err := ctx.Err(); err != nil {
			_ = root.Close()
			return nil, err
		}
		last := index == len(components)-1
		info, inspectErr := root.Lstat(component)
		if errors.Is(inspectErr, fs.ErrNotExist) && createFinal && last {
			if beforeCreate != nil {
				if err := beforeCreate(); err != nil {
					_ = root.Close()
					return nil, err
				}
			}
			if err := ctx.Err(); err != nil {
				_ = root.Close()
				return nil, err
			}
			created := false
			if err := root.Mkdir(component, 0o755); err == nil {
				created = true
			} else if !errors.Is(err, fs.ErrExist) {
				_ = root.Close()
				return nil, fmt.Errorf("create final directory: %w", err)
			}
			if created {
				if err := syncRootDirectory(root); err != nil {
					_ = root.Close()
					return nil, err
				}
			}
			info, inspectErr = root.Lstat(component)
		}
		if inspectErr != nil {
			_ = root.Close()
			return nil, fmt.Errorf("inspect path component %q: %w", component, inspectErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close()
			return nil, fmt.Errorf("path component %q is a symbolic link", component)
		}
		if !info.Mode().IsDir() {
			_ = root.Close()
			return nil, fmt.Errorf("path component %q is not a regular directory", component)
		}
		next, openErr := root.OpenRoot(component)
		if openErr != nil {
			_ = root.Close()
			return nil, fmt.Errorf("open path component %q: %w", component, openErr)
		}
		opened, statErr := next.Stat(".")
		if statErr != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			_ = root.Close()
			return nil, fmt.Errorf("path component %q changed while opening", component)
		}
		if closeErr := root.Close(); closeErr != nil {
			_ = next.Close()
			return nil, closeErr
		}
		root = next
	}
	return root, nil
}

func openStableParent(ctx context.Context, path string) (*os.Root, string, string, error) {
	if path == "" {
		return nil, "", "", errors.New("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	name := filepath.Base(absolute)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return nil, "", "", fmt.Errorf("path %q has no file name", path)
	}
	root, err := openStableAbsoluteRoot(ctx, filepath.Dir(absolute), false, nil)
	if err != nil {
		return nil, "", "", err
	}
	return root, name, absolute, nil
}

func openStableRootedParent(ctx context.Context, base *os.Root, path string) (*os.Root, string, error) {
	path = filepath.Clean(path)
	if path == "." || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return nil, "", fmt.Errorf("rooted path %q has no file name", path)
	}
	name := filepath.Base(path)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return nil, "", fmt.Errorf("rooted path %q has no file name", path)
	}
	root, err := openStableRootedDirectory(ctx, base, filepath.Dir(path))
	if err != nil {
		return nil, "", err
	}
	return root, name, nil
}

func openStableRootedDirectory(ctx context.Context, base *os.Root, path string) (*os.Root, error) {
	if ctx == nil {
		return nil, errors.New("path context is nil")
	}
	if base == nil {
		return nil, errors.New("rooted base is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("rooted directory %q escapes its base", path)
	}
	baseInfo, err := base.Stat(".")
	if err != nil {
		return nil, err
	}
	root, err := base.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(baseInfo, opened) {
		_ = root.Close()
		return nil, errors.New("rooted base changed while opening")
	}
	if path == "." {
		if err := ctx.Err(); err != nil {
			_ = root.Close()
			return nil, err
		}
		return root, nil
	}
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			_ = root.Close()
			return nil, errors.New("rooted path contains an invalid component")
		}
		if err := ctx.Err(); err != nil {
			_ = root.Close()
			return nil, err
		}
		info, err := root.Lstat(component)
		if err != nil {
			_ = root.Close()
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close()
			return nil, fmt.Errorf("rooted path component %q is a symbolic link", component)
		}
		if !info.Mode().IsDir() {
			_ = root.Close()
			return nil, fmt.Errorf("rooted path component %q is not a regular directory", component)
		}
		next, err := root.OpenRoot(component)
		if err != nil {
			_ = root.Close()
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			_ = root.Close()
			return nil, fmt.Errorf("rooted path component %q changed while opening", component)
		}
		if err := root.Close(); err != nil {
			_ = next.Close()
			return nil, err
		}
		root = next
	}
	if err := ctx.Err(); err != nil {
		_ = root.Close()
		return nil, err
	}
	return root, nil
}

func createTemporary(root *os.Root, target string, mode fs.FileMode) (string, *os.File, error) {
	var random [12]byte
	for range 32 {
		if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
			return "", nil, fmt.Errorf("generate temporary output name: %w", err)
		}
		name := "." + target + ".tmp-" + hex.EncodeToString(random[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, fmt.Errorf("create temporary output: %w", err)
		}
	}
	return "", nil, errors.New("cannot allocate a unique temporary output")
}

func writeFileContext(ctx context.Context, destination *os.File, content []byte) error {
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		written, err := destination.Write(content)
		if err != nil {
			return fmt.Errorf("write temporary output: %w", err)
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		content = content[written:]
	}
	return ctx.Err()
}

func readRegularFile(ctx context.Context, path string, maximum int64) ([]byte, error) {
	file, err := openRegularFile(ctx, path, maximum)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	content, err := readContextBounded(ctx, file, maximum)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return content, nil
}

func openRegularFile(ctx context.Context, path string, maximum int64) (*os.File, error) {
	if ctx == nil {
		return nil, errors.New("read context is nil")
	}
	if maximum < 1 || maximum > maximumCommandOutputBytes {
		return nil, fmt.Errorf("input limit %d is outside supported bounds", maximum)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, name, absolute, err := openStableParent(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open input parent %q: %w", path, err)
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect %q: %w", absolute, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("input %q is not a regular file", absolute)
	}
	if info.Size() > maximum {
		return nil, fmt.Errorf("input %q exceeds %d bytes", absolute, maximum)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", absolute, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect opened input %q: %w", absolute, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("input %q changed while opening", absolute)
	}
	current, err := root.Lstat(name)
	if err != nil || current.Mode()&fs.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		_ = file.Close()
		return nil, fmt.Errorf("input %q changed while opening", absolute)
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func readContextBounded(ctx context.Context, reader io.Reader, maximum int64) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("read context is nil")
	}
	if maximum < 0 || maximum > maximumCommandOutputBytes {
		return nil, fmt.Errorf("byte limit %d is outside supported bounds", maximum)
	}
	capacity := min(maximum+1, 64<<10)
	raw := make([]byte, 0, int(capacity))
	buffer := make([]byte, 32<<10)
	limited := io.LimitReader(reader, maximum+1)
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, err := limited.Read(buffer)
		if count > 0 {
			raw = append(raw, buffer[:count]...)
			if int64(len(raw)) > maximum {
				return nil, fmt.Errorf("content exceeds %d bytes", maximum)
			}
			emptyReads = 0
		} else {
			emptyReads++
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if errors.Is(err, io.EOF) {
			return raw, nil
		}
		if err != nil {
			return nil, err
		}
		if emptyReads >= 100 {
			return nil, io.ErrNoProgress
		}
	}
}
