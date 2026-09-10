//go:build windows

package main

import "os"

func syncRootDirectory(*os.Root) error {
	return nil
}

func syncRootPath(*os.Root, string) error {
	return nil
}
