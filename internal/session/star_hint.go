package session

import (
	"os"
	"path/filepath"
)

// StarHint is shown once, after the first successful conversion on a machine.
// Kept short so it fits the TUI status line on an 80-column terminal.
const StarHint = "worked? a ★ helps: github.com/aytzey/showagent"

// FirstConversion reports true exactly once per state directory, recording
// that the star hint was shown. Any file error just skips the hint.
func FirstConversion() bool {
	directory, err := stateDirectory()
	if err != nil {
		return false
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return false
	}
	file, err := os.OpenFile(filepath.Join(directory, "star-hint-shown"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	return file.Close() == nil
}
