# showcodex

`showcodex` is a fast terminal picker for local Codex and Claude Code sessions.
It scans every workspace, shows both providers in one timeline, and resumes the
selected session with the right CLI command.

It is built as a portable Go TUI with:

- Bubble Tea v2 for the application loop and terminal renderer
- Bubbles v2 for the searchable session list
- Lip Gloss v2 for styling and layout

## Features

- Unified session list for `codex` and `claude`
- Provider column: `codex` or `claude`
- Sorted by latest conversation time
- Search across provider, directory, session id, and user messages
- Preview modes for first user message, latest user message, or both
- One-key resume:
  - Codex: `codex resume <session-id>`
  - Claude Code: `claude --resume <session-id>`
- No runtime dependencies beyond the compiled binary
- Works well on Ubuntu, Debian, Fedora, Arch, and other Linux distributions

## Install

With Go 1.25 or newer:

```bash
go install github.com/aytzey/showcodex/cmd/showcodex@latest
```

Or download a Linux binary from the GitHub releases page and put it somewhere
on your `PATH`, for example `~/.local/bin`.

## Usage

```bash
showcodex
```

`showcodex` intentionally takes no arguments. Everything is selected inside the
CLI.

Keybindings:

| Key | Action |
| --- | --- |
| `↑/↓`, `j/k` | Move through sessions |
| `/` | Search |
| `c` | Toggle Codex sessions |
| `d` | Toggle Claude Code sessions |
| `f` | Show first user message |
| `l` | Show latest user message |
| `b` | Show first + latest user messages |
| `enter` | Resume selected session |
| `q`, `esc`, `ctrl+c` | Quit |

When output is piped, `showcodex` prints a plain table instead of opening the
TUI.

## Session Locations

By default, `showcodex` reads:

- Codex: `~/.codex/sessions/**/*.jsonl`
- Claude Code: `~/.claude/projects/**/*.jsonl`

Environment overrides:

- `CODEX_HOME`
- `CLAUDE_HOME`

Claude subagent transcripts under `subagents/` are ignored so the list stays
focused on top-level conversations.

## Build From Source

```bash
git clone https://github.com/aytzey/showcodex.git
cd showcodex
go test ./...
go build -o showcodex ./cmd/showcodex
```

For a portable Linux binary:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o showcodex-linux-amd64 ./cmd/showcodex
```

## Privacy

`showcodex` only reads local JSONL history files. It does not upload session
data anywhere.

Message previews apply basic redaction for password-like words and OpenAI-style
API keys before rendering them.

## License

MIT
