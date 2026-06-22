package main

import (
	"fmt"
	"os"

	"github.com/aytzey/showagent/internal/session"
	"github.com/aytzey/showagent/internal/tui"
)

func main() {
	if len(os.Args) > 1 {
		fmt.Fprintln(os.Stderr, "showagent does not take arguments. Run: showagent")
		os.Exit(2)
	}

	// When output is not an interactive terminal, print a plain table so the
	// tool stays scriptable (pipes, redirects, CI).
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		rows := session.Discover()
		if len(rows) == 0 {
			fmt.Fprintln(os.Stderr, "showagent: no Codex or Claude sessions found")
			os.Exit(1)
		}
		tui.PrintTable(os.Stdout, rows)
		return
	}

	selection, err := tui.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "showagent: %v\n", err)
		os.Exit(1)
	}
	if selection == nil {
		return
	}

	var actErr error
	switch selection.Action {
	case tui.ActionCompound:
		actErr = session.Compound(selection.Row, selection.Agent, selection.Options)
	default:
		actErr = session.Resume(selection.Row, selection.Options)
	}
	if actErr != nil {
		fmt.Fprintf(os.Stderr, "showagent: %v\n", actErr)
		os.Exit(1)
	}
}

func isTerminal(file *os.File) bool {
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}
