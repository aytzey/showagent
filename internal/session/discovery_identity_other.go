//go:build !unix && !windows

package session

import "os"

func discoveryFileIdentity(string, os.FileInfo) string { return "" }
