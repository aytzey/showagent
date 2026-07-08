package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/aytzey/showagent/internal/session"
	"github.com/aytzey/showagent/internal/tui"
)

// version is stamped by the release build via -ldflags "-X main.version=...".
var version = "dev"

const usageLine = "usage: showagent [list [--json] | resume <id|latest> [--yolo] | setup]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches CLI arguments and returns the process exit code. It is
// separated from main so argument handling stays testable.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runDefault(stdout, stderr)
	}

	switch args[0] {
	case "--help", "-h", "help":
		printHelp(stdout)
		return 0
	case "--version", "-v", "version":
		fmt.Fprintf(stdout, "showagent %s\n", versionString())
		return 0
	case "list":
		return runList(args[1:], stdout, stderr)
	case "resume":
		return runResume(args[1:], stderr)
	case "setup":
		if len(args) != 1 {
			return usageError(stderr, fmt.Sprintf("setup takes no arguments, got %q", args[1]))
		}
		return runSetup(stdout, stderr)
	default:
		return usageError(stderr, fmt.Sprintf("unknown argument %q", args[0]))
	}
}

// runDefault keeps the original no-argument behavior: an interactive picker on
// a terminal, and a plain table when output is piped or redirected.
func runDefault(stdout, stderr io.Writer) int {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		rows := session.Discover()
		if len(rows) == 0 {
			return printNoSessions(stderr)
		}
		tui.PrintTable(stdout, terminalWidth(stdout), rows)
		return 0
	}

	selection, err := tui.Run()
	if err != nil {
		fmt.Fprintf(stderr, "showagent: %v\n", err)
		return 1
	}
	if selection == nil {
		return 0
	}

	var actErr error
	switch selection.Action {
	case tui.ActionCompound:
		actErr = session.Compound(selection.Row, selection.Agent, selection.Options)
	default:
		actErr = session.Resume(selection.Row, selection.Options)
	}
	if actErr != nil {
		fmt.Fprintf(stderr, "showagent: %v\n", actErr)
		return 1
	}
	return 0
}

// listItem is the machine-consumable shape emitted by `showagent list --json`.
// Field names are part of the CLI contract; do not rename them.
type listItem struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Workspace    string `json:"workspace"`
	Updated      string `json:"updated"`
	FirstMessage string `json:"first_message"`
	LastMessage  string `json:"last_message"`
}

func runList(args []string, stdout, stderr io.Writer) int {
	asJSON := false
	for _, arg := range args {
		if arg == "--json" {
			asJSON = true
			continue
		}
		return usageError(stderr, fmt.Sprintf("unknown list argument %q", arg))
	}

	rows := session.Discover()
	if asJSON {
		items := make([]listItem, 0, len(rows))
		for _, row := range rows {
			updated := ""
			if !row.LastAt.IsZero() {
				updated = row.LastAt.Format(time.RFC3339)
			}
			items = append(items, listItem{
				ID:           row.ID,
				Provider:     string(row.Provider),
				Workspace:    row.CWD,
				Updated:      updated,
				FirstMessage: row.FirstUser,
				LastMessage:  row.LastUser,
			})
		}
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(items); err != nil {
			fmt.Fprintf(stderr, "showagent: %v\n", err)
			return 1
		}
		return 0
	}

	if len(rows) == 0 {
		return printNoSessions(stderr)
	}
	tui.PrintTable(stdout, terminalWidth(stdout), rows)
	return 0
}

// printNoSessions explains exactly which directories were scanned and how to
// get a first session listed, then returns exit code 1.
func printNoSessions(stderr io.Writer) int {
	fmt.Fprintln(stderr, "showagent: no supported local sessions found")
	fmt.Fprintln(stderr, "scanned:")
	for _, target := range session.ScanTargets() {
		line := fmt.Sprintf("  %-8s %s  (override with %s)", target.Provider, target.Path, target.EnvVar)
		if target.Note != "" {
			line += "  — " + target.Note
		}
		fmt.Fprintln(stderr, line)
	}
	fmt.Fprintln(stderr, "start a conversation with codex or claude, then run showagent again")
	return 1
}

func runResume(args []string, stderr io.Writer) int {
	id := ""
	options := session.ResumeOptions{}
	for _, arg := range args {
		switch {
		case arg == "--yolo":
			options.Dangerous = true
		case id == "":
			id = arg
		default:
			return usageError(stderr, fmt.Sprintf("unexpected resume argument %q", arg))
		}
	}
	if id == "" {
		return usageError(stderr, "resume needs a session id or 'latest'")
	}

	row, err := resolveSession(session.Discover(), id)
	if err != nil {
		fmt.Fprintf(stderr, "showagent: %v\n", err)
		return 1
	}
	if err := session.Resume(row, options); err != nil {
		fmt.Fprintf(stderr, "showagent: %v\n", err)
		return 1
	}
	return 0
}

// resolveSession maps a user-supplied id (or the literal "latest") to a
// discovered session row. Rows are expected newest-first, as returned by
// session.Discover.
func resolveSession(rows []session.Row, id string) (session.Row, error) {
	if len(rows) == 0 {
		return session.Row{}, fmt.Errorf("no supported local sessions found")
	}
	if id == "latest" {
		return rows[0], nil
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return session.Row{}, fmt.Errorf("session %q not found; run 'showagent list' to see session ids", id)
}

func runSetup(stdout, stderr io.Writer) int {
	results, err := session.EnsureCompoundEngineeringPlugin()
	if err != nil {
		fmt.Fprintf(stderr, "showagent setup: %v\n", err)
		return 1
	}
	for _, result := range results {
		extra := ""
		if result.MarketplaceAdded {
			extra = " (marketplace added)"
		}
		fmt.Fprintf(stdout, "%s: %s%s\n", result.Provider, result.Status(), extra)
	}
	return 0
}

func usageError(stderr io.Writer, message string) int {
	fmt.Fprintf(stderr, "showagent: %s\n%s\nrun 'showagent --help' for details\n", message, usageLine)
	return 2
}

func printHelp(w io.Writer) {
	fmt.Fprintf(w, `showagent — browse, resume, branch, and hand off local Codex, Claude Code, OpenCode, and jcode sessions.

Usage:
  showagent                          open the interactive session picker
  showagent list [--json]            print sessions (plain table, or JSON with --json)
  showagent resume <id|latest> [--yolo]
                                     resume a session directly, without the picker
  showagent setup                    install the compound-engineering plugin for supported agents

Flags:
  -h, --help                         show this help
  -v, --version                      print version
  --json                             (list) emit a JSON array of sessions
  --yolo                             (resume) skip the agent's permission prompts

Picker keys:
  enter resume · y yolo · space collapse group · / search · t scope
  x hand off to another agent · n branch a copy · C compound · d/del delete
  p cycle preview (first/latest/both) · 1..9 toggle providers · r rescan
  ? full help · esc clear search/overlay · q quit

Session locations:
  codex     ~/.codex/sessions             (override with CODEX_HOME)
  claude    ~/.claude/projects            (override with CLAUDE_HOME)
  jcode     ~/.jcode/sessions             (override with JCODE_HOME)
  opencode  ~/.local/share/opencode       (override with OPENCODE_DATA_HOME;
                                           read via the opencode CLI)

When stdout is not a terminal, 'showagent' prints the plain table (same as 'showagent list').
`)
}

// versionString prefers the release-stamped version and falls back to Go
// module build info so 'go install ...@latest' builds still report a version.
func versionString() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

// terminalWidth reports the width of the terminal behind w, falling back to
// 120 when w is not a terminal or the size cannot be determined.
func terminalWidth(w io.Writer) int {
	file, ok := w.(*os.File)
	if !ok {
		return 120
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		return 120
	}
	return width
}

func isTerminal(file *os.File) bool {
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}
