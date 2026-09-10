//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
)

func syncRootDirectory(root *os.Root) error {
	return syncRootPath(root, ".")
}

func syncRootPath(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("open publication directory for sync: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(syncErr, closeErr)
	}
	return nil
}
