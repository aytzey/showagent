//go:build !unix

package session

func syscallExec(_ string, _ []string, _ []string) error {
	return errNoExec()
}
