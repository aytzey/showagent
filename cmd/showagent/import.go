package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aytzey/showagent/internal/importer"
	"github.com/aytzey/showagent/internal/session"
)

const (
	maxImportInputBytes   = 10 * 1024 * 1024
	maxImportPreviewRunes = 120
)

type importCLIOptions struct {
	url       string
	file      string
	stdin     bool
	format    string
	asNote    bool
	target    string
	cwd       string
	into      string
	dryRun    bool
	asJSON    bool
	inputKind string
}

type importCLIResult struct {
	Operation            string `json:"operation"`
	Source               string `json:"source"`
	Provider             string `json:"provider"`
	SessionID            string `json:"session_id,omitempty"`
	Workspace            string `json:"workspace"`
	MessageCount         int    `json:"message_count"`
	FirstMessage         string `json:"first_message,omitempty"`
	LastMessage          string `json:"last_message,omitempty"`
	NativeHistoryVisible bool   `json:"native_history_visible"`
	NoOp                 bool   `json:"no_op,omitempty"`
	DryRun               bool   `json:"dry_run"`
	Warning              string `json:"warning,omitempty"`
}

func runImport(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	options, err := parseImportArgs(args)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	conversation, err := loadImportConversation(context.Background(), options, stdin)
	if err != nil {
		var ambiguity *importer.AmbiguityError
		if errors.As(err, &ambiguity) {
			return usageError(stderr, "import input: "+err.Error())
		}
		_, _ = fmt.Fprintf(stderr, "showagent: import input: %v\n", err)
		return 1
	}
	messages := make([]session.ImportMessage, 0, len(conversation.Messages))
	for _, message := range conversation.Messages {
		messages = append(messages, session.ImportMessage{Role: string(message.Role), Text: message.Text})
	}

	request := session.ImportRequest{Mode: session.ImportCreate, Messages: messages}
	if options.into != "" {
		provider, id, err := parseImportTarget(options.into)
		if err != nil {
			return usageError(stderr, err.Error())
		}
		row, err := resolveSession(session.Discover(), id)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "showagent: %v\n", err)
			return 1
		}
		if row.Provider != provider {
			return usageError(stderr, fmt.Sprintf("session %q belongs to %s, not %s", id, row.Provider, provider))
		}
		request.Mode = session.ImportAppendContext
		request.Provider = provider
		request.Target = row
	} else {
		provider, err := session.ParseProvider(options.target)
		if err != nil {
			return usageError(stderr, err.Error())
		}
		cwd, err := filepath.Abs(options.cwd)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "showagent: resolve import workspace: %v\n", err)
			return 1
		}
		request.Provider = provider
		request.CWD = cwd
	}

	plan, err := session.PlanImport(request)
	if err != nil {
		return usageError(stderr, err.Error())
	}
	result := importCLIResult{
		Operation:    string(plan.Mode),
		Source:       options.inputKind,
		Provider:     string(plan.Provider),
		Workspace:    plan.CWD,
		MessageCount: plan.MessageCount,
		DryRun:       options.dryRun,
		Warning:      importWarning(plan.Mode, plan.Provider),
	}
	if plan.Mode == session.ImportAppendContext {
		result.SessionID = plan.Target.ID
	}
	if options.dryRun {
		result.FirstMessage = importPreviewBoundary(conversation.Messages[0])
		result.LastMessage = importPreviewBoundary(conversation.Messages[len(conversation.Messages)-1])
		return printImportResult(stdout, stderr, result, options.asJSON)
	}

	receipt, err := session.ApplyImport(context.Background(), plan)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "showagent: import failed: %v\n", err)
		return 1
	}
	result.Operation = string(receipt.Mode)
	result.Provider = string(receipt.Provider)
	result.SessionID = receipt.Row.ID
	result.Workspace = receipt.Row.CWD
	result.MessageCount = receipt.MessageCount
	result.NativeHistoryVisible = receipt.NativeHistoryVisible
	result.NoOp = receipt.NoOp
	result.DryRun = false
	return printImportResult(stdout, stderr, result, options.asJSON)
}

func parseImportArgs(args []string) (importCLIOptions, error) {
	options := importCLIOptions{format: "auto"}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := func(name string) (string, error) {
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", fmt.Errorf("import %s needs a value", name)
			}
			return args[index], nil
		}
		var err error
		switch arg {
		case "--url":
			options.url, err = value(arg)
		case "--file":
			options.file, err = value(arg)
		case "--stdin":
			options.stdin = true
		case "--format":
			options.format, err = value(arg)
		case "--as-note":
			options.asNote = true
		case "--to":
			options.target, err = value(arg)
		case "--cwd":
			options.cwd, err = value(arg)
		case "--into":
			options.into, err = value(arg)
		case "--dry-run":
			options.dryRun = true
		case "--json":
			options.asJSON = true
		default:
			return options, fmt.Errorf("unknown import argument %q", arg)
		}
		if err != nil {
			return options, err
		}
	}
	sources := 0
	if options.url != "" {
		sources++
		options.inputKind = "share link"
	}
	if options.file != "" {
		sources++
		options.inputKind = "transcript file"
	}
	if options.stdin {
		sources++
		options.inputKind = "pasted text"
	}
	if sources != 1 {
		return options, fmt.Errorf("import needs exactly one source: --url, --file, or --stdin")
	}
	if options.format != "auto" && options.format != "text" && options.format != "json" {
		return options, fmt.Errorf("import --format must be auto, text, or json")
	}
	if options.asNote && options.url != "" {
		return options, fmt.Errorf("import --as-note is only valid with text from --file or --stdin")
	}
	if options.asNote && (options.format == "json" || (options.format == "auto" && strings.EqualFold(filepath.Ext(options.file), ".json"))) {
		return options, fmt.Errorf("import --as-note cannot be combined with JSON input")
	}
	if options.into != "" {
		if options.target != "" || options.cwd != "" {
			return options, fmt.Errorf("choose either --to with --cwd, or --into")
		}
	} else if options.target == "" || options.cwd == "" {
		return options, fmt.Errorf("new-session import needs --to <provider> and --cwd <directory>")
	}
	return options, nil
}

func parseImportTarget(value string) (session.Provider, string, error) {
	providerText, id, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(providerText) == "" || strings.TrimSpace(id) == "" {
		return "", "", fmt.Errorf("import --into needs provider:exact-session-id")
	}
	if id == "latest" {
		return "", "", fmt.Errorf("import --into needs an exact session id; 'latest' is not accepted")
	}
	provider, err := session.ParseProvider(providerText)
	return provider, id, err
}

func loadImportConversation(ctx context.Context, options importCLIOptions, stdin io.Reader) (importer.Conversation, error) {
	if options.url != "" {
		if options.format != "auto" {
			return importer.Conversation{}, fmt.Errorf("--format is only valid with --file or --stdin")
		}
		return importer.ImportURL(ctx, options.url, importer.FetchOptions{})
	}
	reader := stdin
	if options.file != "" {
		file, err := os.Open(options.file)
		if err != nil {
			return importer.Conversation{}, err
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	data, err := readImportInput(reader)
	if err != nil {
		return importer.Conversation{}, err
	}
	format := options.format
	if format == "auto" && options.file != "" && strings.EqualFold(filepath.Ext(options.file), ".json") {
		format = "json"
	}
	if format == "json" {
		conversation, err := importer.ParseJSON(data)
		return conversation, err
	}
	sourceKind := importer.SourcePastedText
	if options.file != "" {
		sourceKind = importer.SourceTranscriptFile
	}
	return importer.ParseText(string(data), importer.TextOptions{
		AsOneContextNote: options.asNote,
		SourceKind:       sourceKind,
	})
}

func readImportInput(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxImportInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportInputBytes {
		return nil, fmt.Errorf("conversation exceeds the %d MiB input limit", maxImportInputBytes/(1024*1024))
	}
	return data, nil
}

func importWarning(mode session.ImportMode, provider session.Provider) string {
	if mode == session.ImportAppendContext && provider == session.ProviderCodex {
		return "Saved to this session's context. Imported messages may not appear as chat bubbles in Codex."
	}
	return "Text only. Attachments, tool state, permissions, and files are not imported."
}

func importPreviewBoundary(message importer.Message) string {
	value := fmt.Sprintf("%s: %s", message.Role, session.RedactSecrets(session.SafeDisplayText(message.Text)))
	runes := []rune(value)
	if len(runes) <= maxImportPreviewRunes {
		return value
	}
	return string(runes[:maxImportPreviewRunes-3]) + "..."
}

func printImportResult(stdout, stderr io.Writer, result importCLIResult, asJSON bool) int {
	if asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			_, _ = fmt.Fprintf(stderr, "showagent: %v\n", err)
			return 1
		}
		return 0
	}
	heading := "import complete"
	if result.DryRun {
		heading = "import preview"
	} else if result.NoOp {
		heading = "import already complete"
	}
	_, _ = fmt.Fprintln(stdout, heading)
	_, _ = fmt.Fprintf(stdout, "  source:    %s\n", result.Source)
	_, _ = fmt.Fprintf(stdout, "  operation: %s\n", result.Operation)
	_, _ = fmt.Fprintf(stdout, "  target:    %s\n", result.Provider)
	if result.SessionID != "" {
		_, _ = fmt.Fprintf(stdout, "  session:   %s\n", session.SafeDisplayText(result.SessionID))
	}
	_, _ = fmt.Fprintf(stdout, "  workspace: %s\n", session.SafeDisplayText(result.Workspace))
	_, _ = fmt.Fprintf(stdout, "  content:   %d messages · text only\n", result.MessageCount)
	if result.FirstMessage != "" {
		_, _ = fmt.Fprintf(stdout, "  first:     %s\n", result.FirstMessage)
	}
	if result.LastMessage != "" {
		_, _ = fmt.Fprintf(stdout, "  last:      %s\n", result.LastMessage)
	}
	if result.Warning != "" {
		_, _ = fmt.Fprintf(stdout, "  warning:   %s\n", result.Warning)
	}
	return 0
}
