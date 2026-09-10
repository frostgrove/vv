//go:build windows

package main

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	windowsLockFileFailImmediately = 0x00000001
	windowsLockFileExclusive       = 0x00000002
	windowsErrorLockViolation      = syscall.Errno(33)
)

var windowsKernel32 = syscall.NewLazyDLL("kernel32.dll")
var windowsLockFileEx = windowsKernel32.NewProc("LockFileEx")
var windowsUnlockFileEx = windowsKernel32.NewProc("UnlockFileEx")

func platformPublisherLockSupported() bool {
	return true
}

func tryPlatformPublisherLock(file *os.File) (bool, error) {
	overlapped := syscall.Overlapped{}
	result, _, callErr := windowsLockFileEx.Call(
		file.Fd(),
		windowsLockFileFailImmediately|windowsLockFileExclusive,
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	runtime.KeepAlive(file)
	if result != 0 {
		return true, nil
	}
	if errors.Is(callErr, windowsErrorLockViolation) {
		return false, nil
	}
	return false, callErr
}

func unlockPlatformPublisherLock(file *os.File) error {
	overlapped := syscall.Overlapped{}
	result, _, callErr := windowsUnlockFileEx.Call(
		file.Fd(),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	runtime.KeepAlive(file)
	if result != 0 {
		return nil
	}
	return callErr
}
