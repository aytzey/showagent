//go:build !unix && !windows

package session

import (
	"errors"
	"os"
)

func openTrimWriter(string) (*os.File, error) {
	return nil, errors.New("session replacement locking is unavailable on this platform")
}

func replaceTrimFile(from, to string) error { return os.Rename(from, to) }
