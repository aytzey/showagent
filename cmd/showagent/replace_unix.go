//go:build !windows

package main

import "os"

func replaceInstalledFile(source, destination string) error {
	return os.Rename(source, destination)
}
