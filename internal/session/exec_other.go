//go:build !unix

package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// syscallExec approximates Unix exec-replacement semantics on platforms
// without execve (notably Windows): it runs the command as a child process
// with the terminal attached and then exits with the child's exit code, so
// callers behave as if the process image had been replaced.
func syscallExec(path string, argv []string, env []string) error {
	command := exec.Command(path)
	command.Args = argv
	command.Env = env
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err := command.Run()
	if err == nil {
		os.Exit(0)
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			code = 1
		}
		os.Exit(code)
	}
	return fmt.Errorf("run %s: %w", argv[0], err)
}
