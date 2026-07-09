//go:build windows

package main

import (
	"fmt"
	"os"
)

// Windows does not let os.Rename replace an existing destination. Move the
// current executable aside first, install the new one, and roll back if the
// second rename fails. A running executable may keep the .old file locked
// until process exit, so cleanup is deliberately best-effort.
func replaceInstalledFile(source, destination string) error {
	backup := destination + ".old"
	_ = os.Remove(backup)

	hadDestination := true
	if err := os.Rename(destination, backup); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("move current executable aside: %w", err)
		}
		hadDestination = false
	}
	if err := os.Rename(source, destination); err != nil {
		if hadDestination {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("place new executable: %w", err)
	}
	if hadDestination {
		_ = os.Remove(backup)
	}
	return nil
}
