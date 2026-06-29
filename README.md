# showagent

`showagent` is a fast terminal picker for local Codex, Claude Code, and optional
JCode sessions. It scans every workspace, shows available providers in one
timeline, and resumes the selected session with the right CLI command.

It is built as a portable Go TUI with:

- Bubble Tea v2 for the application loop and terminal renderer
- Bubbles v2 for the searchable session list
- Lip Gloss v2 for styling and layout

## Features

- Unified session list for `codex`, `claude`, and optional `jcode`, grouped by workspace folder
  (groups and the sessions inside them are ordered newest-first)
- One-key compound-engineering launch (`C`): pick Codex or Claude and it resumes
  the session and captures durable learnings into a shared, per-project pool both
  tools read and write
- Provider badges, relative timestamps, and a focused detail panel
- Instant startup: sessions are scanned in the background behind a spinner
- Adaptive theme that adjusts to light and dark terminals (and honors `NO_COLOR`)
- `?` toggles a full keybinding overlay so the header stays compact
- Search across provider, directory, session id, and user messages
- Preview modes for first user message, latest user message, or both
- Toggleable yolo resume mode for approval-free continuation
- Selectable cross-agent transfer target and scope: all history by default, or latest N turns
- One-key resume, branch, cross-agent conversion, and delete:
  - Codex: `codex resume <session-id>`
  - Codex yolo: `codex resume --dangerously-bypass-approvals-and-sandbox <session-id>`
  - Codex delete: `codex delete --force <session-id>`
  - Claude Code: `claude --resume <session-id>`
  - Claude Code yolo: `claude --dangerously-skip-permissions --resume <session-id>`
  - Claude Code delete: removes the selected local session JSONL file
  - JCode: `jcode --no-update --resume <session-id>`
  - JCode delete: removes the selected local session JSON file
  - Branch creates a new local session copy without leaving the picker
  - Cross-agent conversion writes a new target-provider session file and keeps you in the picker
- No runtime dependencies beyond the compiled binary
- Works well on Ubuntu, Debian, Fedora, Arch, and other Linux distributions

## Install

With Go 1.25 or newer:

```bash
go install github.com/aytzey/showagent/cmd/showagent@latest
```

Or download a Linux binary from the GitHub releases page and put it somewhere
on your `PATH`, for example `~/.local/bin`.

Then install the companion Compound Engineering plugin for the local Codex and
Claude Code CLIs that are present on the machine:

```bash
showagent setup
```

`setup` is idempotent. It registers
`EveryInc/compound-engineering-plugin` and installs
`compound-engineering@compound-engineering-plugin` only when the plugin is
missing.

## Usage

```bash
showagent
```

`showagent setup` handles companion plugin setup. Everything else is selected
inside the CLI.

Keybindings:

| Key | Action |
| --- | --- |
| `↑/↓`, `j/k` | Move through sessions |
| `pgup/pgdn` | Page through sessions |
| `/` | Search (press `esc` to clear an applied search) |
| `?` | Toggle the full keybinding overlay |
| `c` | Toggle Codex sessions |
| `d` | Toggle Claude Code sessions |
| `z` | Toggle JCode sessions, when JCode is installed and has local sessions |
| `y` | Toggle yolo/dangerous resume mode |
| `o` | Cycle the cross-agent transfer target for the selected session |
| `t` | Cycle cross-agent transfer scope: all, latest 200, 100, 50, 20, or 10 turns |
| `f` | Show first user message |
| `l` | Show latest user message |
| `b` | Show first + latest user messages |
| `space` | Collapse or expand the selected workspace group |
| `enter` | Resume selected session |
| `enter` on a group | Collapse or expand that workspace group |
| `C` | Compound: pick Codex or Claude to resume the session and capture learnings |
| `x` | Convert selected session to the selected target agent and select the new session |
| `n` | Create a full local branch/copy of the selected session and select it |
| `delete`, `backspace` | Delete selected session after second press confirmation |
| `q`, `ctrl+c` | Quit (`esc` clears an active search first) |

When output is piped, `showagent` prints a plain table instead of opening the
TUI.

Cross-agent conversion preserves the selected user/assistant transcript as a new
local session in the selected target provider's format, then selects that new
session in the picker. Press `enter` to resume it. It intentionally does not copy
private runtime state such as tool-call internals, approval history, encrypted
reasoning blobs, or provider attachments. The default scope is the full transcript.

JCode support is optional. If `jcode` is not on `PATH`, `showagent` silently skips
JCode discovery and does not show JCode controls as available targets. When
`jcode` is present, sessions are read from `~/.jcode/sessions/*.json` and other
providers can be converted into JCode session JSON.

## Compound Engineering

Press `C` on a session and choose **Codex** or **Claude**. `showagent` resumes
that session in the chosen agent and starts it straight into a
compound-engineering pass: review what was solved, then record the durable
learnings as markdown.

Learnings are pooled **per project** but **shared across both tools**: each
workspace gets its own directory under `~/.showagent/learnings/<project>/`, and
both Codex and Claude read from and write to it. So knowledge compounds across
Codex and Claude for a project, while different projects never mix.

- The shared root is `~/.showagent/learnings/` (override with
  `SHOWAGENT_LEARNINGS_DIR`); each project lands in its own subdirectory.
- If you pick the agent that did *not* create the session, `showagent` first
  converts the session to that provider so it has full context.

## Session Locations

By default, `showagent` reads:

- Codex: `~/.codex/sessions/**/*.jsonl`
- Claude Code: `~/.claude/projects/**/*.jsonl`
- JCode, when installed: `~/.jcode/sessions/*.json`

Environment overrides:

- `CODEX_HOME`
- `CLAUDE_HOME`
- `JCODE_HOME`

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
