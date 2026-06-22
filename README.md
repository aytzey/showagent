# showagent

`showagent` is a fast terminal picker for local Codex and Claude Code sessions.
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
- Toggleable yolo resume mode for approval-free continuation
- Selectable cross-agent handoff scope: all history by default, or latest N turns
- One-key resume, branch, cross-agent handoff, and delete:
  - Codex: `codex resume <session-id>`
  - Codex yolo: `codex resume --dangerously-bypass-approvals-and-sandbox <session-id>`
  - Codex branch: `codex fork <session-id>`
  - Codex delete: `codex delete --force <session-id>`
  - Claude Code: `claude --resume <session-id>`
  - Claude Code yolo: `claude --dangerously-skip-permissions --resume <session-id>`
  - Claude Code branch: `claude --fork-session --resume <session-id>`
  - Claude Code delete: removes the selected local session JSONL file
  - Cross-agent handoff starts the other agent in the same workspace with the selected transcript scope
- No runtime dependencies beyond the compiled binary
- Works well on Ubuntu, Debian, Fedora, Arch, and other Linux distributions

## Install

With Go 1.25 or newer:

```bash
go install github.com/aytzey/showagent/cmd/showagent@latest
```

Or download a Linux binary from the GitHub releases page and put it somewhere
on your `PATH`, for example `~/.local/bin`.

## Usage

```bash
showagent
```

`showagent` intentionally takes no arguments. Everything is selected inside the
CLI.

Keybindings:

| Key | Action |
| --- | --- |
| `↑/↓`, `j/k` | Move through sessions |
| `/` | Search |
| `c` | Toggle Codex sessions |
| `d` | Toggle Claude Code sessions |
| `y` | Toggle yolo/dangerous resume mode |
| `t` | Cycle cross-agent handoff scope: all, latest 200, 100, 50, 20, or 10 turns |
| `f` | Show first user message |
| `l` | Show latest user message |
| `b` | Show first + latest user messages |
| `enter` | Resume selected session |
| `x` | Continue selected session in the other agent |
| `n` | Create a branch/fork of the selected session |
| `delete`, `backspace`, `D` | Delete selected session after second press confirmation |
| `q`, `esc`, `ctrl+c` | Quit |

When output is piped, `showagent` prints a plain table instead of opening the
TUI.

Cross-agent continuation cannot import the provider's private native session
state. It starts the other agent in the same workspace with the selected
transcript scope so you can continue the work from the latest useful context.
The default scope is the full transcript.

## Session Locations

By default, `showagent` reads:

- Codex: `~/.codex/sessions/**/*.jsonl`
- Claude Code: `~/.claude/projects/**/*.jsonl`

Environment overrides:

- `CODEX_HOME`
- `CLAUDE_HOME`

Claude subagent transcripts under `subagents/` are ignored so the list stays
focused on top-level conversations.

## Build From Source

```bash
git clone https://github.com/aytzey/showagent.git
cd showagent
go test ./...
go build -o showagent ./cmd/showagent
```

For a portable Linux binary:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o showagent-linux-amd64 ./cmd/showagent
```

## Privacy

`showagent` only reads local JSONL history files. It does not upload session
data anywhere.

Message previews apply basic redaction for password-like words and OpenAI-style
API keys before rendering them.

## License

MIT
