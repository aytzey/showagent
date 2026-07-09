package session

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ConversionPreview is the trust-before-write summary shown by `showagent
// convert --dry-run` and by the picker before the user confirms a hand-off.
type ConversionPreview struct {
	SourceProvider Provider
	SourceID       string
	SourceLocation string
	TargetProvider Provider
	Workspace      string
	Scope          string
	TransferTurns  int
	LastUser       string
	Dropped        []string
	Warnings       []string
}

// ResumeRecipe explains exactly how showagent will resume a row and where that
// session lives. It is intentionally plain data so CLI, TUI, and tests can
// render it differently without duplicating provider knowledge.
type ResumeRecipe struct {
	Provider        Provider
	ID              string
	Command         []string
	CommandString   string
	CWD             string
	SourceLocation  string
	StorageLocation string
	Note            string
}

// PreviewConversion extracts the transferable transcript and summarizes the
// hand-off without writing anything into the target agent's session store.
func PreviewConversion(row Row, target Provider, options HandoffOptions) (ConversionPreview, error) {
	if _, ok := providerFor(target); !ok {
		return ConversionPreview{}, fmt.Errorf("unsupported target provider %q", target)
	}

	turns, err := Transcript(row)
	if err != nil {
		return ConversionPreview{}, err
	}
	turns = options.apply(turns)
	if len(turns) == 0 {
		return ConversionPreview{}, fmt.Errorf("source session has no transferable user or assistant turns")
	}

	_, lastUser := userPreviewFromTurns(turns)
	preview := ConversionPreview{
		SourceProvider: row.Provider,
		SourceID:       row.ID,
		SourceLocation: SourceLocation(row),
		TargetProvider: target,
		Workspace:      row.resumeCWD(),
		Scope:          options.Label(),
		TransferTurns:  len(turns),
		LastUser:       lastUser,
		Dropped: []string{
			"tool calls and tool results",
			"permission/approval state",
			"model, provider, token, and runtime metadata",
			"sidechain, subagent, cache, and checkpoint internals",
		},
	}
	if row.Provider == target {
		preview.Warnings = append(preview.Warnings, "target provider matches source; this creates a local branch/copy")
	}
	if strings.TrimSpace(row.resumeCWD()) == "" || strings.HasPrefix(strings.TrimSpace(row.resumeCWD()), "(") {
		preview.Warnings = append(preview.Warnings, "source workspace is unknown; some target providers may refuse conversion")
	}
	if target == ProviderOpenCode && !ProviderCommandAvailable(ProviderOpenCode) {
		preview.Warnings = append(preview.Warnings, "opencode CLI is not on PATH; writing the conversion will fail until it is installed")
	}
	return preview, nil
}

// RecipeFor builds the resume recipe for an existing or newly-created session.
func RecipeFor(row Row, options ResumeOptions) ResumeRecipe {
	command := row.ResumeCommand(options)
	return ResumeRecipe{
		Provider:        row.Provider,
		ID:              row.ID,
		Command:         command,
		CommandString:   ShellCommand(command),
		CWD:             row.resumeCWD(),
		SourceLocation:  SourceLocation(row),
		StorageLocation: StorageLocation(row),
		Note:            recipeNote(row),
	}
}

func SourceLocation(row Row) string {
	if row.File != "" {
		return row.File
	}
	switch row.Provider {
	case ProviderOpenCode:
		return filepath.Join(defaultOpenCodeDataHome(), "opencode*.db") + " via `opencode export " + row.ID + "`"
	default:
		return "(unknown source location)"
	}
}

func StorageLocation(row Row) string {
	if row.File != "" {
		return row.File
	}
	switch row.Provider {
	case ProviderOpenCode:
		return filepath.Join(defaultOpenCodeDataHome(), "opencode*.db") + " via `opencode import`"
	default:
		return "(unknown storage location)"
	}
}

func recipeNote(row Row) string {
	if row.Provider == ProviderOpenCode {
		return "OpenCode sessions live in its SQLite database; showagent reads and writes through the opencode CLI."
	}
	return ""
}

// ShellCommand renders argv in a copy-pasteable shell form.
func ShellCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool { return !isShellSafeRune(r) }) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func isShellSafeRune(r rune) bool {
	return r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' ||
		(r >= '0' && r <= '9') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z')
}
