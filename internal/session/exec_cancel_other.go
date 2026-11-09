//go:build !unix && !windows

package session

import "os/exec"

func configureCommandCancellation(*exec.Cmd) {}
