//go:build unix

package session

import (
	"os"

	"golang.org/x/sys/unix"
)

func openTrimWriter(path string) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func replaceTrimFile(from, to string) error { return os.Rename(from, to) }
