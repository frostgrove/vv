package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"time"
)

const publisherLockName = ".vv-i18n.lock"

var errPublisherLockChanged = errors.New("publisher lock file changed")

type publisherLock struct {
	root     *os.Root
	file     *os.File
	identity fs.FileInfo
}

func ensurePublisherLockSupported() error {
	if platformPublisherLockSupported() {
		return nil
	}
	return fmt.Errorf("publisher locking is unsupported on %s", runtime.GOOS)
}

func acquirePublisherLock(ctx context.Context, root *os.Root) (*publisherLock, error) {
	if ctx == nil {
		return nil, errors.New("publisher lock context is nil")
	}
	if err := ensurePublisherLockSupported(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, identity, created, err := openPublisherLockFile(root)
	if err != nil {
		return nil, err
	}
	locked := false
	defer func() {
		if !locked {
			_ = file.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		acquired, err := tryPlatformPublisherLock(file)
		if err != nil {
			return nil, fmt.Errorf("acquire publisher lock: %w", err)
		}
		if acquired {
			locked = true
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	lock := &publisherLock{root: root, file: file, identity: identity}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, lock.release())
	}
	if err := lock.validate(); err != nil {
		return nil, errors.Join(err, lock.release())
	}
	if created {
		if err := file.Sync(); err != nil {
			return nil, errors.Join(fmt.Errorf("sync publisher lock file: %w", err), lock.release())
		}
		if err := syncRootDirectory(root); err != nil {
			return nil, errors.Join(err, lock.release())
		}
	}
	return lock, nil
}

func openPublisherLockFile(root *os.Root) (*os.File, fs.FileInfo, bool, error) {
	for range 32 {
		info, err := root.Lstat(publisherLockName)
		if errors.Is(err, fs.ErrNotExist) {
			file, createErr := root.OpenFile(publisherLockName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(createErr, fs.ErrExist) {
				continue
			}
			if createErr != nil {
				return nil, nil, false, fmt.Errorf("create publisher lock file: %w", createErr)
			}
			identity, validateErr := validateOpenedPublisherLock(root, file, nil)
			if validateErr != nil {
				_ = file.Close()
				return nil, nil, false, validateErr
			}
			return file, identity, true, nil
		}
		if err != nil {
			return nil, nil, false, fmt.Errorf("inspect publisher lock file: %w", err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, nil, false, errors.New("publisher lock path is not a regular file")
		}
		file, err := root.Open(publisherLockName)
		if err != nil {
			return nil, nil, false, fmt.Errorf("open publisher lock file: %w", err)
		}
		identity, validateErr := validateOpenedPublisherLock(root, file, info)
		if validateErr != nil {
			_ = file.Close()
			return nil, nil, false, validateErr
		}
		return file, identity, false, nil
	}
	return nil, nil, false, errors.New("publisher lock file could not be opened after concurrent creation")
}

func validateOpenedPublisherLock(root *os.Root, file *os.File, inspected fs.FileInfo) (fs.FileInfo, error) {
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened publisher lock file: %w", err)
	}
	current, err := root.Lstat(publisherLockName)
	if err != nil {
		return nil, fmt.Errorf("inspect publisher lock file identity: %w", err)
	}
	if opened.Mode()&fs.ModeSymlink != 0 || !opened.Mode().IsRegular() || current.Mode()&fs.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return nil, errPublisherLockChanged
	}
	if inspected != nil && !os.SameFile(inspected, opened) {
		return nil, errPublisherLockChanged
	}
	return opened, nil
}

func (lock *publisherLock) validate() error {
	if lock == nil || lock.root == nil || lock.file == nil || lock.identity == nil {
		return errors.New("publisher lock is not initialized")
	}
	opened, err := lock.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect held publisher lock file: %w", err)
	}
	current, err := lock.root.Lstat(publisherLockName)
	if err != nil {
		return fmt.Errorf("inspect held publisher lock identity: %w", err)
	}
	if !opened.Mode().IsRegular() || current.Mode()&fs.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(lock.identity, opened) || !os.SameFile(lock.identity, current) {
		return errPublisherLockChanged
	}
	return nil
}

func (lock *publisherLock) validateTarget(info fs.FileInfo, path string) error {
	if lock == nil || lock.identity == nil {
		return errors.New("publisher lock is not initialized")
	}
	if info != nil && os.SameFile(lock.identity, info) {
		return fmt.Errorf("publication target %q aliases the reserved publisher lock", path)
	}
	return nil
}

func (lock *publisherLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	validationErr := lock.validate()
	releaseErr := lock.release()
	return errors.Join(validationErr, releaseErr)
}

func (lock *publisherLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	unlockErr := unlockPlatformPublisherLock(file)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}
