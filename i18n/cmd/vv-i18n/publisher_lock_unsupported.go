//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !windows

package main

import (
	"errors"
	"os"
)

func platformPublisherLockSupported() bool {
	return false
}

func tryPlatformPublisherLock(*os.File) (bool, error) {
	return false, errors.New("publisher locking is unsupported")
}

func unlockPlatformPublisherLock(*os.File) error {
	return errors.New("publisher locking is unsupported")
}
