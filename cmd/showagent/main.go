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

	rows := session.Discover()
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "showagent: no Codex or Claude sessions found")
		os.Exit(1)
	}

	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		tui.PrintTable(os.Stdout, rows)
		return
	}

	selection, err := tui.Pick(rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "showagent: %v\n", err)
		os.Exit(1)
	}
	if selection == nil {
		return
	}

	if err := runSelection(*selection); err != nil {
		fmt.Fprintf(os.Stderr, "showagent: %v\n", err)
		os.Exit(1)
	}
}

func runSelection(selection tui.Selection) error {
	switch selection.Action {
	case tui.ActionHandoff:
		return session.Handoff(selection.Row, session.OtherProvider(selection.Row.Provider), selection.Options, selection.Handoff)
	case tui.ActionFork:
		return session.Fork(selection.Row, selection.Options)
	default:
		return session.Resume(selection.Row, selection.Options)
	}
}

func isTerminal(file *os.File) bool {
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}
