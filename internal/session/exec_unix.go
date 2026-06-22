//go:build unix

package session

import "golang.org/x/sys/unix"

func syscallExec(path string, argv []string, env []string) error {
	return unix.Exec(path, argv, env)
}
